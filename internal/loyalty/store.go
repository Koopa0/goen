package loyalty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

const ExpiryWarningDays = 30

const MaxHistoryRows = 50

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("loyalty: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

func (s *Store) Balance(ctx context.Context, userID string) (uuid.UUID, int64, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return uuid.UUID{}, 0, ErrNoAccount
	}
	row, err := s.q.PointsBalance(ctx, uuid.NullUUID{UUID: owner, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, 0, ErrNoAccount
		}
		return uuid.UUID{}, 0, fmt.Errorf("read points balance: %w", err)
	}
	return row.AccountID, row.Points, nil
}

// Redeem lets the database own the exchange rate and allocate the spend across
// award lots under the account lock, because a comparison in Go is one two
// racers both pass.
func (s *Store) Redeem(
	ctx context.Context, userID string, points int64, operationID uuid.UUID,
) (int64, error) {
	if points < MinRedemption || points > MaxRedemptionPoints {
		return 0, ErrTooSmall
	}
	if points%PointsPerCredit != 0 {
		return 0, ErrTooSmall
	}
	owner, err := uuid.Parse(userID)
	if err != nil {
		return 0, ErrNoAccount
	}
	if operationID == uuid.Nil {
		return 0, ErrInvalidOperation
	}

	cents, err := s.q.RedeemPoints(ctx, db.RedeemPointsParams{
		UserID: owner, Points: points,
		OperationID: operationID,
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "loyalty_entries_within_balance":
				return 0, ErrNotEnough
			case "loyalty_redemption_return_unsettled":
				return 0, ErrReturnUnsettled
			}
		}
		return 0, fmt.Errorf("redeem %d points: %w", points, err)
	}
	return cents, nil
}

const historyScope = "/account/points"

// historyCursor is one ledger group's ordering values; the group is the unit,
// so a spend across several award lots cannot be split between pages. Owner
// binds the token to one account: a token minted for another is refused here as
// well as by the query's user_id predicate.
type historyCursor struct {
	At    time.Time
	ID    uuid.UUID
	Owner string
	Valid bool `json:"-"`
}

func readHistoryCursor(owner, token string) historyCursor {
	c, ok := web.ReadKeyset[historyCursor](historyScope, token)
	if !ok || c.ID == uuid.Nil || c.Owner != owner {
		return historyCursor{}
	}
	c.Valid = true
	return c
}

func (s *Store) History(ctx context.Context, userID, after string) (pages.PointsView, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return pages.PointsView{}, ErrNoAccount
	}
	id := uuid.NullUUID{UUID: owner, Valid: true}

	_, balance, err := s.Balance(ctx, userID)
	if err != nil && !errors.Is(err, ErrNoAccount) {
		return pages.PointsView{}, err
	}

	cursor := readHistoryCursor(userID, after)
	rows, err := s.q.PointsHistory(ctx, db.PointsHistoryParams{
		UserID: id, RowLimit: MaxHistoryRows + 1,
		HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
	})
	if err != nil {
		return pages.PointsView{}, fmt.Errorf("read points history: %w", err)
	}
	soon, err := s.q.PointsExpiringSoon(ctx, db.PointsExpiringSoonParams{
		UserID: id, WithinDays: ExpiryWarningDays,
	})
	if err != nil {
		return pages.PointsView{}, fmt.Errorf("read expiring points: %w", err)
	}

	standing, err := s.q.MemberStanding(ctx, db.MemberStandingParams{
		UserID: owner, WindowDays: int32(MembershipWindow / (24 * time.Hour)), Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.PointsView{}, fmt.Errorf("read points member standing: %w", err)
	}

	view := pages.PointsView{
		TierName:       standing.TierName,
		MultiplierBP:   standing.MultiplierBp,
		Balance:        balance,
		Redeemable:     Redeemable(balance),
		CreditCents:    CreditFor(Redeemable(balance)),
		ExpiringPoints: soon.Points,
		WarningDays:    ExpiryWarningDays,
		PerCredit:      PointsPerCredit,
		Minimum:        MinRedemption,
	}
	rows, more := web.PageOf(rows, MaxHistoryRows)
	if cursor.Valid {
		view.First = historyScope + "#ledger-heading"
		view.PastEnd = len(rows) == 0
	}
	if more {
		last := rows[len(rows)-1]
		position, marshalErr := json.Marshal(historyCursor{At: last.CreatedAt, ID: last.GroupID, Owner: userID})
		if marshalErr != nil {
			return pages.PointsView{}, fmt.Errorf("encode points position: %w", marshalErr)
		}
		if next, ok := web.NextKeysetURL(historyScope, string(position)); ok {
			view.Next = next + "#ledger-heading"
		}
	}

	now := time.Now()
	if soon.AnyExpiring {
		view.ExpiringOn = shoptime.DateText(ctx, shoptime.DateOf(soon.Soonest, now))
	}
	for i := range rows {
		view.Entries = append(view.Entries, pointsHistoryEntry(ctx, rows[i], now))
	}
	return view, nil
}

func pointsHistoryEntry(ctx context.Context, r db.PointsHistoryRow, now time.Time) pages.PointsEntry {
	kind := pages.PointsEntryKind(r.Kind)
	entry := pages.PointsEntry{
		Points: r.Points, Kind: kind, Reason: r.Reason, Order: r.OrderNumber,
		At: shoptime.DateText(ctx, shoptime.DateOf(r.CreatedAt, now)), Expired: r.Expired.Bool,
	}
	switch kind {
	case pages.PointsClawedBack:
		entry.RequestedPoints = r.RequestedPoints
		entry.ShortfallPoints = r.RequestedPoints + r.Points
	case pages.PointsAwarded:
		entry.ExpiresOn = shoptime.DateText(ctx, shoptime.DateOf(r.ExpiresOn, now))
	case pages.PointsSpent:
		entry.CreditCents = CreditFor(-r.Points)
	}
	return entry
}
