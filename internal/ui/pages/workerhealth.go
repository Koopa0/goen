package pages

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// WorkerHealthView is what the background workers have and have not done.
//
// The thresholds travel WITH the figures rather than living in the template,
// because "is this bad" is a decision the feature owns and the page renders. A
// template deciding it would be a second copy of every threshold.
type WorkerHealthView struct {
	OutboxPending int64
	// OutboxOldest is how long the most overdue message has been DUE, not how
	// old it is. A message waiting on its backoff is not late.
	OutboxOldest  time.Duration
	OutboxStuck   int64
	ExpiredHolds  int64
	CopurchaseAge time.Duration
	// CopurchaseEverBuilt distinguishes "rebuilt an hour ago" from "never
	// rebuilt", which a zero age would collapse into each other — and the
	// second is the one that means the worker has not run at all.
	CopurchaseEverBuilt bool
	// The two housekeeping sweepers. Neither changes an answer — a session past
	// its expiry is already nobody, and an unreferenced upload is invisible —
	// so these are the one place on this page where the figure is about the
	// TABLE rather than about the work being wrong. They are here because a
	// sweeper that stops is silent by construction: nothing else would ever say
	// so.
	ExpiredSessions   int64
	UnreferencedMedia int64
	// Stuck is WHICH messages have given up, not just how many. A page that
	// says "3 stuck" and cannot name them tells an operator that something is
	// wrong and nothing about what to do.
	Stuck []StuckMessage
	// OpenRefunds is money a customer is owed that has not landed. It is on
	// this page rather than a page of its own because it is the same question:
	// what has been started and not finished, that nothing will finish by
	// itself. Nothing read the refunds table at all before it — the row goen
	// commits BEFORE calling Stripe, so that a crash leaves something
	// reconciliation can find, was a row nobody could see.
	OpenRefunds []OpenRefund

	OutboxStaleAfter     time.Duration
	MaxExpiredHolds      int64
	CopurchaseStaleAfter time.Duration
	MaxExpiredSessions   int64
	MaxUnreferencedMedia int64
}

// OutboxHealthy reports whether messages are moving.
//
// A PENDING count on its own says nothing — a busy shop always has some — so
// the signal is the AGE of the oldest one. Messages arriving faster than they
// leave shows up as an oldest that keeps getting older.
func (v WorkerHealthView) OutboxHealthy() bool {
	return v.OutboxStuck == 0 &&
		(v.OutboxPending == 0 || v.OutboxOldest < v.OutboxStaleAfter)
}

// SweeperHealthy reports whether abandoned holds are being released.
func (v WorkerHealthView) SweeperHealthy() bool { return v.ExpiredHolds <= v.MaxExpiredHolds }

// RecommendHealthy reports whether the projection is being rebuilt.
func (v WorkerHealthView) RecommendHealthy() bool {
	return v.CopurchaseEverBuilt && v.CopurchaseAge < v.CopurchaseStaleAfter
}

// HousekeepingHealthy reports whether the two pruners are keeping up.
//
// A COUNT rather than an age, because neither is urgent: what matters is
// whether the number is a backlog between ticks or a worker that stopped.
func (v WorkerHealthView) HousekeepingHealthy() bool {
	return v.ExpiredSessions <= v.MaxExpiredSessions &&
		v.UnreferencedMedia <= v.MaxUnreferencedMedia
}

// HousekeepingText is the pruners' state.
func (v WorkerHealthView) HousekeepingText(ctx context.Context) string {
	if v.ExpiredSessions == 0 && v.UnreferencedMedia == 0 {
		return i18n.T(ctx, i18n.KeyHealthSweeperClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthSweeperBacklog),
		v.ExpiredSessions, v.UnreferencedMedia)
}

// RefundsHealthy reports whether every refund goen opened has landed.
//
// A COUNT and not an age, and no grace period, because nothing here settles
// itself: goen consumes no refund webhook, so a refund Stripe answered
// 'pending' stays pending in this database until a person confirms it. One
// outstanding row is therefore already a job somebody has to do.
func (v WorkerHealthView) RefundsHealthy() bool { return len(v.OpenRefunds) == 0 }

// AllHealthy reports whether everything is doing its job.
//
// Refunds are in it even though they are not a worker. The badge answers "is
// there anything nobody has been told about", and a page reading 一切正常 above
// three refunds that never left is the same false claim this section exists to
// stop.
func (v WorkerHealthView) AllHealthy() bool {
	return v.OutboxHealthy() && v.SweeperHealthy() &&
		v.RecommendHealthy() && v.HousekeepingHealthy() && v.RefundsHealthy()
}

// OutboxText is the outbox's state in a sentence a person can act on.
func (v WorkerHealthView) OutboxText(ctx context.Context) string {
	switch {
	case v.OutboxStuck > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxStuck), v.OutboxStuck)
	case v.OutboxPending == 0:
		return i18n.T(ctx, i18n.KeyHealthOutboxClear)
	case v.OutboxOldest >= v.OutboxStaleAfter:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxOverdue),
			v.OutboxPending, humanDuration(ctx, v.OutboxOldest))
	default:
		if v.OutboxOldest == 0 {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxNotYetDue), v.OutboxPending)
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthOutboxWaiting),
			v.OutboxPending, humanDuration(ctx, v.OutboxOldest))
	}
}

// SweeperText is the sweeper's state.
func (v WorkerHealthView) SweeperText(ctx context.Context) string {
	if v.ExpiredHolds == 0 {
		return i18n.T(ctx, i18n.KeyHealthHoldsClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthHoldsStuck), v.ExpiredHolds)
}

// RefundsText is the refund ledger's state.
func (v WorkerHealthView) RefundsText(ctx context.Context) string {
	if len(v.OpenRefunds) == 0 {
		return i18n.T(ctx, i18n.KeyHealthRefundsClear)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthRefundsStuck), len(v.OpenRefunds))
}

// RecommendText is the projection's state.
func (v WorkerHealthView) RecommendText(ctx context.Context) string {
	if !v.CopurchaseEverBuilt {
		return i18n.T(ctx, i18n.KeyHealthProjectionNever)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthProjectionAge),
		humanDuration(ctx, v.CopurchaseAge))
}

// humanDuration is a duration in words, rounded.
//
// Rounded because the exact seconds are never the point: "8 分鐘" and "8 分 12
// 秒" lead to the same decision, and the second reads like a number somebody
// should be watching.
func humanDuration(ctx context.Context, d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminSeconds), int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminMinutes), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminHours), int(d.Hours()))
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDays), int(d.Hours()/24))
	}
}

// StuckMessage is one delivery that has exhausted its attempts.
type StuckMessage struct {
	Topic     string
	Key       string
	Attempts  int32
	LastError string
	Since     string
}

// AttemptsText is how many times it has been tried.
func (m StuckMessage) AttemptsText() string { return strconv.FormatInt(int64(m.Attempts), 10) }

// Reason is the last error, or a stand-in — a stuck message with no recorded
// reason is still worth showing, and a blank cell reads as missing data.
func (m StuckMessage) Reason(ctx context.Context) string {
	if m.LastError == "" {
		return i18n.T(ctx, i18n.KeyHealthNoReason)
	}
	return m.LastError
}

// OpenRefund is one refund that has not landed.
//
// Every field is something a person needs in order to ACT: the order to look
// the customer up by, the amount they are owed, what the provider last said,
// and the two identifiers that find the refund at Stripe — its own reference,
// and the idempotency key goen sent, which is the handle when there is no
// reference because goen never heard an answer.
type OpenRefund struct {
	OrderNumber string
	Key         string
	Status      string
	AmountCents int64
	ProviderRef string
	Since       string
}

// Amount is what the customer is owed.
func (r OpenRefund) Amount() string { return twd(r.AmountCents) }

// StatusText says what has to happen next, not what the column holds. "pending"
// on this page means goen asked and has no answer; "failed" means Stripe said
// no and the return did not close. Those are different jobs.
//
// A status it does not know renders as ITSELF rather than panicking. The set is
// bounded by the query's own WHERE clause, so a fourth value means that query
// changed — and this is the page somebody opens to find out something is wrong,
// which a 500 from a template would tell them rather less usefully.
func (r OpenRefund) StatusText(ctx context.Context) string {
	switch r.Status {
	case "pending":
		return i18n.T(ctx, i18n.KeyHealthRefundPending)
	case "requires_action":
		return i18n.T(ctx, i18n.KeyHealthRefundAction)
	case "failed":
		return i18n.T(ctx, i18n.KeyHealthRefundFailed)
	default:
		return r.Status
	}
}

// Reference is the provider's own id, or a stand-in. It is blank exactly when
// goen never got an answer — which is the case where the request key below is
// the only way to find out what happened at Stripe.
func (r OpenRefund) Reference(ctx context.Context) string {
	if r.ProviderRef == "" {
		return i18n.T(ctx, i18n.KeyHealthNoRef)
	}
	return r.ProviderRef
}
