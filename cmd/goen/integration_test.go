//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/newsletter"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

// countingSender records what would have gone out. The whole question here is
// whether a letter is sent at all, so a sender that counts is the instrument.
type countingSender struct{ sent int }

func (s *countingSender) Send(context.Context, *email.Message) error {
	s.sent++
	return nil
}

// TestAnUnsubscribeDuringTheDrainStopsTheCopy is the lock on main's own consent
// gate. The send freezes one outbox row per subscriber and the queue drains at
// bulk priority behind every transactional message, so an unsubscribe committing
// anywhere in that window has to be read at DELIVERY. Nothing else in the tree
// exercises this handler: it is wiring, and wiring is where a rule goes to be
// deleted without a suite noticing.
func TestAnUnsubscribeDuringTheDrainStopsTheCopy(t *testing.T) {
	ctx := t.Context()
	subscribers := newsletter.NewStore(pool)
	sender := &countingSender{}
	deliver := newsletterIssueHandler(subscribers,
		email.Notifier{Sender: sender, BaseURL: "https://goen.test"})

	address := "drain-" + uuid.NewString()[:12] + "@goen.invalid"
	token := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO newsletter_subscribers (email, unsubscribe_token)
		VALUES ($1, $2)`, address, token); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	payload, err := json.Marshal(email.NewsletterIssue{
		Email: address, UnsubscribeToken: token,
		Subject: "本週選品", Body: "內容", Locale: "zh-Hant",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if deliverErr := deliver(ctx, payload); deliverErr != nil {
		t.Fatalf("deliver to somebody who wants it: %v", deliverErr)
	}
	if sender.sent != 1 {
		t.Fatalf("a subscriber on the list got %d copies, want 1 — this proves "+
			"nothing about the refusal below", sender.sent)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE newsletter_subscribers SET unsubscribed_at = now() WHERE email = $1`,
		address); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	if deliverErr := deliver(ctx, payload); deliverErr != nil {
		t.Fatalf("deliver to somebody who left: %v — leaving is not a failure, and "+
			"rescheduling would retry the one thing that must not happen", deliverErr)
	}
	if sender.sent != 1 {
		t.Errorf("%d copies sent; the second went to an address that had "+
			"unsubscribed while the queue was draining", sender.sent)
	}
}
