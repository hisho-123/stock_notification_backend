//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hisho-123/stock_notification_backend/internal/bootstrap"
	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

type cancelOnStart struct {
	io.Writer
	cancel context.CancelFunc
}

func (w cancelOnStart) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if bytes.Contains(data, []byte(`"msg":"web_started"`)) {
		w.cancel()
	}
	return n, err
}

func TestBootstrapAndCLIProcesses(t *testing.T) {
	_, _, dsn := database(t)
	c := config.Config{DatabaseURL: dsn, FMPAPIKey: "dummy", LINEToken: "dummy", LINEChannelSecret: "dummy", PublicOrigin: "https://stock.example.com", HTTPAddr: "invalid"}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if got, err := bootstrap.Monitor(context.Background(), c, logger); err != nil || !got.Success() || got != (usecase.MonitorResult{}) {
		t.Fatal(got, err)
	}
	if err := bootstrap.Serve(context.Background(), c, logger); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	c.HTTPAddr = "127.0.0.1:0"
	if err := bootstrap.Serve(ctx, c, slog.New(slog.NewJSONHandler(cancelOnStart{&logs, cancel}, nil))); err != nil || !strings.Contains(logs.String(), `"msg":"web_started"`) {
		t.Fatal(err, logs.String())
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "stock-notification")
	build := exec.Command("go", "build", "-race", "-cover", "-covermode=atomic", "-coverpkg=github.com/hisho-123/stock_notification_backend/...", "-o", binary, "../../cmd/stock-notification")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	coverDir := os.Getenv("STOCK_CLI_COVERDIR")
	if coverDir == "" {
		coverDir = filepath.Join(dir, "coverage")
	}
	if err := os.MkdirAll(coverDir, 0700); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	for _, command := range []string{"monitor", "serve"} {
		for _, source := range []string{"environment", "dotenv"} {
			t.Run(command+"/"+source, func(t *testing.T) {
				workDir := t.TempDir()
				cmd := exec.Command(binary, command)
				cmd.Dir = workDir
				cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir, "DATABASE_URL="+dsn, "FMP_API_KEY=dummy", "LINE_TOKEN=dummy", "LINE_CHANNEL_SECRET=dummy", "PUBLIC_ORIGIN=https://stock.example.com", "HTTP_ADDR="+occupied.Addr().String(), "TRUSTED_PROXY_CIDRS=")
				if source == "dotenv" {
					contents := fmt.Sprintf("DATABASE_URL='%s'\nFMP_API_KEY='dummy'\nLINE_TOKEN='dummy'\nLINE_CHANNEL_SECRET='dummy'\nPUBLIC_ORIGIN='https://stock.example.com'\nHTTP_ADDR='%s'\nTRUSTED_PROXY_CIDRS=''\n", dsn, occupied.Addr().String())
					if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte(contents), 0600); err != nil {
						t.Fatal(err)
					}
					cmd.Env = append(cmd.Env, "DATABASE_URL=", "FMP_API_KEY=", "LINE_TOKEN=", "LINE_CHANNEL_SECRET=")
				}
				output, err := cmd.CombinedOutput()
				want := 0
				if command == "serve" {
					want = 1
				}
				got := 0
				if err != nil {
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatal(err)
					}
					got = exit.ExitCode()
				}
				if got != want || !strings.Contains(string(output), `"msg":"command_finished"`) {
					t.Fatal(got, string(output))
				}
			})
		}
	}
}
