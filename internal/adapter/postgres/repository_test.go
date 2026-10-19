package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rowStub struct {
	values []any
	err    error
}

func (r rowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan: %d destinations, %d values", len(dest), len(r.values))
	}
	for i, v := range r.values {
		d := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			d.SetZero()
		} else {
			d.Set(reflect.ValueOf(v))
		}
	}
	return nil
}

type rowsStub struct {
	pgx.Rows
	values       [][]any
	index        int
	scanErr, err error
	closed       bool
}

func (r *rowsStub) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}
func (r *rowsStub) Scan(dest ...any) error {
	return (rowStub{r.values[r.index-1], r.scanErr}).Scan(dest...)
}
func (r *rowsStub) Err() error { return r.err }
func (r *rowsStub) Close()     { r.closed = true }

type queryStub struct {
	row               rowStub
	rows              *rowsStub
	queryErr, execErr error
	tag               pgconn.CommandTag
	args              []any
	sql               string
}

func (q *queryStub) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.sql, q.args = sql, args
	return q.rows, q.queryErr
}
func (q *queryStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql, q.args = sql, args
	return q.row
}
func (q *queryStub) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.sql, q.args = sql, args
	return q.tag, q.execErr
}

type txStub struct {
	pgx.Tx
	execs, commits, rollbacks int
	failExec                  int
	commitErr                 error
	rollbackCanceled          bool
}

func (t *txStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	t.execs++
	if t.execs == t.failExec {
		return pgconn.CommandTag{}, errors.New("secret SQL")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (t *txStub) Commit(context.Context) error { t.commits++; return t.commitErr }
func (t *txStub) Rollback(ctx context.Context) error {
	t.rollbacks++
	t.rollbackCanceled = ctx.Err() != nil
	return nil
}

type beginStub struct {
	tx  *txStub
	err error
}

func (b beginStub) Begin(context.Context) (pgx.Tx, error) { return b.tx, b.err }

func assetValues(id int64, latest string) []any {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	return []any{id, "owner", "AAPL", "Apple", "100", latest, now, true, nil, now, nil, now, now}
}

func TestDatabaseErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err, want error
		closed    bool
	}{
		{"nil", nil, nil, false}, {"closed", errors.New("closed"), usecase.ErrConnectionLost, true},
		{"missing", pgx.ErrNoRows, usecase.ErrNotFound, false}, {"wrapped missing", fmt.Errorf("wrapped: %w", pgx.ErrNoRows), usecase.ErrNotFound, false},
		{"network", &net.DNSError{}, usecase.ErrConnectionLost, false}, {"unknown", errors.New("secret"), usecase.ErrUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{isClosed: func() bool { return tc.closed }}
			if got := s.mapError(tc.err); !errors.Is(got, tc.want) {
				t.Fatal(got, tc.want)
			}
		})
	}
	for _, code := range []string{"23505", "57P01", "57P02", "57P03", "08006", "23514", "0", ""} {
		want := usecase.ErrUnavailable
		switch code {
		case "23505":
			want = usecase.ErrAlreadyExists
		case "57P01", "57P02", "57P03", "08006":
			want = usecase.ErrConnectionLost
		}
		if got := (&Store{}).mapError(&pgconn.PgError{Code: code}); !errors.Is(got, want) {
			t.Fatal(code, got)
		}
	}
	(&Store{}).Close()
}

func TestOpeningInvalidOrUnavailableDatabase(t *testing.T) {
	if _, err := OpenPool(context.Background(), "postgres://%zz"); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := OpenConnection(context.Background(), "postgres://%zz"); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenPool(ctx, "postgres://stock:stock@127.0.0.1:5432/stock_test?sslmode=disable"); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := OpenConnection(ctx, "postgres://stock:stock@127.0.0.1:5432/stock_test?sslmode=disable"); !errors.Is(err, usecase.ErrConnectionLost) {
		t.Fatal(err)
	}
}

func TestAssetScanningFailures(t *testing.T) {
	for _, index := range []int{4, 5} {
		values := assetValues(1, "90")
		values[index] = "invalid"
		if _, err := scanAsset(rowStub{values: values}); !errors.Is(err, domain.ErrInvalidPrice) {
			t.Fatal(err)
		}
	}
	values := assetValues(1, "90")
	code := "PRICE_FETCH_FAILED"
	values[10] = &code
	got, err := scanAsset(rowStub{values: values})
	if err != nil || got.LastErrorCode == nil || *got.LastErrorCode != domain.PriceFetchFailed {
		t.Fatal(got, err)
	}
}

func TestListAndTargetsFailures(t *testing.T) {
	for _, method := range []string{"list", "targets"} {
		for _, stage := range []string{"query", "scan", "iteration"} {
			t.Run(method+"/"+stage, func(t *testing.T) {
				rows := &rowsStub{}
				q := &queryStub{rows: rows}
				failure := errors.New("secret database error")
				switch stage {
				case "query":
					q.queryErr = failure
				case "scan":
					rows.values = [][]any{{}}
					rows.scanErr = failure
				case "iteration":
					rows.err = failure
				}
				s := &Store{q: q}
				var err error
				if method == "list" {
					_, err = s.List(context.Background(), usecase.ListOptions{Sort: "code", Order: "asc"})
				} else {
					_, err = s.Targets(context.Background())
				}
				if !errors.Is(err, usecase.ErrUnavailable) || stage != "query" && !rows.closed {
					t.Fatal(err, rows.closed)
				}
			})
		}
	}
	for _, o := range []usecase.ListOptions{{Sort: "bad", Order: "asc"}, {Sort: "code", Order: "bad"}} {
		if _, err := (&Store{}).List(context.Background(), o); err == nil {
			t.Fatal("invalid list accepted")
		}
	}
}

func TestDeclineSortingTiesAndDirections(t *testing.T) {
	for _, order := range []string{"asc", "desc"} {
		t.Run(order, func(t *testing.T) {
			rows := &rowsStub{values: [][]any{assetValues(2, "90"), assetValues(3, "80"), assetValues(1, "90")}}
			s := &Store{q: &queryStub{rows: rows}}
			got, err := s.List(context.Background(), usecase.ListOptions{Sort: "decline_rate", Order: order})
			want := []int64{1, 2, 3}
			if order == "desc" {
				want = []int64{3, 1, 2}
			}
			var ids []int64
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			if err != nil || !reflect.DeepEqual(ids, want) || !rows.closed {
				t.Fatal(ids, err)
			}
		})
	}
	rows := &rowsStub{values: [][]any{{int64(42), "AAPL", "owner"}}}
	got, err := (&Store{q: &queryStub{rows: rows}}).Targets(context.Background())
	if err != nil || !reflect.DeepEqual(got, []usecase.Target{{ID: 42, Code: "AAPL", LoginID: "owner"}}) {
		t.Fatal(got, err)
	}
}

func TestRepositoryMutationFailures(t *testing.T) {
	q := &queryStub{execErr: errors.New("secret"), row: rowStub{err: errors.New("secret")}}
	s := &Store{q: q}
	ctx := context.Background()
	if err := s.Delete(ctx, 1); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	q.execErr = nil
	q.tag = pgconn.NewCommandTag("DELETE 0")
	if err := s.Delete(ctx, 1); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal(err)
	}
	q.tag = pgconn.NewCommandTag("DELETE 1")
	if err := s.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	q.row.err = pgx.ErrNoRows
	if _, err := s.SaveSettings(ctx, domain.NotificationSettings{Thresholds: []int{10}}); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	q.execErr = errors.New("failure")
	if err := s.DeleteSession(ctx, "hashed"); !errors.Is(err, usecase.ErrUnavailable) || !reflect.DeepEqual(q.args, []any{"hashed"}) {
		t.Fatal(err, q.args)
	}
	now := time.Now()
	if err := s.DeleteExpiredSessions(ctx, now); !errors.Is(err, usecase.ErrUnavailable) || !reflect.DeepEqual(q.args, []any{now}) {
		t.Fatal(err, q.args)
	}
}

func TestAlreadyOrderedDeclineTies(t *testing.T) {
	for _, order := range []string{"asc", "desc"} {
		t.Run(order, func(t *testing.T) {
			rows := &rowsStub{values: [][]any{assetValues(1, "90"), assetValues(2, "90")}}
			got, err := (&Store{q: &queryStub{rows: rows}}).List(context.Background(), usecase.ListOptions{Sort: "decline_rate", Order: order})
			if err != nil || len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestTransactionFailureAndRollback(t *testing.T) {
	for _, stage := range []string{"begin", "callback", "commit", "success"} {
		t.Run(stage, func(t *testing.T) {
			tx := &txStub{}
			b := beginStub{tx: tx}
			callbackErr := errors.New("callback failure")
			want := error(nil)
			if stage == "begin" {
				b.err = errors.New("begin failure")
				want = usecase.ErrUnavailable
			}
			if stage == "callback" {
				want = callbackErr
			}
			if stage == "commit" {
				tx.commitErr = errors.New("commit failure")
				want = usecase.ErrUnavailable
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			err := (&Store{b: b}).WithinTransaction(ctx, func(repo usecase.AssetRepository) error {
				called = true
				if stage == "callback" {
					cancel()
					return callbackErr
				}
				return nil
			})
			wantRollbacks := 1
			if stage == "begin" {
				wantRollbacks = 0
			}
			if !errors.Is(err, want) || called != (stage != "begin") || tx.rollbacks != wantRollbacks || tx.rollbackCanceled {
				t.Fatal(err, called, tx)
			}
			wantCommits := 0
			if stage == "commit" || stage == "success" {
				wantCommits = 1
			}
			if tx.commits != wantCommits {
				t.Fatal(tx.commits)
			}
		})
	}
}

func TestSessionReplacementIsAtomic(t *testing.T) {
	for _, stage := range []string{"begin", "delete", "insert", "commit", "new", "replace"} {
		t.Run(stage, func(t *testing.T) {
			tx := &txStub{}
			b := beginStub{tx: tx}
			old := "old-hash"
			want := usecase.ErrUnavailable
			switch stage {
			case "begin":
				b.err = errors.New("begin failure")
			case "delete":
				tx.failExec = 1
			case "insert":
				tx.failExec = 2
			case "commit":
				tx.commitErr = errors.New("commit failure")
			case "new":
				old = ""
				want = nil
			case "replace":
				want = nil
			}
			err := (&Store{b: b}).ReplaceSession(context.Background(), old, domain.Session{})
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if stage == "begin" {
				if tx.execs != 0 || tx.rollbacks != 0 {
					t.Fatal(tx)
				}
				return
			}
			if tx.rollbacks != 1 || stage == "delete" && tx.execs != 1 || stage == "insert" && tx.execs != 2 || stage == "new" && tx.execs != 1 || stage == "replace" && tx.execs != 2 {
				t.Fatal(tx)
			}
			wantCommits := 1
			if stage == "delete" || stage == "insert" {
				wantCommits = 0
			}
			if tx.commits != wantCommits {
				t.Fatal(tx)
			}
		})
	}
}
