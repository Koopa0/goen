package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
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
//
// A duplicate email is ErrEmailTaken rather than a raw constraint violation,
// because it is the one failure the form has something to say about.
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
//
// An unknown email and a wrong password both return ErrBadCredentials, and the
// unknown-email path still runs a hash. Returning early would make a missing
// account measurably faster to reject than a wrong password, which turns the
// sign-in form into an account-enumeration oracle.
func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnHashTime(password)
			return User{}, ErrBadCredentials
		}
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if !row.PasswordHash.Valid {
		// An account with no password — created through an identity provider.
		// Same answer, same cost.
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

// burnHashTime spends roughly what a real verification costs, so a wrong email
// and a wrong password take the same time to refuse.
func burnHashTime(password string) {
	// The result is discarded on purpose: this exists only to spend the time a
	// real verification would, so a wrong email and a wrong password are equally
	// slow to refuse.
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
	// The column is inet, so the value has to parse as an address. An
	// unparseable one is stored as NULL rather than refusing the sign-in: the
	// session's audit trail is worth less than the sign-in itself.
	var addr *netip.Addr
	if parsed, perr := netip.ParseAddr(ip); perr == nil {
		addr = &parsed
	}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: HashToken(token),
		UserID:    id,
		UserAgent: text(userAgent),
		IP:        addr,
		ExpiresAt: time.Now().Add(SessionTTL * time.Second),
	}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// SessionUser returns who a session token belongs to. An expired session is
// nobody: the query checks expires_at, so a session is dead the moment it
// expires rather than when a sweeper next runs.
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
//
// The guest's lines are MERGED into whatever the account already had, rather
// than replacing it: someone who added things while signed out has not agreed
// to lose what was in their account cart, and the reverse is just as true.
func (s *Store) AdoptCart(ctx context.Context, userID string, guestCartID uuid.UUID) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin adopt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	existing, err := q.CartForUser(ctx, uuid.NullUUID{UUID: id, Valid: true})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No account cart yet: the guest cart becomes it.
		if adoptErr := q.AdoptCart(ctx, db.AdoptCartParams{
			ID: guestCartID, UserID: uuid.NullUUID{UUID: id, Valid: true},
		}); adoptErr != nil {
			return fmt.Errorf("adopt cart: %w", adoptErr)
		}
	case err != nil:
		return fmt.Errorf("read account cart: %w", err)
	case existing == guestCartID:
		// Already this account's cart. Nothing to do.
	default:
		if mergeErr := q.MergeCartItems(ctx, db.MergeCartItemsParams{
			CartID: guestCartID, CartID_2: existing,
		}); mergeErr != nil {
			return fmt.Errorf("merge cart: %w", mergeErr)
		}
		if delErr := q.DeleteCart(ctx, guestCartID); delErr != nil {
			return fmt.Errorf("delete guest cart: %w", delErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit adopt: %w", err)
	}
	return nil
}

// ChangePassword sets a new password and ENDS every other session.
//
// The second half is the point. A password change that leaves existing sessions
// alive has not locked anyone out, so a stolen session survives the very action
// taken to stop it.
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
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
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

	// The profile form's phone field was blank on every visit: the column is
	// written by registration and by the form itself, and nothing ever read it
	// back. A form that forgets what you told it reads as a form that did not
	// save — and UserByID, which exists for exactly this, had no caller.
	profile, err := s.q.UserByID(ctx, id)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read profile: %w", err)
	}
	view.Phone = profile.Phone.String

	// Which ways this account can be signed into. A customer who has forgotten
	// they used Google reads a password that does not work as a broken account.
	identities, err := s.q.IdentitiesForUser(ctx, id)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read linked identities: %w", err)
	}
	view.GoogleLinked = len(identities) > 0
	// Unlinking the only way in locks somebody out of their own orders, so the
	// control is absent rather than present and refused. UnlinkGoogle asks the
	// same question again at the write, because this one only decides what to
	// render.
	//
	// Read from the profile row this function already has, keyed on the ID.
	// Looking it up by EMAIL was the first version and it was wrong twice over:
	// a caller holding a User with no address made Overview fail outright, and
	// the id was right there.
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
			PlacedAt:   o.PlacedAt.Format("2006-01-02"),
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
//
// The owner is part of the query, not a check afterwards: an order belonging to
// someone else must be indistinguishable from one that does not exist, and a
// query that returns the row and then filters is one forgotten branch away from
// leaking it.
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
		PlacedAt:      o.PlacedAt.Format("2006-01-02 15:04"),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason, TaxCents: o.TaxCents,
		Recipient: o.RecipientName, Phone: o.Phone, Email: o.Email,
		Address: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: o.PickupBrand, PickupStoreCode: o.PickupStoreCode,
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

// AddAddress saves a delivery address.
//
// Making it the default clears the previous one in the SAME transaction,
// because addresses_one_default_per_user is a unique partial index: two
// defaults is not a state the table will hold, and clearing afterwards leaves a
// window where the insert has already failed.
func (s *Store) AddAddress(ctx context.Context, userID string, a *Address) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin add address: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
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
//
// Expiry is enforced by the QUERY that reads a session, so a row past its date
// is already nobody — this is about the table, not about correctness. Six
// hourly is often enough that it never becomes a large delete and rare enough
// that it is invisible.
const SessionSweepInterval = 6 * time.Hour

// ResetTokenGrace is how long a dead reset token is kept after it stops working.
//
// A week. The row is already nobody — the spend has `used_at IS NULL AND
// expires_at > now()` in its own WHERE clause — so this is only about being able
// to answer "did they ask for a reset?" while somebody is still asking.
const ResetTokenGrace = 7 * 24 * time.Hour

// SweepSessions deletes every expired session and every dead reset token, once.
//
// Nothing called DeleteExpiredSessions before. Every session goen ever issued
// stayed in the table: the reads were correct, because expiry is in their WHERE
// clauses, and the table grew without bound behind them. A row that is nobody
// is still a row somebody has to back up, and it holds the user id it belonged
// to — which is data goen said it would not keep.
func (s *Store) SweepSessions(ctx context.Context) error {
	if err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	// Same idea, same worker: an auth row that has stopped meaning anything.
	// Separate statements rather than one, because the two tables have nothing to
	// do with each other and a single failure should name which.
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
//
// The clear and the set are ONE transaction, because addresses_one_default_per_user
// is a unique index over (user_id) WHERE is_default — two statements with a
// commit between them are a moment where the account has two defaults, and the
// index refuses the second.
//
// Both statements are scoped to the owner IN the query. The id comes off a
// form, and "is this mine?" asked afterwards is a question somebody forgets:
// the failure here would be moving a stranger's default address.
//
// Nothing could change the default before this — the first address saved
// became the default and stayed it forever, and checkout prefills from it.
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
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if clearErr := q.ClearDefaultAddress(ctx, uid); clearErr != nil {
		return fmt.Errorf("clear default address: %w", clearErr)
	}
	n, err := q.SetDefaultAddress(ctx, db.SetDefaultAddressParams{UserID: uid, ID: aid})
	if err != nil {
		return fmt.Errorf("set default address: %w", err)
	}
	if n == 0 {
		// Zero rows is an address that is not this account's, or is gone. The
		// rollback matters: without it the account is left with NO default,
		// which is worse than the one it had.
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit set default address: %w", err)
	}
	return nil
}

// DeleteAddress removes one of this account's addresses. An id belonging to
// someone else deletes nothing: the owner is part of the statement.
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

// Erase runs the schema's erase_user, which is the ONLY way an account goes
// away: store holds no DELETE on users, so a direct delete is refused and the
// personal data on the account's orders would have been left behind.
func (s *Store) Erase(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.EraseUser(ctx, id); err != nil {
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

// Validate checks an address before it is saved. Every field the schema marks
// NOT NULL has to be present, and none may carry a control character.
func (a *Address) Validate() []FieldError {
	var errs []FieldError
	add := func(f string, k i18n.Key) { errs = append(errs, FieldError{Field: f, MessageKey: k}) }

	if strings.TrimSpace(a.Name) == "" {
		add("name", i18n.KeyNameRequired)
	}
	if strings.TrimSpace(a.Phone) == "" {
		add("phone", i18n.KeyPhoneRequired)
	}
	if strings.TrimSpace(a.PostalCode) == "" {
		add("postal_code", i18n.KeyPostalCodeRequired)
	}
	if strings.TrimSpace(a.City) == "" {
		add("city", i18n.KeyCityRequired)
	}
	if strings.TrimSpace(a.District) == "" {
		add("district", i18n.KeyDistrictRequired)
	}
	if strings.TrimSpace(a.Street) == "" {
		add("street", i18n.KeyStreetRequired)
	}
	for _, f := range []struct{ name, value string }{
		{"label", a.Label}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
	} {
		if hasControl(f.value) {
			add(f.name, i18n.KeyFieldHasControlChars)
		}
	}
	return errs
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
//
// An unknown or unpublished slug writes nothing rather than erroring: the
// SELECT that feeds the INSERT finds no row. A customer who followed a stale
// link gets their wishlist unchanged, which is what they would want, and an
// attacker learns nothing about which slugs exist.
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
//
// # The three cases, and the one that is a security decision
//
//  1. The subject is already linked. Sign that account in. The EMAIL is not
//     consulted at all: a Google account that changed address is the same
//     person, and user_identities keys on the subject for exactly this.
//
//  2. No link, and no goen account with that address. Create one with no
//     password and mark the address proved, because Google proved it.
//
//  3. No link, and an account with that address EXISTS. This is the decision.
//
// # Why case 3 does not auto-link an unverified account
//
// goen does not verify an address at registration — anybody may register
// victim@example.com and use the account. If a Google sign-in auto-linked on the
// address alone, an attacker could register the victim's address, wait, and
// collect the victim the moment they first used Google: same account, attacker's
// password, victim's orders and delivery address. That is pre-hijacking, and the
// mitigation is not to link to a local account that has not proved the address.
//
// So it links only when goen's OWN copy is verified — when both sides have
// proved the same mailbox. Otherwise it refuses and the customer is sent to
// /forgot, which already ends every session and hands control to whoever reads
// the mail: the legitimate owner recovers and an attacker sitting in the account
// is thrown out.
func (s *Store) SignInWithGoogle(ctx context.Context, id Identity) (User, error) {
	// Google's own claim comes first. An unverified address proves nothing, and
	// a Workspace administrator can set one to anything in their domain.
	if !id.EmailVerified || id.Email == "" {
		return User{}, ErrOAuthUnverified
	}

	linked, err := s.q.UserByGoogleSubject(ctx, id.Subject)
	if err == nil {
		if touchErr := s.q.TouchLastLogin(ctx, linked.ID); touchErr != nil {
			return User{}, fmt.Errorf("touch last login: %w", touchErr)
		}
		return User{
			ID: linked.ID.String(), Email: linked.Email,
			Name: linked.FullName.String, Role: linked.Role,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("read the linked account: %w", err)
	}

	existing, err := s.q.UserForOAuthLink(ctx, id.Email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return s.createFromIdentity(ctx, id)
	case err != nil:
		return User{}, fmt.Errorf("read the account for %s: %w", id.Email, err)
	case !existing.Verified:
		// See the header. The account exists and nobody has proved it belongs to
		// the person holding the mailbox.
		return User{}, ErrOAuthCollision
	}

	// Both sides proved the same address. Link and sign in.
	if linkErr := s.q.LinkIdentity(ctx, db.LinkIdentityParams{
		UserID: existing.ID, Subject: id.Subject,
	}); linkErr != nil {
		return User{}, fmt.Errorf("link the google identity: %w", linkErr)
	}
	if touchErr := s.q.TouchLastLogin(ctx, existing.ID); touchErr != nil {
		return User{}, fmt.Errorf("touch last login: %w", touchErr)
	}
	return User{
		ID: existing.ID.String(), Email: existing.Email,
		Name: existing.FullName.String, Role: existing.Role,
	}, nil
}

// createFromIdentity makes a new account for a provider that has proved an
// address, in one transaction with its link.
//
// Together, because a user with no identity is an account nobody can sign in to
// — it has no password either — and an identity with no user cannot exist at
// all. Split, a crash between them leaves the first, which the NEXT sign-in
// would then meet as an unverifiable collision.
func (s *Store) createFromIdentity(ctx context.Context, id Identity) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin identity sign-up: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.CreateUserFromIdentity(ctx, db.CreateUserFromIdentityParams{
		Email: id.Email, FullName: id.Name,
	})
	if err != nil {
		// A race with another tab, or with a password registration that landed
		// between the read above and this write. users_email_key is the real
		// guard; the pre-check only decides which message to show.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return User{}, ErrOAuthCollision
		}
		return User{}, fmt.Errorf("create an account for %s: %w", id.Email, err)
	}
	if linkErr := q.LinkIdentity(ctx, db.LinkIdentityParams{
		UserID: row.ID, Subject: id.Subject,
	}); linkErr != nil {
		return User{}, fmt.Errorf("link the google identity: %w", linkErr)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit identity sign-up: %w", err)
	}
	return User{
		ID: row.ID.String(), Email: row.Email,
		Name: row.FullName.String, Role: row.Role,
	}, nil
}

// UnlinkGoogle removes a provider from an account.
//
// Refused when the account has NO PASSWORD, because unlinking the only way in
// locks somebody out of their own orders — the same shape as /admin/staff
// refusing to revoke the last admin. The way out is to set a password first,
// which /forgot does and which is already the one path that proves the mailbox.
func (s *Store) UnlinkGoogle(ctx context.Context, u User) error {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	// Keyed on the ID rather than the address, for the reason Overview is: the
	// caller has an id and an address that may be empty, and only one of them is
	// the account's identity.
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
