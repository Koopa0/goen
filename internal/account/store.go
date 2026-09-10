package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"

	"github.com/koopa0/goen/internal/i18n"
)

// Store reads and writes accounts, sessions and the customer's own pages.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("account: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// Register creates an account.
func (s *Store) Register(ctx context.Context, c *Credentials) (User, error) {
	hash, err := HashPassword(c.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	row, err := s.q.CreateUser(ctx, db.CreateUserParams{
		Email:        c.Email,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		FullName:     text(c.Name),
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: row.Role}, nil
}

// Authenticate checks an email and password.
func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	// Refuse this before reading the account, or the outcomes are distinguishable:
	// burnHashTime returns immediately at this length while VerifyPassword does
	// not. Every password_hash writer in account goes through HashPassword, which
	// refuses to produce a hash for an input over this same bound.
	if len(password) > MaxPasswordBytes {
		return User{}, ErrBadCredentials
	}

	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnHashTime(password)
			return User{}, ErrBadCredentials
		}
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if !row.PasswordHash.Valid {
		burnHashTime(password)
		return User{}, ErrBadCredentials
	}

	if !VerifyPassword(row.PasswordHash.String, password) {
		return User{}, ErrBadCredentials
	}
	if err := s.q.TouchLastLogin(ctx, row.ID); err != nil {
		return User{}, fmt.Errorf("touch last login: %w", err)
	}
	return User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: row.Role}, nil
}

// burnHashTime makes a wrong email cost the time a wrong password does.
func burnHashTime(password string) {
	_, _ = HashPassword(password) //nolint:errcheck // discarding is the point
}

// StartSession issues a session and returns its token.
func (s *Store) StartSession(ctx context.Context, userID, userAgent, ip string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	// The column is inet: an unparseable address is NULL rather than a refused sign-in.
	var addr *netip.Addr
	if parsed, perr := netip.ParseAddr(ip); perr == nil {
		addr = &parsed
	}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: HashToken(token),
		UserID:    id,
		UserAgent: text(normaliseUserAgent(userAgent)),
		IP:        addr,
		Ttl: pgtype.Interval{
			Microseconds: int64(time.Duration(SessionTTL) * time.Second / time.Microsecond),
			Valid:        true,
		},
	}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// SessionUser returns who a session token belongs to.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	row, err := s.q.SessionUser(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read session: %w", err)
	}
	return User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: row.Role}, nil
}

// EndSession signs one browser out.
func (s *Store) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.q.DeleteSession(ctx, HashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// AdoptCart attaches a guest cart to an account on sign-in.
func (s *Store) AdoptCart(ctx context.Context, userID string, guestCartID uuid.UUID) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin adopt: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The account is the stable aggregate root for the one-cart decision. Take it
	// before reading CartForUser: two first adopters otherwise both observe no row
	// and meet only at the partial unique index. Every cart lock comes afterwards
	// and LockCarts sorts UUIDs, which keeps the cross-aggregate order canonical.
	lockedUser, err := q.LockUserForCartAdoption(ctx, id)
	if err != nil {
		return fmt.Errorf("lock account for cart adoption: %w", err)
	}
	if !lockedUser {
		return ErrNotFound
	}

	if err := adoptGuestCart(ctx, q, id, guestCartID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit adopt: %w", err)
	}
	return nil
}

// adoptGuestCart performs the cart-row portion after the caller has serialized
// the account's one-cart decision. Cart locks are always acquired in UUID order.
func adoptGuestCart(ctx context.Context, q *db.Queries, userID, guestCartID uuid.UUID) error {
	existing, err := q.CartForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if lockErr := lockCarts(ctx, q, guestCartID); lockErr != nil {
			return lockErr
		}
		if ownerErr := requireUnownedCart(ctx, q, guestCartID); ownerErr != nil {
			return ownerErr
		}
		n, adoptErr := q.AdoptCart(ctx, db.AdoptCartParams{
			CartID: guestCartID, UserID: userID,
		})
		if adoptErr != nil {
			return fmt.Errorf("adopt cart: %w", adoptErr)
		}
		if n == 0 {
			return ErrNotFound
		}
	case err != nil:
		return fmt.Errorf("read account cart: %w", err)
	case existing == guestCartID:
		// Already this account's cart. Nothing to do.
	default:
		if lockErr := lockCarts(ctx, q, guestCartID, existing); lockErr != nil {
			return lockErr
		}
		if ownerErr := requireUnownedCart(ctx, q, guestCartID); ownerErr != nil {
			return ownerErr
		}
		if mergeErr := q.MergeCartItems(ctx, db.MergeCartItemsParams{
			CartID: guestCartID, CartID_2: existing,
		}); mergeErr != nil {
			return fmt.Errorf("merge cart: %w", mergeErr)
		}
		if delErr := q.DeleteCart(ctx, guestCartID); delErr != nil {
			return fmt.Errorf("delete guest cart: %w", delErr)
		}
	}
	return nil
}

// requireUnownedCart revalidates the guest identity after its cart-row lock is
// held. A browser token can race another account's sign-in; ownership that won
// that race is not permission for this transaction to move or delete its cart.
func requireUnownedCart(ctx context.Context, q *db.Queries, cartID uuid.UUID) error {
	owner, err := q.CartOwner(ctx, cartID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read cart owner for adoption: %w", err)
	}
	if owner.Valid {
		return ErrNotFound
	}
	return nil
}

func lockCarts(ctx context.Context, q *db.Queries, ids ...uuid.UUID) error {
	locked, err := q.LockCarts(ctx, ids)
	if err != nil {
		return fmt.Errorf("lock carts for adoption: %w", err)
	}
	if len(locked) != len(ids) {
		return ErrNotFound
	}
	return nil
}

// ChangePassword sets a new password and ends every other session.
func (s *Store) ChangePassword(ctx context.Context, userID, password string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ID: id, PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, id); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

// Overview reads the account landing page.
func (s *Store) Overview(ctx context.Context, u User) (pages.AccountView, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("parse user id: %w", err)
	}

	view := pages.AccountView{Email: u.Email, Name: u.Name}

	profile, err := s.q.UserByID(ctx, id)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read profile: %w", err)
	}
	view.Phone = profile.Phone.String

	identities, err := s.q.IdentitiesForUser(ctx, id)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read linked identities: %w", err)
	}
	view.GoogleLinked = len(identities) > 0
	view.CanUnlinkGoogle = view.GoogleLinked && profile.HasPassword

	orders, err := s.q.UserOrders(ctx, db.UserOrdersParams{UserID: uuid.NullUUID{UUID: id, Valid: true}, Limit: 20})
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read orders: %w", err)
	}
	for i := range orders {
		o := &orders[i]
		view.Orders = append(view.Orders, pages.AccountOrder{
			Number:     o.OrderNumber,
			Status:     o.FulfillmentStatus,
			PlacedAt:   shoptime.Day(o.PlacedAt),
			TotalCents: o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
			LineCount:  o.LineCount,
			Committed:  o.Committed,
			OwedCents:  o.OwedCents,
		})
	}

	standing, err := s.q.MemberStanding(ctx, db.MemberStandingParams{
		Locale: string(i18n.FromContext(ctx)),
		UserID: id, WindowDays: MembershipWindowDays,
	})
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read member standing: %w", err)
	}
	view.Standing = pages.MemberStanding{
		SpendCents: standing.SpendCents, TierName: standing.TierName,
		MultiplierBP: standing.MultiplierBp,
		NextName:     standing.NextName, NextNeedsCents: standing.NextNeedsCents,
	}

	balance, err := s.q.StoreCreditBalance(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read store credit: %w", err)
	}
	view.CreditCents = balance

	addrs, err := s.q.AddressesForUser(ctx, id)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read addresses: %w", err)
	}
	for i := range addrs {
		a := &addrs[i]
		view.Addresses = append(view.Addresses, pages.AccountAddress{
			ID: a.ID.String(), Label: a.Label.String, Name: a.RecipientName, Phone: a.Phone,
			PostalCode: a.PostalCode, City: a.City, District: a.District,
			Street: a.Street, Default: a.IsDefault,
		})
	}
	return view, nil
}

// Order reads one of this account's orders.
func (s *Store) Order(ctx context.Context, u User, number string) (pages.AccountOrderView, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return pages.AccountOrderView{}, fmt.Errorf("parse user id: %w", err)
	}
	o, err := s.q.UserOrderByNumber(ctx, db.UserOrderByNumberParams{
		OrderNumber: number, UserID: uuid.NullUUID{UUID: id, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.AccountOrderView{}, ErrNotFound
		}
		return pages.AccountOrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderLinesByOrder(ctx, o.ID)
	if err != nil {
		return pages.AccountOrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	view := pages.AccountOrderView{
		Number: o.OrderNumber, Status: o.FulfillmentStatus,
		PlacedAt:      shoptime.Minute(o.PlacedAt),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason, TaxCents: o.TaxCents,
		Recipient: o.RecipientName, Phone: o.Phone, Email: o.Email,
		Address: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: pickup.Brand(o.PickupBrand), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		Committed: o.Committed,
		OwedCents: o.OwedCents,
	}
	for _, l := range lines {
		view.Lines = append(view.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
		})
	}
	return view, nil
}

// UpdateProfile changes the name and phone on an account.
func (s *Store) UpdateProfile(ctx context.Context, userID, name, phone string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	name, phone = strings.TrimSpace(name), strings.TrimSpace(phone)
	if !profileInputValid(name, phone) {
		return ErrInvalidInput
	}
	if err := s.q.UpdateProfile(ctx, db.UpdateProfileParams{
		ID: id, FullName: text(name), Phone: text(phone),
	}); err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// AddAddress saves a delivery address. Clearing the previous default happens in
// the same transaction: addresses_one_default_per_user is a unique partial index.
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
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if a.Default {
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

// SessionSweepInterval is how often expired sessions are deleted.
const SessionSweepInterval = 6 * time.Hour

// ResetTokenGrace is how long a dead reset token is kept after it stops working.
const ResetTokenGrace = 7 * 24 * time.Hour

// SweepSessions deletes every expired session and every dead reset token, once.
func (s *Store) SweepSessions(ctx context.Context) error {
	if err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if err := s.q.DeleteDeadResetTokens(ctx, pgtype.Interval{
		Microseconds: int64(ResetTokenGrace / time.Microsecond), Valid: true,
	}); err != nil {
		return fmt.Errorf("delete dead reset tokens: %w", err)
	}
	return nil
}

// SweepSessionsForever runs SweepSessions on a ticker until ctx is cancelled.
func (s *Store) SweepSessionsForever(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(SessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SweepSessions(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "sweep expired sessions", "error", err)
			}
		}
	}
}

// MakeDefaultAddress moves the default to another of this account's addresses.
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
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

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

// DeleteAddress removes one of this account's addresses.
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

// Erase runs the schema's erase_user, the only door an account leaves by.
func (s *Store) Erase(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.EraseUser(ctx, id); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "erase_user_open_return" {
			return ErrOpenReturn
		}
		return fmt.Errorf("erase user: %w", err)
	}
	return nil
}

// Address is a delivery address as a form supplies it.
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

// Validate checks an address before it is saved.
func (a *Address) Validate() []FieldError {
	var errs []FieldError
	appendAddressFieldError(&errs, "name", a.Name, maxNameRunes,
		i18n.KeyNameRequired, i18n.KeyNameTooLong)
	switch {
	case strings.TrimSpace(a.Phone) == "":
		errs = append(errs, FieldError{Field: "phone", MessageKey: i18n.KeyPhoneRequired})
	case !looksLikeDeliveryPhone(a.Phone):
		errs = append(errs, FieldError{Field: "phone", MessageKey: i18n.KeyPhoneMalformed})
	}
	switch {
	case strings.TrimSpace(a.PostalCode) == "":
		errs = append(errs, FieldError{Field: "postal_code", MessageKey: i18n.KeyPostalCodeRequired})
	case !isHomePostalCode(a.PostalCode):
		errs = append(errs, FieldError{Field: "postal_code", MessageKey: i18n.KeyPostalCodeMalformed})
	}
	appendAddressFieldError(&errs, "city", a.City, maxCityRunes,
		i18n.KeyCityRequired, i18n.KeyAddressIncomplete)
	appendAddressFieldError(&errs, "district", a.District, maxDistrictRunes,
		i18n.KeyDistrictRequired, i18n.KeyAddressIncomplete)
	appendAddressFieldError(&errs, "street", a.Street, maxStreetRunes,
		i18n.KeyStreetRequired, i18n.KeyStreetTooLong)
	if utf8.RuneCountInString(a.Label) > maxAddressLabelRunes {
		errs = append(errs, FieldError{Field: "label", MessageKey: i18n.KeyAddressIncomplete})
	}
	for _, f := range []struct{ name, value string }{
		{"label", a.Label}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
	} {
		if hasControl(f.value) {
			errs = append(errs, FieldError{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

// looksLikeDeliveryPhone is deliberately the same contract checkout applies —
// common Taiwan and international punctuation, 8–15 actual digits, a bounded
// label-safe representation — so a saved address cannot fail at checkout.
func looksLikeDeliveryPhone(s string) bool {
	if utf8.RuneCountInString(s) > maxPhoneRunes {
		return false
	}
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '-' || r == ' ' || r == '(' || r == ')' || r == '+':
		default:
			return false
		}
	}
	return digits >= 8 && digits <= 15
}

// isHomePostalCode accepts Taiwan's legacy three-digit and current longer
// numeric forms without guessing a city from the prefix.
func isHomePostalCode(s string) bool {
	if len(s) < 3 || len(s) > maxPostalCodeRunes {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func appendAddressFieldError(
	errs *[]FieldError,
	field, value string,
	maxRunes int,
	required, tooLong i18n.Key,
) {
	switch {
	case strings.TrimSpace(value) == "":
		*errs = append(*errs, FieldError{Field: field, MessageKey: required})
	case utf8.RuneCountInString(value) > maxRunes:
		*errs = append(*errs, FieldError{Field: field, MessageKey: tooLong})
	}
}

// Trim normalises the whitespace a form carries.
func (a *Address) Trim() {
	a.Label = strings.TrimSpace(a.Label)
	a.Name = strings.TrimSpace(a.Name)
	a.Phone = strings.TrimSpace(a.Phone)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
}

// Wishlist reads a customer's saved products.
func (s *Store) Wishlist(ctx context.Context, userID string) ([]pages.ProductTile, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.q.WishlistItems(ctx, db.WishlistItemsParams{
		UserID: id, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read wishlist: %w", err)
	}
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary.String,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out, nil
}

// SaveToWishlist adds a product, or does nothing if it is already there.
func (s *Store) SaveToWishlist(ctx context.Context, userID, slug string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotFound
	}
	if err := s.q.AddWishlistItem(ctx, db.AddWishlistItemParams{
		UserID: id, Slug: slug,
	}); err != nil {
		return fmt.Errorf("save to wishlist: %w", err)
	}
	return nil
}

// RemoveFromWishlist drops a product.
func (s *Store) RemoveFromWishlist(ctx context.Context, userID, slug string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotFound
	}
	if err := s.q.RemoveWishlistItem(ctx, db.RemoveWishlistItemParams{
		UserID: id, Slug: slug,
	}); err != nil {
		return fmt.Errorf("remove from wishlist: %w", err)
	}
	return nil
}

// SignInWithGoogle turns a verified Google identity into a goen session.
func (s *Store) SignInWithGoogle(ctx context.Context, id Identity) (User, error) {
	id, err := normaliseGoogleIdentity(id)
	if err != nil {
		return User{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin google sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The provider subject, not its mutable email, is the identity. Serialising on
	// it makes the initial lookup and a possible link one decision across every
	// process. hashtextextended collisions only serialize unrelated sign-ins; they
	// cannot merge identities because the unique index remains the final guard.
	if lockErr := q.LockGoogleSubject(ctx, id.Subject); lockErr != nil {
		return User{}, fmt.Errorf("lock google subject: %w", lockErr)
	}

	linked, lookupErr := q.UserByGoogleSubject(ctx, id.Subject)
	if lookupErr == nil {
		return finishGoogleSignIn(ctx, q, tx,
			linked.ID, linked.Email, linked.FullName, linked.Role)
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("read the linked account: %w", lookupErr)
	}

	candidateID, candidateEmail, candidateName, candidateRole, candidateErr :=
		googleLinkCandidate(ctx, q, id)
	if candidateErr != nil {
		return User{}, candidateErr
	}

	n, linkErr := q.LinkIdentity(ctx, db.LinkIdentityParams{
		UserID: candidateID, Subject: id.Subject,
	})
	if linkErr != nil {
		return User{}, fmt.Errorf("link the google identity: %w", linkErr)
	}
	if n == 0 {
		// Defensive even for a writer that did not take the advisory lock: never
		// commit a just-created orphan or return the email-selected candidate.
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil {
			return User{}, fmt.Errorf("roll back lost google link: %w", rollbackErr)
		}
		return s.googleSubjectOwner(ctx, id.Subject)
	}
	return finishGoogleSignIn(ctx, q, tx,
		candidateID, candidateEmail, candidateName, candidateRole)
}

func googleLinkCandidate(
	ctx context.Context,
	q *db.Queries,
	id Identity,
) (userID uuid.UUID, email string, fullName pgtype.Text, role string, err error) {
	existing, err := q.UserForOAuthLink(ctx, id.Email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row, createErr := q.CreateUserFromIdentity(ctx, db.CreateUserFromIdentityParams{
			Email: id.Email, FullName: id.Name,
		})
		if createErr != nil {
			// users_email_key is the real guard: another subject can claim the
			// address while this subject lock is held.
			if pgErr, ok := errors.AsType[*pgconn.PgError](createErr); ok && pgErr.Code == "23505" {
				return uuid.UUID{}, "", pgtype.Text{}, "", ErrOAuthCollision
			}
			return uuid.UUID{}, "", pgtype.Text{}, "",
				fmt.Errorf("create an account for %s: %w", id.Email, createErr)
		}
		return row.ID, row.Email, row.FullName, row.Role, nil
	case err != nil:
		return uuid.UUID{}, "", pgtype.Text{}, "",
			fmt.Errorf("read the account for %s: %w", id.Email, err)
	case !existing.Verified:
		// Pre-hijacking: an unverified account may belong to whoever registered
		// the address rather than to whoever reads the mailbox.
		return uuid.UUID{}, "", pgtype.Text{}, "", ErrOAuthCollision
	default:
		return existing.ID, existing.Email, existing.FullName, existing.Role, nil
	}
}

func finishGoogleSignIn(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	userID uuid.UUID,
	email string,
	fullName pgtype.Text,
	role string,
) (User, error) {
	if err := q.TouchLastLogin(ctx, userID); err != nil {
		return User{}, fmt.Errorf("touch last login: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit google sign-in: %w", err)
	}
	return User{
		ID: userID.String(), Email: email, Name: fullName.String, Role: role,
	}, nil
}

func normaliseGoogleIdentity(id Identity) (Identity, error) {
	id.Email = strings.TrimSpace(id.Email)
	if !id.EmailVerified || EmailError(id.Email) != "" {
		return Identity{}, ErrOAuthUnverified
	}

	subject := strings.TrimSpace(id.Subject)
	if subject == "" || subject != id.Subject ||
		utf8.RuneCountInString(subject) > maxOAuthSubjectRunes || hasControl(subject) {
		return Identity{}, errOAuthIdentity
	}

	// The display name is optional provider decoration, not identity. A malformed
	// or oversized value must not prevent sign-in or become an unbounded row.
	id.Name = strings.TrimSpace(id.Name)
	if utf8.RuneCountInString(id.Name) > maxNameRunes || hasControl(id.Name) {
		id.Name = ""
	}
	return id, nil
}

// googleSubjectOwner returns only the account the unique subject row names. It
// is the conflict fallback for a writer that did not participate in our lock.
func (s *Store) googleSubjectOwner(ctx context.Context, subject string) (User, error) {
	linked, err := s.q.UserByGoogleSubject(ctx, subject)
	if err != nil {
		return User{}, fmt.Errorf("read the winning google link: %w", err)
	}
	if err := s.q.TouchLastLogin(ctx, linked.ID); err != nil {
		return User{}, fmt.Errorf("touch last login: %w", err)
	}
	return User{
		ID: linked.ID.String(), Email: linked.Email,
		Name: linked.FullName.String, Role: linked.Role,
	}, nil
}

// UnlinkGoogle removes a provider from an account.
func (s *Store) UnlinkGoogle(ctx context.Context, u User) error {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	row, err := s.q.UserByID(ctx, id)
	if err != nil {
		return fmt.Errorf("read the account: %w", err)
	}
	if !row.HasPassword {
		return ErrLastSignInMethod
	}
	n, err := s.q.UnlinkIdentity(ctx, db.UnlinkIdentityParams{UserID: id, Provider: "google"})
	if err != nil {
		return fmt.Errorf("unlink google: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
