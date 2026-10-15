package refunds

import (
	"errors"
	"log/slog"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/refund"
)

// unsentNotice is the outcome that selects the order page notice for what
// RefundUnsent or UnsentRefundPreview returned, and false for an error no
// notice describes. As for the refund before shipment, recovery comes before
// the refusals: a payout can keep a database refusal as its cause while the
// approved refund stays open for Resume.
func unsentNotice(err error) (refundOutcome, bool) {
	switch {
	case err == nil:
		return refundOutcome{Query: "?unsentrefunded=1", Level: slog.LevelInfo}, true
	case errors.Is(err, ErrUnsentAllowance):
		return refundOutcome{Query: "?unsentallowance=1", Level: slog.LevelError}, true
	case errors.Is(err, ErrUnsettled),
		db.IsConstraint(err, "return_unsent_payout_outstanding"):
		return refundOutcome{Query: "?refundpending=1", Level: slog.LevelWarn}, true
	case db.IsConstraint(err, "return_unsent_hold_short"):
		return refundOutcome{Query: "?unsenthold=1", Level: slog.LevelError}, true
	case errors.Is(err, refund.ErrIncomplete):
		return refundOutcome{Query: "?refundretry=1", Level: slog.LevelError}, true
	case errors.Is(err, ErrUnsentDiffers):
		return refundOutcome{Query: "?unsentdiffers=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrUnsentReason):
		return refundOutcome{Query: "?unsentreason=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrUnsentUnits),
		db.IsConstraint(err, "return_within_shipment"),
		db.IsConstraint(err, "return_unsent_lines"):
		return refundOutcome{Query: "?unsentbounds=1", Level: slog.LevelWarn}, true
	case db.IsConstraint(err, "return_unsent_undecided_return"):
		return refundOutcome{Query: "?unsentundecided=1", Level: slog.LevelWarn}, true
	case db.IsConstraint(err, "return_unsent_eligible"):
		return refundOutcome{Query: "?unsentineligible=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrPayoutUnfit),
		db.IsConstraint(err, "refunds_sources_cover_return"),
		db.IsConstraint(err, "refunds_credit_attribution"),
		db.IsConstraint(err, "refunds_card_amount_positive"),
		db.IsConstraint(err, "refunds_return_captured"):
		return refundOutcome{Query: "?refundmismatch=1", Level: slog.LevelError}, true
	case errors.Is(err, ErrInvalid):
		return refundOutcome{Query: "?refundreason=1", Level: slog.LevelWarn}, true
	case errors.Is(err, refund.ErrRefused):
		return refundOutcome{Query: "?refundunsure=1", Level: slog.LevelError}, true
	}
	return refundOutcome{}, false
}
