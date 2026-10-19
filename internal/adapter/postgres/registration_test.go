package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type sqlStep struct {
	sql    string
	values []any
	err    error
	rows   *rowsStub
}
type scriptedTransaction struct {
	pgx.Tx
	t                        *testing.T
	steps                    []sqlStep
	used, commits, rollbacks int
	commitErr                error
}

func (tx *scriptedTransaction) next(sql string) sqlStep {
	tx.t.Helper()
	if tx.used >= len(tx.steps) {
		tx.t.Fatalf("unexpected SQL after failed operation: %s", sql)
	}
	step := tx.steps[tx.used]
	tx.used++
	if !strings.Contains(sql, step.sql) {
		tx.t.Fatalf("unexpected operation: %s, expected %s", sql, step.sql)
	}
	return step
}
func (tx *scriptedTransaction) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	step := tx.next(sql)
	return pgconn.NewCommandTag("INSERT 0 1"), step.err
}
func (tx *scriptedTransaction) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	step := tx.next(sql)
	return rowStub{values: step.values, err: step.err}
}
func (tx *scriptedTransaction) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	step := tx.next(sql)
	return step.rows, step.err
}
func (tx *scriptedTransaction) Commit(context.Context) error   { tx.commits++; return tx.commitErr }
func (tx *scriptedTransaction) Rollback(context.Context) error { tx.rollbacks++; return nil }

type scriptedBegin struct {
	tx  pgx.Tx
	err error
}

func (b scriptedBegin) Begin(context.Context) (pgx.Tx, error) { return b.tx, b.err }
func registrationSteps(now time.Time) []sqlStep {
	return []sqlStep{
		{sql: "SELECT line_user_id", values: []any{"U00000000000000000000000000000002"}},
		{sql: "pg_advisory_xact_lock"},
		{sql: "SELECT token_hash", values: []any{"hash", "U00000000000000000000000000000002", now, now.Add(24 * time.Hour)}},
		{sql: "INSERT INTO users"}, {sql: "INSERT INTO notification_settings"}, {sql: "DELETE FROM sessions"}, {sql: "INSERT INTO sessions"}, {sql: "SET consumed_at"},
	}
}
func TestRegistrationRollsBackAtEveryPersistenceBoundary(t *testing.T) {
	now := time.Now()
	failure := errors.New("database unavailable")
	cases := []struct {
		name      string
		index     int
		err, want error
	}{
		{"begin", -1, failure, usecase.ErrUnavailable},
		{"unknown token", 0, pgx.ErrNoRows, usecase.ErrRegistrationUnavailable}, {"token lookup", 0, failure, usecase.ErrUnavailable},
		{"LINE serialization", 1, failure, usecase.ErrUnavailable},
		{"expired or revoked under lock", 2, pgx.ErrNoRows, usecase.ErrRegistrationUnavailable}, {"validation query", 2, failure, usecase.ErrUnavailable},
		{"user persistence", 3, failure, usecase.ErrUnavailable},
		{"same LINE conflict", 3, &pgconn.PgError{Code: "23505", ConstraintName: "users_line_user_id_key"}, usecase.ErrRegistrationUnavailable},
		{"same ID conflict", 3, &pgconn.PgError{Code: "23505", ConstraintName: "users_pkey"}, usecase.ErrLoginIDExists},
		{"initial settings", 4, failure, usecase.ErrUnavailable}, {"old browser revocation", 5, failure, usecase.ErrUnavailable},
		{"new session", 6, failure, usecase.ErrUnavailable}, {"token consumption", 7, failure, usecase.ErrUnavailable},
		{"commit", 8, failure, usecase.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &scriptedTransaction{t: t, steps: registrationSteps(now)}
			b := scriptedBegin{tx: tx}
			if tc.index < 0 {
				b.err = tc.err
			} else if tc.index == len(tx.steps) {
				tx.commitErr = tc.err
			} else {
				tx.steps[tc.index].err = tc.err
			}
			err := (&Store{b: b}).Register(context.Background(), "hash", domain.User{LoginID: "new"}, domain.Session{}, "old", now)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if tc.index < 0 {
				if tx.used != 0 || tx.rollbacks != 0 {
					t.Fatal(tx)
				}
				return
			}
			if tx.rollbacks != 1 || tc.index < len(tx.steps) && tx.commits != 0 || tc.index < len(tx.steps) && tx.used != tc.index+1 {
				t.Fatalf("partial registration was not stopped and rolled back: %+v", tx)
			}
		})
	}
}
func freshFollowSteps() []sqlStep {
	return []sqlStep{
		{sql: "pg_advisory_xact_lock"}, {sql: "SELECT event_type", err: pgx.ErrNoRows}, {sql: "SELECT max", err: pgx.ErrNoRows},
		{sql: "INSERT INTO line_webhook_events"}, {sql: "SELECT EXISTS", values: []any{false}},
		{sql: "SET revoked_at"}, {sql: "INSERT INTO registration_tokens"}, {sql: "reply_status='pending'"},
	}
}
func TestWebhookDoesNotCommitOrReplyOnPersistenceFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	now := time.Now()
	for stageIndex, stage := range []string{"begin", "LINE serialization", "friend history", "request history", "event history", "registered lookup", "old URL revocation", "new URL persistence", "reply reservation", "commit"} {
		index := stageIndex - 1
		t.Run(stage, func(t *testing.T) {
			tx := &scriptedTransaction{t: t, steps: freshFollowSteps()}
			b := scriptedBegin{tx: tx}
			if index < 0 {
				b.err = failure
			} else if index == len(tx.steps) {
				tx.commitErr = failure
			} else {
				tx.steps[index].err = failure
			}
			reply, err := (&Store{b: b}).ProcessLINEEvent(context.Background(), domain.LINEEvent{ID: "event", Type: "follow", Timestamp: 1}, domain.RegistrationToken{}, "register", "login", now)
			if !errors.Is(err, usecase.ErrUnavailable) || reply.Text != "" {
				t.Fatal(reply, err)
			}
			if index < 0 {
				if tx.rollbacks != 0 {
					t.Fatal(tx)
				}
				return
			}
			if tx.rollbacks != 1 || index < len(tx.steps) && tx.commits != 0 {
				t.Fatal("event committed after failure", tx)
			}
		})
	}
	t.Run("registered friend update", func(t *testing.T) {
		steps := freshFollowSteps()[:6]
		steps[4].values = []any{true}
		steps[5] = sqlStep{sql: "UPDATE users", err: failure}
		tx := &scriptedTransaction{t: t, steps: steps}
		reply, err := (&Store{b: scriptedBegin{tx: tx}}).ProcessLINEEvent(context.Background(), domain.LINEEvent{Type: "follow", Timestamp: 1}, domain.RegistrationToken{}, "register", "login", now)
		if !errors.Is(err, usecase.ErrUnavailable) || reply.Text != "" || tx.commits != 0 || tx.rollbacks != 1 {
			t.Fatal(reply, err, tx)
		}
	})
	t.Run("reissue counter", func(t *testing.T) {
		steps := freshFollowSteps()[:6]
		steps[5] = sqlStep{sql: "SELECT count", err: failure}
		tx := &scriptedTransaction{t: t, steps: steps}
		reply, err := (&Store{b: scriptedBegin{tx: tx}}).ProcessLINEEvent(context.Background(), domain.LINEEvent{Type: "registration", Timestamp: 1}, domain.RegistrationToken{}, "register", "login", now)
		if !errors.Is(err, usecase.ErrUnavailable) || reply.Text != "" || tx.commits != 0 || tx.rollbacks != 1 {
			t.Fatal(reply, err, tx)
		}
	})
}
func TestRegistrationAuxiliaryPersistenceFailures(t *testing.T) {
	q := &queryStub{row: rowStub{err: errors.New("database unavailable")}, execErr: errors.New("database unavailable")}
	s := &Store{q: q}
	if _, err := s.ValidateRegistration(context.Background(), "hash", time.Now()); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.SaveLINEReply(context.Background(), "event", "failed"); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredRegistrationTokens(context.Background(), time.Now()); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestMonitoringSnapshotFailuresReleaseReadTransaction(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, stage := range []string{"begin", "isolation", "settings query", "settings scan", "settings iteration", "targets query", "commit"} {
		t.Run(stage, func(t *testing.T) {
			rows := &rowsStub{}
			tx := &scriptedTransaction{t: t, steps: []sqlStep{{sql: "SET TRANSACTION"}, {sql: "FROM users", rows: rows}, {sql: "FROM assets", rows: &rowsStub{}}}}
			b := scriptedBegin{tx: tx}
			switch stage {
			case "begin":
				b.err = failure
			case "isolation":
				tx.steps[0].err = failure
			case "settings query":
				tx.steps[1].err = failure
			case "settings scan":
				rows.values = [][]any{{}}
				rows.scanErr = failure
			case "settings iteration":
				rows.err = failure
			case "targets query":
				tx.steps[2].err = failure
			case "commit":
				tx.commitErr = failure
			}
			_, err := (&Store{b: b}).MonitoringSnapshot(context.Background())
			if !errors.Is(err, usecase.ErrUnavailable) {
				t.Fatal(err)
			}
			if stage != "begin" && tx.rollbacks != 1 || stage != "begin" && stage != "commit" && tx.commits != 0 {
				t.Fatal(tx)
			}
			if stage == "settings scan" && !rows.closed {
				t.Fatal("failed scan left query open")
			}
		})
	}
}
