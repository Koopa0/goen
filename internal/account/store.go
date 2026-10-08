package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
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
	mailmsg "github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"

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

// Register does the same work whether or not the address already has an
// account: the password is hashed either way, and one statement creates the
// account when the address is free and names it when not. The caller must
// answer both the same. Nobody is signed in: the account is usable once the
// mailed link has been followed with the password chosen here, by
// [Store.CompleteRegistration].
func (s *Store) Register(ctx context.Context, c *Credentials, next string) error {
	hash, err := HashPassword(c.Password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin registration: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.CreateUserUnlessRegistered(ctx, db.CreateUserUnlessRegisteredParams{
		Email: c.Email, PasswordHash: hash, FullName: text(c.Name),
	})
	if err != nil {
		return fmt.Errorf("record registration: %w", err)
	}
	registration := outbox.AccountRegistration{Created: row.Created, Locale: i18n.FromContext(ctx).Tag(), Next: next}
	if row.UserID != uuid.Nil {
		registration.UserID = row.UserID.String()
	}
	if err := outbox.Enqueue(ctx, q, outbox.TopicRegistration, "registration:"+uuid.NewString(), &registration); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit registration: %w", err)
	}
	return nil
}

func (s *Store) ResendRegistration(ctx context.Context, addr, next string) error {
	if err := s.q.EnqueueRegistrationResend(ctx, db.EnqueueRegistrationResendParams{
		Topic:     outbox.TopicRegistration.Name(),
		DedupeKey: "registration:" + uuid.NewString(),
		Locale:    i18n.FromContext(ctx).Tag(),
		Next:      next,
		Email:     addr,
	}); err != nil {
		return fmt.Errorf("queue registration resend: %w", err)
	}
	return nil
}

// FollowUpRegistration reads the account here rather than carrying it in the
// message, so one erased in between, or completed some other way, is nothing to
// do.
func (s *Store) FollowUpRegistration(
	ctx context.Context,
	r *outbox.AccountRegistration,
	tell func(context.Context, *mailmsg.AccountExists) error,
) error {
	if r.UserID == "" {
		return nil
	}
	id, err := uuid.Parse(r.UserID)
	if err != nil {
		return fmt.Errorf("parse registered account: %w", err)
	}
	row, err := s.q.RegistrationAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read registered account: %w", err)
	}
	if !r.Created {
		return tell(ctx, &mailmsg.AccountExists{Locale: r.Locale, Email: row.Email, Name: row.FullName.String})
	}
	if row.Verified {
		return nil
	}
	ctx = i18n.WithLocale(ctx, i18n.Parse(r.Locale))
	return s.queueVerification(ctx, r.UserID, row.Email, mailmsg.AddressVerify{
		Registration: true, Next: r.Next,
	})
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (user.User, error) {
	row, err := s.verifiedCredentials(ctx, email, password)
	if err != nil {
		return user.User{}, err
	}
	if err := s.q.TouchLastLogin(ctx, row.ID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	return user.User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: user.Role(row.Role)}, nil
}

// ConfirmPassword checks the verified account's password without recording a sign-in.
func (s *Store) ConfirmPassword(ctx context.Context, email, password string) error {
	_, err := s.verifiedCredentials(ctx, email, password)
	return err
}

func (s *Store) verifiedCredentials(ctx context.Context, email, password string) (db.UserByEmailRow, error) {
	// Refuse this before reading the account, or the outcomes are
	// distinguishable: burnHashTime returns immediately at this length while
	// VerifyPassword does not. Every password_hash writer in account goes
	// through HashPassword, which refuses an input over this same bound.
	if len(password) > MaxPasswordBytes {
		return db.UserByEmailRow{}, ErrBadCredentials
	}

	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			burnHashTime(password)
			return db.UserByEmailRow{}, ErrBadCredentials
		}
		return db.UserByEmailRow{}, fmt.Errorf("read user: %w", err)
	}
	if !passwordMatches(row.PasswordHash, password) {
		return db.UserByEmailRow{}, ErrBadCredentials
	}
	// After the hash, so an unproved account costs what a wrong password does.
	// Its password was chosen by whoever registered the address, who has not
	// yet shown they read the mailbox, and a different answer would say which
	// registrations created an account.
	if !row.Verified {
		return db.UserByEmailRow{}, ErrBadCredentials
	}
	return row, nil
}

// passwordMatches costs an account with no password the hash a wrong password
// does, so the two cannot be told apart by how long the answer takes.
func passwordMatches(hash pgtype.Text, password string) bool {
	if !hash.Valid {
		burnHashTime(password)
		return false
	}
	return VerifyPassword(hash.String, password)
}

func burnHashTime(password string) {
	_, _ = HashPassword(password) //nolint:errcheck // discarding is the point
}

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

func (s *Store) SessionUser(ctx context.Context, token string) (user.User, error) {
	if token == "" {
		return user.User{}, ErrNotFound
	}
	row, err := s.q.SessionUser(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, ErrNotFound
		}
		return user.User{}, fmt.Errorf("read session: %w", err)
	}
	return user.User{ID: row.ID.String(), Email: row.Email, Name: row.FullName.String, Role: user.Role(row.Role)}, nil
}

func (s *Store) SignedInRecently(ctx context.Context, token string, window time.Duration) (bool, error) {
	if token == "" {
		return false, nil
	}
	recent, err := s.q.SessionCreatedSince(ctx, db.SessionCreatedSinceParams{
		TokenHash: HashToken(token),
		MaxAge:    pgtype.Interval{Microseconds: int64(window / time.Microsecond), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read session age: %w", err)
	}
	return recent, nil
}

func (s *Store) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.q.DeleteSession(ctx, HashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) AdoptCart(ctx context.Context, userID string, guestCartID uuid.UUID) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin adopt: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The account is the stable aggregate root for the one-cart decision. Take
	// it before reading CartForUser: two first adopters otherwise both observe
	// no row and meet only at the partial unique index. Every cart lock comes
	// afterwards and LockCarts sorts UUIDs, which keeps the cross-aggregate
	// order canonical.
	if _, err := q.LockUser(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock account for cart adoption: %w", err)
	}

	if err := adoptGuestCart(ctx, q, id, guestCartID); err != nil {
		if !errors.Is(err, ErrQuantityAdjusted) {
			return err
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit adopt: %w", commitErr)
		}
		return ErrQuantityAdjusted
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit adopt: %w", err)
	}
	return nil
}

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
		adjusted, prepErr := clampGuestCartLines(ctx, q, guestCartID)
		if prepErr != nil {
			return prepErr
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
		if adjusted {
			return ErrQuantityAdjusted
		}
	case err != nil:
		return fmt.Errorf("read account cart: %w", err)
	case existing == guestCartID:
	default:
		if lockErr := lockCarts(ctx, q, guestCartID, existing); lockErr != nil {
			return lockErr
		}
		if ownerErr := requireUnownedCart(ctx, q, guestCartID); ownerErr != nil {
			return ownerErr
		}
		if mergeErr := mergeGuestIntoAccount(ctx, q, guestCartID, existing); mergeErr != nil {
			return mergeErr
		}
	}
	return nil
}

const maxCartLineQuantity int32 = 999

func mergeGuestIntoAccount(
	ctx context.Context, q *db.Queries, guestID, accountID uuid.UUID,
) error {
	guestLines, err := q.CartItemRows(ctx, guestID)
	if err != nil {
		return fmt.Errorf("read guest cart lines: %w", err)
	}
	var adjusted bool
	for i := range guestLines {
		line := &guestLines[i]
		sellable, err := sellableForMerge(ctx, q, line.VariantID)
		if err != nil {
			return err
		}
		existing := int32(0)
		if qty, readErr := q.CartLineQuantity(ctx, db.CartLineQuantityParams{
			CartID: accountID, VariantID: line.VariantID,
		}); readErr == nil {
			existing = qty
		} else if !errors.Is(readErr, pgx.ErrNoRows) {
			return fmt.Errorf("read account cart line: %w", readErr)
		}
		wanted := existing + line.Quantity
		merged := clampCartQuantity(wanted, sellable)
		if merged < wanted {
			adjusted = true
		}
		if existing > 0 {
			if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
				CartID: accountID, VariantID: line.VariantID, Quantity: merged,
			}); err != nil {
				return fmt.Errorf("merge cart line: %w", err)
			}
			continue
		}
		if err := q.AddCartItem(ctx, db.AddCartItemParams{
			CartID: accountID, VariantID: line.VariantID, Quantity: merged,
		}); err != nil {
			return fmt.Errorf("merge guest-only line: %w", err)
		}
	}
	if err := q.DeleteCart(ctx, guestID); err != nil {
		return fmt.Errorf("delete guest cart: %w", err)
	}
	if adjusted {
		return ErrQuantityAdjusted
	}
	return nil
}

func clampGuestCartLines(ctx context.Context, q *db.Queries, cartID uuid.UUID) (bool, error) {
	lines, err := q.CartItemRows(ctx, cartID)
	if err != nil {
		return false, fmt.Errorf("read guest cart lines: %w", err)
	}
	var adjusted bool
	for i := range lines {
		line := &lines[i]
		sellable, err := sellableForMerge(ctx, q, line.VariantID)
		if err != nil {
			return false, err
		}
		clamped := clampCartQuantity(line.Quantity, sellable)
		if clamped == line.Quantity {
			continue
		}
		adjusted = true
		if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
			CartID: cartID, VariantID: line.VariantID, Quantity: clamped,
		}); err != nil {
			return false, fmt.Errorf("clamp guest cart line: %w", err)
		}
	}
	return adjusted, nil
}

func sellableForMerge(ctx context.Context, q *db.Queries, variantID uuid.UUID) (int32, error) {
	v, err := q.VariantForCart(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCartMergeRefused
		}
		return 0, fmt.Errorf("read variant for merge: %w", err)
	}
	if !v.IsActive || pages.ProductStatus(v.Status) != pages.ProductActive || v.SellableQuantity <= 0 {
		return 0, ErrCartMergeRefused
	}
	return v.SellableQuantity, nil
}

func clampCartQuantity(wanted, sellable int32) int32 {
	if wanted > sellable {
		wanted = sellable
	}
	if wanted > maxCartLineQuantity {
		wanted = maxCartLineQuantity
	}
	return wanted
}

// requireUnownedCart revalidates after the cart-row lock: a browser token can
// race another account's sign-in, and ownership that won is not permission to
// move or delete the cart.
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
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ID: id, PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := q.InvalidateResetTokens(ctx, id); err != nil {
		return fmt.Errorf("invalidate reset tokens: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, id); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func (s *Store) Overview(ctx context.Context, u user.User, after ...string) (pages.AccountView, error) {
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

	cursor := readOrderCursor(u.ID, after)
	orders, err := s.q.UserOrders(ctx, db.UserOrdersParams{
		UserID: uuid.NullUUID{UUID: id, Valid: true}, RowLimit: orderPageSize + 1,
		HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
	})
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read orders: %w", err)
	}
	orders, view.OrdersBound = orderBound(cursor, u.ID, orders,
		func(o *db.UserOrdersRow) (uuid.UUID, time.Time) { return o.ID, o.PlacedAt })

	orderIDs := make([]uuid.UUID, len(orders))
	for i := range orders {
		orderIDs[i] = orders[i].ID
	}
	returned, err := s.q.ReturnedOrders(ctx, orderIDs)
	if err != nil {
		return pages.AccountView{}, fmt.Errorf("read returned orders: %w", err)
	}

	now := time.Now()
	for i := range orders {
		o := &orders[i]
		view.Orders = append(view.Orders, pages.AccountOrder{
			Number:     o.OrderNumber,
			Status:     order.FulfillmentStatus(o.FulfillmentStatus),
			PlacedAt:   shoptime.DateOf(o.PlacedAt, now),
			TotalCents: o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
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

func (s *Store) UpdateProfile(ctx context.Context, userID, name, phone string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	name, phone = strings.TrimSpace(name), strings.TrimSpace(web.FoldWidth(phone))
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
	defer pgtx.Rollback(ctx, tx)
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

const SessionSweepInterval = 6 * time.Hour

const ResetTokenGrace = 7 * 24 * time.Hour

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
	defer pgtx.Rollback(ctx, tx)
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

// Erase runs the schema's erase_user, the only door an account leaves by.
func (s *Store) Erase(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.EraseUser(ctx, id); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch pgErr.ConstraintName {
			case "erase_user_open_return":
				return ErrOpenReturn
			// users_keep_one_admin is the trigger behind erase_user's own
			// check, reached only if that check is bypassed; the refusal is the
			// same. The database's answer stays in the chain for callers that
			// name it.
			case "erase_user_keeps_one_admin", "users_keep_one_admin":
				return fmt.Errorf("%w: %w", ErrLastAdmin, err)
			}
		}
		return fmt.Errorf("erase user: %w", err)
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

func (a *Address) Validate() []web.FieldRefusal {
	var errs []web.FieldRefusal
	appendAddressFieldRefusal(&errs, "name", a.Name, maxNameRunes,
		i18n.KeyNameRequired, i18n.KeyNameTooLong)
	switch {
	case strings.TrimSpace(a.Phone) == "":
		errs = append(errs, web.FieldRefusal{Field: "phone", MessageKey: i18n.KeyPhoneRequired})
	case !looksLikeDeliveryPhone(a.Phone):
		errs = append(errs, web.FieldRefusal{Field: "phone", MessageKey: i18n.KeyPhoneMalformed})
	}
	switch {
	case strings.TrimSpace(a.PostalCode) == "":
		errs = append(errs, web.FieldRefusal{Field: "postal_code", MessageKey: i18n.KeyPostalCodeRequired})
	case !isHomePostalCode(a.PostalCode):
		errs = append(errs, web.FieldRefusal{Field: "postal_code", MessageKey: i18n.KeyPostalCodeMalformed})
	}
	appendAddressFieldRefusal(&errs, "city", a.City, maxCityRunes,
		i18n.KeyCityRequired, i18n.KeyAddressIncomplete)
	appendAddressFieldRefusal(&errs, "district", a.District, maxDistrictRunes,
		i18n.KeyDistrictRequired, i18n.KeyAddressIncomplete)
	appendAddressFieldRefusal(&errs, "street", a.Street, maxStreetRunes,
		i18n.KeyStreetRequired, i18n.KeyStreetTooLong)
	if utf8.RuneCountInString(a.Label) > maxAddressLabelRunes {
		errs = append(errs, web.FieldRefusal{Field: "label", MessageKey: i18n.KeyAddressIncomplete})
	}
	for _, f := range []struct{ name, value string }{
		{"label", a.Label}, {"name", a.Name}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
	} {
		if web.HasControlChars(f.value) {
			errs = append(errs, web.FieldRefusal{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

// looksLikeDeliveryPhone is deliberately the contract checkout applies, so a
// saved address cannot fail at checkout.
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

func appendAddressFieldRefusal(
	errs *[]web.FieldRefusal,
	field, value string,
	maxRunes int,
	required, tooLong i18n.Key,
) {
	switch {
	case strings.TrimSpace(value) == "":
		*errs = append(*errs, web.FieldRefusal{Field: field, MessageKey: required})
	case utf8.RuneCountInString(value) > maxRunes:
		*errs = append(*errs, web.FieldRefusal{Field: field, MessageKey: tooLong})
	}
}

func (a *Address) Trim() {
	a.Label = strings.TrimSpace(a.Label)
	a.Name = strings.TrimSpace(a.Name)
	a.Phone = strings.TrimSpace(web.FoldWidth(a.Phone))
	a.PostalCode = strings.TrimSpace(web.FoldWidth(a.PostalCode))
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
}

func (s *Store) Wishlist(ctx context.Context, userID string) ([]pages.WishlistItem, error) {
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
	out := make([]pages.WishlistItem, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		var soleVariant string
		if r.SoleVariantID != uuid.Nil {
			soleVariant = r.SoleVariantID.String()
		}
		out = append(out, pages.WishlistItem{
			SoleVariantID: soleVariant,
			ProductTile: pages.ProductTile{
				Slug:         r.Slug,
				Name:         r.Name,
				Summary:      r.Summary.String,
				Brand:        r.Brand,
				PriceCents:   r.MinPriceCents,
				PriceVaries:  r.PriceVaries,
				CompareCents: r.CompareAtPriceCents.Int64,
				InCampaign:   r.InCampaign,
				Rating:       r.Rating,
				RatingCount:  r.RatingCount,
				InStock:      r.InStock,
				Colours:      r.Colours,
				ImageURL:     assets.ProductImageURL(r.ImageKey),
				ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
				ImageAlt:     r.ImageAlt,
				ImageWidth:   r.ImageWidth,
				ImageHeight:  r.ImageHeight,
			},
		})
	}
	return out, nil
}

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

func (s *Store) SignInWithGoogle(ctx context.Context, id Identity) (user.User, error) {
	id, err := normaliseGoogleIdentity(id)
	if err != nil {
		return user.User{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin google sign-in: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The provider subject, not its mutable email, is the identity. Serialising
	// on it makes the lookup and a possible link one decision across every
	// process. hashtextextended collisions only serialize unrelated sign-ins;
	// the unique index remains the final guard.
	if lockErr := q.LockGoogleSubject(ctx, id.Subject); lockErr != nil {
		return user.User{}, fmt.Errorf("lock google subject: %w", lockErr)
	}

	linked, lookupErr := q.UserByGoogleSubject(ctx, id.Subject)
	if lookupErr == nil {
		return finishGoogleSignIn(ctx, q, tx,
			linked.ID, linked.Email, linked.FullName, linked.Role)
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return user.User{}, fmt.Errorf("read the linked account: %w", lookupErr)
	}

	candidateID, candidateEmail, candidateName, candidateRole, candidateErr :=
		googleLinkCandidate(ctx, q, id)
	if candidateErr != nil {
		return user.User{}, candidateErr
	}

	n, linkErr := q.LinkIdentity(ctx, db.LinkIdentityParams{
		UserID: candidateID, Subject: id.Subject,
	})
	if linkErr != nil {
		return user.User{}, fmt.Errorf("link the google identity: %w", linkErr)
	}
	if n == 0 {
		// Defensive even for a writer that did not take the advisory lock:
		// never commit a just-created orphan or return the email-selected
		// candidate.
		pgtx.Rollback(ctx, tx)
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
		// the address rather than whoever reads the mailbox.
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
) (user.User, error) {
	if err := q.TouchLastLogin(ctx, userID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit google sign-in: %w", err)
	}
	return user.User{
		ID: userID.String(), Email: email, Name: fullName.String, Role: user.Role(role),
	}, nil
}

func normaliseGoogleIdentity(id Identity) (Identity, error) {
	id.Email = strings.TrimSpace(id.Email)
	if !id.EmailVerified || EmailError(id.Email) != "" {
		return Identity{}, ErrOAuthUnverified
	}

	subject := strings.TrimSpace(id.Subject)
	if subject == "" || subject != id.Subject ||
		utf8.RuneCountInString(subject) > maxOAuthSubjectRunes || web.HasControlChars(subject) {
		return Identity{}, errOAuthIdentity
	}

	// The display name is decoration, not identity: a malformed or oversized
	// value must not prevent sign-in or become an unbounded row.
	id.Name = strings.TrimSpace(id.Name)
	if utf8.RuneCountInString(id.Name) > maxNameRunes || web.HasControlChars(id.Name) {
		id.Name = ""
	}
	return id, nil
}

// googleSubjectOwner is the conflict fallback for a writer that did not take
// our lock.
func (s *Store) googleSubjectOwner(ctx context.Context, subject string) (user.User, error) {
	linked, err := s.q.UserByGoogleSubject(ctx, subject)
	if err != nil {
		return user.User{}, fmt.Errorf("read the winning google link: %w", err)
	}
	if err := s.q.TouchLastLogin(ctx, linked.ID); err != nil {
		return user.User{}, fmt.Errorf("touch last login: %w", err)
	}
	return user.User{
		ID: linked.ID.String(), Email: linked.Email,
		Name: linked.FullName.String, Role: user.Role(linked.Role),
	}, nil
}

func (s *Store) UnlinkGoogle(ctx context.Context, u user.User) error {
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
