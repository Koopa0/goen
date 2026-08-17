package pages

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// WorkerHealthView is what the background workers have and have not done.
type WorkerHealthView struct {
	OutboxPending int64
	// OutboxOldest is how long the most overdue message has been due, not old.
	OutboxOldest        time.Duration
	OutboxStuck         int64
	ExpiredHolds        int64
	CopurchaseAge       time.Duration
	CopurchaseEverBuilt bool

	ExpiredSessions   int64
	UnreferencedMedia int64
	Stuck             []StuckMessage
	OpenRefunds       []OpenRefund

	OutboxStaleAfter     time.Duration
	MaxExpiredHolds      int64
	CopurchaseStaleAfter time.Duration
	MaxExpiredSessions   int64
	MaxUnreferencedMedia int64
}

// OutboxHealthy reports whether messages are moving; the signal is age.
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
func (v WorkerHealthView) RefundsHealthy() bool { return len(v.OpenRefunds) == 0 }

// AllHealthy reports whether everything is doing its job.
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

// Reason is the last error, or a stand-in when none was recorded.
func (m StuckMessage) Reason(ctx context.Context) string {
	if m.LastError == "" {
		return i18n.T(ctx, i18n.KeyHealthNoReason)
	}
	return m.LastError
}

// OpenRefund is one refund that has not landed.
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

// StatusText says what has to happen next; an unknown status renders as itself.
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

// Reference is the provider's own id, blank when goen never got an answer.
func (r OpenRefund) Reference(ctx context.Context) string {
	if r.ProviderRef == "" {
		return i18n.T(ctx, i18n.KeyHealthNoRef)
	}
	return r.ProviderRef
}
