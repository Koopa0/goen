package account

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/web/formtext"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("account: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

func (s *Store) Overview(ctx context.Context, u user.User, after string) (View, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return View{}, fmt.Errorf("parse user id: %w", err)
	}

	view := View{Email: u.Email, Name: u.Name}

	profile, err := s.q.UserByID(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("read profile: %w", err)
	}
	view.Phone = profile.Phone.String
	view.HasPassword = profile.HasPassword

	identities, err := s.q.IdentitiesForUser(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("read linked identities: %w", err)
	}
	view.GoogleLinked = len(identities) > 0
	view.CanUnlinkGoogle = view.GoogleLinked && profile.HasPassword

	cursor := readOrderCursor(u.ID, after)
	orders, err := s.q.UserOrders(ctx, db.UserOrdersParams{
		UserID: uuid.NullUUID{UUID: id, Valid: true}, RowLimit: orderPageSize + 1,
		HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
	})
	if err != nil {
		return View{}, fmt.Errorf("read orders: %w", err)
	}
	orders, view.OrdersBound = orderBound(cursor, u.ID, orders,
		func(o *db.UserOrdersRow) (uuid.UUID, time.Time) { return o.ID, o.PlacedAt })

	orderIDs := make([]uuid.UUID, len(orders))
	for i := range orders {
		orderIDs[i] = orders[i].ID
	}
	returned, err := s.q.ReturnedOrders(ctx, orderIDs)
	if err != nil {
		return View{}, fmt.Errorf("read returned orders: %w", err)
	}

	now := time.Now()
	for i := range orders {
		o := &orders[i]
		view.Orders = append(view.Orders, Order{
			Number:     o.OrderNumber,
			Status:     order.FulfillmentStatus(o.FulfillmentStatus),
			PlacedAt:   shoptime.DateOf(o.PlacedAt, now),
			TotalCents: o.TotalCents,
			LineCount:  o.LineCount,
			Committed:  o.Committed,
			OwedCents:  o.OwedCents,
			Returned:   slices.Contains(returned, o.ID),
			OneLastDay: o.OneLastDay,
			LastDay:    shoptime.DateOf(o.RescissionEnds, now),
		})
	}

	standing, err := s.q.MemberStanding(ctx, db.MemberStandingParams{
		Locale: string(i18n.FromContext(ctx)),
		UserID: id,
	})
	if err != nil {
		return View{}, fmt.Errorf("read member standing: %w", err)
	}
	view.Standing = loyalty.MemberStanding{
		SpendCents: standing.SpendCents, TierName: standing.TierName,
		MultiplierBP: standing.MultiplierBp,
		NextName:     standing.NextName, NextNeedsCents: standing.NextNeedsCents,
	}

	balance, err := s.q.StoreCreditBalance(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return View{}, fmt.Errorf("read store credit: %w", err)
	}
	view.CreditCents = balance

	addrs, err := s.q.AddressesForUser(ctx, id)
	if err != nil {
		return View{}, fmt.Errorf("read addresses: %w", err)
	}
	for i := range addrs {
		a := &addrs[i]
		view.Addresses = append(view.Addresses, AddressView{
			ID: a.ID.String(), Label: a.Label.String, Name: a.RecipientName, Phone: a.Phone,
			PostalCode: a.PostalCode, City: a.City, District: a.District,
			Street: a.Street, Default: a.IsDefault,
		})
	}
	return view, nil
}

type Verification struct {
	Verified     bool
	PendingEmail string
}

func (s *Store) EmailVerification(ctx context.Context, userID string) (Verification, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Verification{}, fmt.Errorf("parse user id: %w", err)
	}
	row, err := s.q.EmailVerification(ctx, id)
	if err != nil {
		return Verification{}, fmt.Errorf("read verification state: %w", err)
	}
	return Verification{Verified: row.Verified, PendingEmail: row.PendingEmail}, nil
}

func (s *Store) UpdateProfile(ctx context.Context, userID, name, phone string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	name, phone = normalizeProfile(name, phone)
	if len(profileRefusals(name, phone)) > 0 {
		return ErrInvalidInput
	}
	if err := s.q.UpdateProfile(ctx, db.UpdateProfileParams{
		ID: id, FullName: text(name), Phone: text(phone),
	}); err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

// OpenReturnOrder is the number of the account's one order with a return still
// being processed, or "" when it has none or several. erase_user alone decides
// that a return stands in the way, by an exposure the store role cannot read,
// so a return is named only when it is the one candidate.
func (s *Store) OpenReturnOrder(ctx context.Context, userID string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	numbers, err := s.q.OpenReturnOrders(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return "", fmt.Errorf("read open returns: %w", err)
	}
	if len(numbers) != 1 {
		return "", nil
	}
	return numbers[0], nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// AddAddress locks the account first when the new address is the default:
// under READ COMMITTED a clear cannot see a default another transaction is
// setting. A non-default insert touches no default, so it takes no lock.
func (s *Store) AddAddress(ctx context.Context, userID string, a *Address) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if a == nil {
		return ErrInvalidInput
	}
	bounded := *a
	bounded.Trim()
	if len(bounded.Validate()) > 0 {
		return ErrInvalidInput
	}
	a = &bounded

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin add address: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	if a.Default {
		if _, lockErr := q.LockUser(ctx, id); lockErr != nil {
			return fmt.Errorf("lock account for add address: %w", lockErr)
		}
		if clearErr := q.ClearDefaultAddress(ctx, id); clearErr != nil {
			return fmt.Errorf("clear default address: %w", clearErr)
		}
	}
	if err := q.CreateAddress(ctx, db.CreateAddressParams{
		UserID: id, Label: text(a.Label), RecipientName: a.Name, Phone: a.Phone,
		PostalCode: a.PostalCode, City: a.City, District: a.District,
		Street: a.Street, IsDefault: a.Default,
	}); err != nil {
		return fmt.Errorf("create address: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit add address: %w", err)
	}
	return nil
}

func (s *Store) addressForUser(ctx context.Context, userID, addressID string) (AddressView, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return AddressView{}, err
	}
	aid, err := uuid.Parse(addressID)
	if err != nil {
		return AddressView{}, ErrNotFound
	}
	row, err := s.q.AddressForUser(ctx, db.AddressForUserParams{UserID: uid, AddressID: aid})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AddressView{}, ErrNotFound
		}
		return AddressView{}, err
	}
	return AddressView{
		ID: row.ID.String(), Label: row.Label.String, Name: row.RecipientName,
		Phone: row.Phone, PostalCode: row.PostalCode, City: row.City,
		District: row.District, Street: row.Street, Default: row.IsDefault,
	}, nil
}

func (s *Store) updateAddress(ctx context.Context, userID, addressID string, a *Address) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return err
	}
	aid, err := uuid.Parse(addressID)
	if err != nil {
		return ErrNotFound
	}
	if a == nil {
		return ErrInvalidInput
	}
	bounded := *a
	bounded.Trim()
	if len(bounded.Validate()) > 0 {
		return ErrInvalidInput
	}
	n, err := s.q.UpdateAddress(ctx, db.UpdateAddressParams{
		UserID: uid, AddressID: aid, Label: text(bounded.Label),
		RecipientName: bounded.Name, Phone: bounded.Phone, PostalCode: bounded.PostalCode,
		City: bounded.City, District: bounded.District, Street: bounded.Street,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MakeDefaultAddress(ctx context.Context, userID, addressID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	aid, err := uuid.Parse(addressID)
	if err != nil {
		return ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin set default address: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	if _, lockErr := q.LockUser(ctx, uid); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock account for set default address: %w", lockErr)
	}

	if clearErr := q.ClearDefaultAddress(ctx, uid); clearErr != nil {
		return fmt.Errorf("clear default address: %w", clearErr)
	}
	n, err := q.SetDefaultAddress(ctx, db.SetDefaultAddressParams{UserID: uid, ID: aid})
	if err != nil {
		return fmt.Errorf("set default address: %w", err)
	}
	if n == 0 {
		// Returning before the commit is what puts the old default back.
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit set default address: %w", err)
	}
	return nil
}

func (s *Store) DeleteAddress(ctx context.Context, userID, addressID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	aid, err := uuid.Parse(addressID)
	if err != nil {
		return ErrNotFound
	}
	if err := s.q.DeleteAddress(ctx, db.DeleteAddressParams{UserID: uid, ID: aid}); err != nil {
		return fmt.Errorf("delete address: %w", err)
	}
	return nil
}

type Address struct {
	Label      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string
	Default    bool
}

// Validate holds the address to the checkout's own rule for who a parcel goes to
// and where, so an address the book keeps cannot fail at checkout, and then
// checks the label, which only the book has.
func (a *Address) Validate() []formtext.FieldRefusal {
	d := a.details()
	errs := d.ValidateRecipient()
	if utf8.RuneCountInString(a.Label) > maxAddressLabelRunes {
		errs = append(errs, formtext.FieldRefusal{Field: "label", MessageKey: i18n.KeyAddressIncomplete})
	}
	if formtext.HasControlChars(a.Label) {
		errs = append(errs, formtext.FieldRefusal{Field: "label", MessageKey: i18n.KeyFieldHasControlChars})
	}
	return errs
}

// Trim normalises the address the way the checkout normalises what it is sent.
func (a *Address) Trim() {
	d := a.details()
	d.Trim()
	a.Label = strings.TrimSpace(a.Label)
	a.Name, a.Phone, a.PostalCode = d.RecipientName, d.Phone, d.PostalCode
	a.City, a.District, a.Street = d.City, d.District, d.Street
}

// details is the address as the checkout receives it from the book: a parcel
// to a street address.
func (a *Address) details() delivery.Details {
	return delivery.Details{
		To: shipping.DestinationAddress, RecipientName: a.Name, Phone: a.Phone,
		PostalCode: a.PostalCode, City: a.City, District: a.District, Street: a.Street,
	}
}
