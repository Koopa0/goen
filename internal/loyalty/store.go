package loyalty

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ExpiryWarningDays is how far ahead the page warns.
const ExpiryWarningDays = 30

// MaxHistoryRows bounds the ledger a customer sees.
const MaxHistoryRows = 50

// Store is the database side of the points programme.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store over pool.
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

// Redeem turns points into store credit. The cents are derived here and never
// taken from a form; the overdraw check is loyalty_never_negative's, under a
// lock on the account, because a comparison in Go is one two racers both pass.
func (s *Store) Redeem(ctx context.Context, userID string, points int64) (int64, error) {
	accountID, balance, err := s.Balance(ctx, userID)
	if err != nil {
		return 0, err
	}
	if points < MinRedemption {
		return 0, ErrTooSmall
	}
	if points%PointsPerCredit != 0 {
		return 0, ErrTooSmall
	}
	if points > balance {
		return 0, ErrNotEnough
	}

	cents, err := s.q.RedeemPoints(ctx, db.RedeemPointsParams{
		AccountID: accountID, Points: points, Cents: CreditFor(points),
		Key: "points:" + uuid.NewString(),
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrNotEnough, err.Error())
	}
	return cents, nil
}

// History is the ledger a customer sees, and what is about to expire.
func (s *Store) History(ctx context.Context, userID string) (pages.PointsView, error) {
	owner, err := uuid.Parse(userID)
	if err != nil {
		return pages.PointsView{}, ErrNoAccount
	}
	id := uuid.NullUUID{UUID: owner, Valid: true}

	_, balance, err := s.Balance(ctx, userID)
	if err != nil && !errors.Is(err, ErrNoAccount) {
		return pages.PointsView{}, err
	}

	rows, err := s.q.PointsHistory(ctx, db.PointsHistoryParams{
		UserID: id, Limit: MaxHistoryRows,
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

	view := pages.PointsView{
		Balance:        balance,
		Redeemable:     Redeemable(balance),
		CreditCents:    CreditFor(Redeemable(balance)),
		ExpiringPoints: soon.Points,
		WarningDays:    ExpiryWarningDays,
		PerCredit:      PointsPerCredit,
		Minimum:        MinRedemption,
	}
	if soon.AnyExpiring {
		view.ExpiringOn = soon.Soonest.Format("2006-01-02")
	}
	for i := range rows {
		r := &rows[i]
		entry := pages.PointsEntry{
			Points: r.Points, Reason: r.Reason, Order: r.OrderNumber,
			At: r.CreatedAt.Format("2006-01-02"), Expired: r.Expired.Bool,
		}
		if r.ExpiresOn.Valid {
			entry.ExpiresOn = r.ExpiresOn.Time.Format("2006-01-02")
		}
		view.Entries = append(view.Entries, entry)
	}
	return view, nil
}
