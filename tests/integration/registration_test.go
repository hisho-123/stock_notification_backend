//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/adapter/postgres"
	"github.com/hisho-123/stock_notification_backend/internal/adapter/security"
	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/testfixture"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

func lineID(n int) string { return fmt.Sprintf("U%032x", n) }
func event(t *testing.T, s *postgres.Store, line, id, kind string, stamp int64, now time.Time) (string, domain.LINEReply) {
	t.Helper()
	tokens := security.Tokens{}
	plain, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token := domain.RegistrationToken{TokenHash: tokens.Hash(plain), LINEUserID: line, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	reply, err := s.ProcessLINEEvent(context.Background(), domain.LINEEvent{ID: id, LINEUserID: line, Type: kind, Timestamp: stamp, ReplyToken: "reply"}, token, "https://stock.example.com/register#token="+plain, "https://stock.example.com/login", now)
	if err != nil {
		t.Fatal(err)
	}
	return plain, reply
}
func validToken(t *testing.T, s *postgres.Store, plain string, now time.Time, want bool) {
	t.Helper()
	_, err := s.ValidateRegistration(context.Background(), security.Tokens{}.Hash(plain), now)
	if want && err != nil || !want && !errors.Is(err, usecase.ErrRegistrationUnavailable) {
		t.Fatal("token validity", want, err)
	}
}
func sessionFor(t *testing.T, id string, now time.Time) domain.Session {
	t.Helper()
	tokens := security.Tokens{}
	a, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return domain.Session{LoginID: id, TokenHash: tokens.Hash(a), CSRFTokenHash: tokens.Hash(b), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
}
func registerToken(t *testing.T, s *postgres.Store, plain, id, oldHash string, now time.Time) error {
	t.Helper()
	return s.Register(context.Background(), security.Tokens{}.Hash(plain), domain.User{LoginID: id, PasswordHash: testfixture.PasswordHash}, sessionFor(t, id, now), oldHash, now)
}
func TestWebhookOrderingDuplicateAndReissueLimit(t *testing.T) {
	s, _, _ := database(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	line := lineID(2)
	first, _ := event(t, s, line, "follow-100", "follow", 100, now)
	validToken(t, s, first, now, true)
	next, _ := event(t, s, line, "message-300", "registration", 300, now)
	validToken(t, s, first, now, false)
	validToken(t, s, next, now, true)
	event(t, s, line, "unfollow-200", "unfollow", 200, now)
	validToken(t, s, next, now, false)
	_, blockedReply := event(t, s, line, "blocked-message-350", "registration", 350, now)
	if blockedReply.Text != "" {
		t.Fatal("blocked user received URL", blockedReply)
	}
	_, reply := event(t, s, line, "follow-250", "follow", 250, now)
	if reply.Text != "" {
		t.Fatal("old request reissued a URL")
	}
	third, _ := event(t, s, line, "message-400", "registration", 400, now)
	validToken(t, s, third, now, true)
	event(t, s, line, "unfollow-400", "unfollow", 400, now)
	validToken(t, s, third, now, false)
	_, reply = event(t, s, line, "follow-400", "follow", 400, now)
	if reply.Text != "" {
		t.Fatal("follow overrode same-time block")
	}
	last, _ := event(t, s, line, "follow-450", "follow", 450, now)
	validToken(t, s, last, now, true)
	_, reply = event(t, s, line, "follow-450", "follow", 450, now)
	if reply.Text != "" {
		t.Fatal("duplicate replied")
	}
	validToken(t, s, last, now, true)
	// 履歴が新しいメッセージであっても、follow/unfollowだけの順序で友だち状態を更新する。
	event(t, s, lineID(1), "owner-message-600", "registration", 600, now)
	event(t, s, lineID(1), "owner-unfollow-500", "unfollow", 500, now)
	user, err := s.FindUser(context.Background(), "owner")
	if err != nil || user.LINEFollowing {
		t.Fatal(user, err)
	}
	_, reply = event(t, s, lineID(1), "owner-follow-550", "follow", 550, now)
	user, err = s.FindUser(context.Background(), "owner")
	if err != nil || !user.LINEFollowing || reply.Text != "" {
		t.Fatal(user, reply, err)
	}
	for i := 0; i < 5; i++ {
		last, reply = event(t, s, lineID(3), fmt.Sprint("limit-", i), "registration", int64(i+1), now)
		if !strings.Contains(reply.Text, "/register#token=") {
			t.Fatal(reply)
		}
	}
	validToken(t, s, last, now, true)
	_, reply = event(t, s, lineID(3), "limit-six", "registration", 6, now)
	if !strings.Contains(reply.Text, "待って") {
		t.Fatal(reply)
	}
	validToken(t, s, last, now, true)
	_, reply = event(t, s, lineID(3), "limit-window", "registration", 7, now.Add(15*time.Minute))
	if !strings.Contains(reply.Text, "/register#token=") {
		t.Fatal(reply)
	}
}
func TestRegistrationAtomicCompetitionAndRollback(t *testing.T) {
	s, raw, _ := database(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	token, _ := event(t, s, lineID(4), "same-token", "follow", 1, now)
	validToken(t, s, token, now.Add(24*time.Hour-time.Nanosecond), true)
	validToken(t, s, token, now.Add(24*time.Hour), false)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"concurrent-a", "concurrent-b"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); results <- registerToken(t, s, token, id, "", now) }(id)
	}
	wg.Wait()
	close(results)
	success, unavailable := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, usecase.ErrRegistrationUnavailable) {
			unavailable++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || unavailable != 1 {
		t.Fatal(success, unavailable)
	}
	var count int
	if err := raw.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE line_user_id=$1`, lineID(4)).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	one, _ := event(t, s, lineID(5), "id-one", "follow", 1, now)
	two, _ := event(t, s, lineID(6), "id-two", "follow", 1, now)
	results = make(chan error, 2)
	for _, plain := range []string{one, two} {
		wg.Add(1)
		go func(plain string) { defer wg.Done(); results <- registerToken(t, s, plain, "shared-id", "", now) }(plain)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, usecase.ErrLoginIDExists) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	// 中間の設定保存を故障させ、ユーザー・旧セッション・トークンをまとめてロールバックする。
	old := sessionFor(t, "owner", now)
	other := sessionFor(t, "owner", now)
	if err := s.ReplaceSession(context.Background(), "", old); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSession(context.Background(), "", other); err != nil {
		t.Fatal(err)
	}
	rollbackToken, _ := event(t, s, lineID(7), "rollback", "follow", 1, now)
	if _, err := raw.Exec(context.Background(), `CREATE FUNCTION reject_registration() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.login_id='rollback' THEN RAISE EXCEPTION 'forced'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_registration BEFORE INSERT ON notification_settings FOR EACH ROW EXECUTE FUNCTION reject_registration()`); err != nil {
		t.Fatal(err)
	}
	if err := registerToken(t, s, rollbackToken, "rollback", old.TokenHash, now); !errors.Is(err, usecase.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := s.FindUser(context.Background(), "rollback"); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal(err)
	}
	validToken(t, s, rollbackToken, now, true)
	if _, err := s.FindSession(context.Background(), old.TokenHash); err != nil {
		t.Fatal("old session revoked after rollback", err)
	}
	if _, err := raw.Exec(context.Background(), `DROP TRIGGER reject_registration ON notification_settings`); err != nil {
		t.Fatal(err)
	}
	if err := registerToken(t, s, rollbackToken, "rollback", old.TokenHash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindSession(context.Background(), old.TokenHash); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal("old session not replaced", err)
	}
	if _, err := s.FindSession(context.Background(), other.TokenHash); err != nil {
		t.Fatal("other browser revoked", err)
	}
	validToken(t, s, rollbackToken, now, false)
}
func TestUserIsolationBlockedMonitoringAndMissingSettings(t *testing.T) {
	s, raw, _ := database(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, n := range []int{2, 3} {
		plain, _ := event(t, s, lineID(n), fmt.Sprint("user-", n), "follow", 1, now)
		if err := registerToken(t, s, plain, fmt.Sprint("user-", n), "", now); err != nil {
			t.Fatal(err)
		}
	}
	owner := ownerContext()
	second := usecase.WithLoginID(context.Background(), "user-2")
	third := usecase.WithLoginID(context.Background(), "user-3")
	a, err := s.Create(owner, asset(t, "AAPL", "100"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(second, asset(t, "AAPL", "100"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(third, asset(t, "AAPL", "100")); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(second, usecase.ListOptions{Sort: "code", Order: "asc"})
	if err != nil || len(rows) != 1 || rows[0].ID != b.ID {
		t.Fatal(rows, err)
	}
	if _, err = s.SetMonitoring(owner, b.ID, false, now); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.Delete(owner, b.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.SaveSettings(second, domain.NotificationSettings{Thresholds: []int{30}, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(context.Background(), `UPDATE users SET line_following=false WHERE login_id='owner'; DELETE FROM notification_settings WHERE login_id='user-3'`); err != nil {
		t.Fatal(err)
	}
	notifier := &recipientNotifier{}
	provider := &monitorPrices{quote: domain.Quote{Price: price(t, "75"), ProvidedAt: now, FetchedAt: now}}
	m := usecase.Monitor{Assets: s, Snapshots: s, Users: s, Sessions: s, Registrations: s, Transaction: s, Prices: provider, Notifier: notifier, Clock: fixedClock{now}}
	var observations []usecase.Observation
	result, err := m.Run(context.Background(), func(o usecase.Observation) { observations = append(observations, o) })
	if err != nil || result.Failed != 1 || result.Skipped != 1 || len(notifier.to) != 0 || len(observations) == 0 || observations[0].ErrorCode != "SETTINGS_MISSING" {
		t.Fatal(result, notifier.to, err)
	}
	saved, err := s.Get(owner, a.ID)
	if err != nil || saved.LatestValue.String() != "75" || saved.LastNotifiedThreshold != nil || saved.LastErrorCode != nil {
		t.Fatal(saved, err)
	}
	provider.quote.ProvidedAt = now.Add(-240 * time.Hour)
	result, err = m.Run(context.Background(), nil)
	if err != nil || result.Stale != 2 || len(notifier.to) != 1 || notifier.to[0] != lineID(2) {
		t.Fatal(result, notifier.to, err)
	}
	saved, err = s.Get(owner, a.ID)
	if err != nil || !saved.MonitoringEnabled || saved.LatestValue.String() != "75" || saved.LastErrorCode == nil || *saved.LastErrorCode != domain.PriceDataStale {
		t.Fatal(saved, err)
	}
	saved, err = s.Get(second, b.ID)
	if err != nil || saved.MonitoringEnabled {
		t.Fatal(saved, err)
	}
}

type recipientNotifier struct{ to []string }

func (n *recipientNotifier) Send(_ context.Context, to, text string) error {
	n.to = append(n.to, to)
	return nil
}

type monitorPrices struct{ quote domain.Quote }

func (p *monitorPrices) Fetch(context.Context, string) (domain.Quote, error) { return p.quote, nil }
