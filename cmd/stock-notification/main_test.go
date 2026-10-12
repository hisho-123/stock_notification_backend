package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hisho-123/stock_notification_backend/internal/config"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func TestCommandValidationAndDispatch(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		name    string
		args    []string
		loadErr error
		status  int
	}{
		{"no command", nil, nil, 1}, {"unknown command", []string{"bad"}, nil, 1}, {"extra argument", []string{"serve", "extra"}, nil, 1},
		{"bad configuration", []string{"serve"}, errors.New("configuration error"), 1}, {"serve", []string{"serve"}, nil, 0}, {"monitor", []string{"monitor"}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			loaded := ""
			started := ""
			ctx := context.Background()
			configuration := config.Config{DatabaseURL: "sentinel"}
			load := func(name string) (config.Config, error) { loaded = name; return configuration, tc.loadErr }
			start := func(name string, status int) command {
				return func(gotCtx context.Context, c config.Config, l *slog.Logger) int {
					started = name
					if gotCtx != ctx || c.DatabaseURL != "sentinel" || l != logger {
						t.Error("dispatch lost dependencies")
					}
					return status
				}
			}
			got := runCommand(ctx, tc.args, logger, load, start("serve", 0), start("monitor", 1))
			if got != tc.status {
				t.Fatal(got, logs.String())
			}
			if tc.name == "serve" || tc.name == "monitor" {
				if started != tc.name || loaded != tc.name || !strings.Contains(logs.String(), `"msg":"command_finished"`) {
					t.Fatal(started, loaded, logs.String())
				}
			} else if started != "" {
				t.Fatal("invalid command started", started)
			}
		})
	}
	t.Setenv("DATABASE_URL", "")
	if code := run(context.Background(), []string{"serve"}); code != 1 {
		t.Fatal(code)
	}
	if code := run(context.Background(), nil); code != 1 {
		t.Fatal(code)
	}
}

func TestServeAndMonitorExitCodes(t *testing.T) {
	for _, failure := range []error{nil, errors.New("secret")} {
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		want := 0
		if failure != nil {
			want = 1
		}
		if got := serveWith(context.Background(), config.Config{}, logger, func(context.Context, config.Config, *slog.Logger) error { return failure }); got != want || strings.Contains(logs.String(), "secret") {
			t.Fatal(got, logs.String())
		}
	}
	for _, tc := range []struct {
		name      string
		result    usecase.MonitorResult
		err       error
		status    int
		errorCode string
	}{
		{"success", usecase.MonitorResult{Processed: 1}, nil, 0, ""}, {"failed observation", usecase.MonitorResult{Failed: 1}, nil, 1, ""},
		{"stale", usecase.MonitorResult{Stale: 1}, nil, 1, ""}, {"connection lost", usecase.MonitorResult{}, usecase.ErrConnectionLost, 1, "DB_CONNECTION_LOST"},
		{"unavailable", usecase.MonitorResult{}, usecase.ErrUnavailable, 1, "SERVICE_UNAVAILABLE"}, {"other", usecase.MonitorResult{}, errors.New("secret"), 1, "MONITOR_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			got := monitorWith(context.Background(), config.Config{}, logger, func(context.Context, config.Config, *slog.Logger) (usecase.MonitorResult, error) {
				return tc.result, tc.err
			})
			if got != tc.status || !strings.Contains(logs.String(), `"msg":"monitor_finished"`) || strings.Contains(logs.String(), "secret") {
				t.Fatal(got, logs.String())
			}
			if tc.errorCode != "" && !strings.Contains(logs.String(), `"error_code":"`+tc.errorCode+`"`) {
				t.Fatal(logs.String())
			}
		})
	}
	c := config.Config{DatabaseURL: "postgres://%zz"}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	if serve(context.Background(), c, logger) != 1 || monitor(context.Background(), c, logger) != 1 {
		t.Fatal("bootstrap failures must exit 1")
	}
}

func TestCLIProcess(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "stock-notification")
	build := exec.Command("go", "build", "-race", "-cover", "-covermode=atomic", "-coverpkg=github.com/hisho-123/stock_notification_backend/...", "-o", binary, ".")
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
	for _, tc := range []struct {
		name, command, contents string
	}{
		{name: "unknown command", command: "unknown"},
		{name: "missing configuration", command: "serve"},
		{name: "invalid dotenv serve", command: "serve", contents: "FMP_API_KEY='secret-invalid-dotenv\n"},
		{name: "invalid dotenv monitor", command: "monitor", contents: "SECRET!KEY=secret-invalid-dotenv\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			if tc.contents != "" {
				if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte(tc.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(binary, tc.command)
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(), "DATABASE_URL=", "GOCOVERDIR="+coverDir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("command=%s err=%v output=%s", tc.command, err, output)
			}
			if !strings.Contains(string(output), `"run_id":`) {
				t.Fatal("CLI did not emit structured logs", string(output))
			}
			if tc.contents != "" {
				if !strings.Contains(string(output), `"msg":"configuration_invalid"`) || !strings.Contains(string(output), `"error_code":"CONFIG_INVALID"`) || !strings.Contains(string(output), ".env") || strings.Contains(string(output), "secret-invalid-dotenv") || strings.Contains(string(output), "SECRET!KEY") {
					t.Fatal("CLI emitted an incorrect or unsafe configuration error", string(output))
				}
			}
		})
	}
}
