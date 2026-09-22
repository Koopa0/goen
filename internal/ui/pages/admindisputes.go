package pages

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// AdminDispute is one row in the payment dispute queue.
type AdminDispute struct {
	ID          string
	ProviderRef string
	OrderNumber string
	PaymentRef  string
	AmountCents int64
	Status      string
	Reason      string
	CreatedAt   string
	Overdue     bool
	Reviewed    bool
	Disposition string
	ReviewedAt  string
	EvidenceDue *time.Time
}

// AdminDisputesView is the staff dispute queue page.
type AdminDisputesView struct {
	Rows   []AdminDispute
	Notice string
}

// Empty reports whether the queue has nothing to show.
func (v AdminDisputesView) Empty() bool { return len(v.Rows) == 0 }

func (d AdminDispute) Amount() string { return twd(d.AmountCents) }

func (d AdminDispute) StripeURL() string {
	return "https://dashboard.stripe.com/disputes/" + d.ProviderRef
}

func (d AdminDispute) ReviewAction() string { return "/admin/disputes/" + d.ID + "/review" }

func (d AdminDispute) OrderLabel(ctx context.Context) string {
	if d.OrderNumber == "" {
		return i18n.T(ctx, i18n.KeyAdminDisUnattributed)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDisOrder), d.OrderNumber)
}

func (d AdminDispute) DeadlineText(ctx context.Context) string {
	if d.EvidenceDue == nil {
		return i18n.T(ctx, i18n.KeyAdminDisDeadlineUnknown)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDisDeadline), shoptime.Minute(*d.EvidenceDue))
}

func (d AdminDispute) StatusText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminDisStatus), d.Status)
}

func dispositionLabel(ctx context.Context, value string) string {
	switch value {
	case "monitoring":
		return i18n.T(ctx, i18n.KeyAdminDisDispositionMonitoring)
	case "accepted":
		return i18n.T(ctx, i18n.KeyAdminDisDispositionAccepted)
	case "challenging":
		return i18n.T(ctx, i18n.KeyAdminDisDispositionChallenging)
	case "closed":
		return i18n.T(ctx, i18n.KeyAdminDisDispositionClosed)
	default:
		return value
	}
}
