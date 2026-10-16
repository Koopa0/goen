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

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/order"
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

func (s *Store) Overview(ctx context.Context, u user.User, after ...string) (AccountView, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return AccountView{}, fmt.Errorf("parse user id: %w", err)
	}

	view := AccountView{Email: u.Email, Name: u.Name}

	profile, err := s.q.UserByID(ctx, id)
	if err != nil {
		return AccountView{}, fmt.Errorf("read profile: %w", err)
	}
	view.Phone = profile.Phone.String
	view.HasPassword = profile.HasPassword

	identities, err := s.q.IdentitiesForUser(ctx, id)
	if err != nil {
		return AccountView{}, fmt.Errorf("read linked identities: %w", err)
	}
	view.GoogleLinked = len(identities) > 0
	view.CanUnlinkGoogle = view.GoogleLinked && profile.HasPassword

	cursor := readOrderCursor(u.ID, after)
	orders, err := s.q.UserOrders(ctx, db.UserOrdersParams{
		UserID: uuid.NullUUID{UUID: id, Valid: true}, RowLimit: orderPageSize + 1,
		HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
	})
	if err != nil {
		return AccountView{}, fmt.Errorf("read orders: %w", err)
	}
	orders, view.OrdersBound = orderBound(cursor, u.ID, orders,
		func(o *db.UserOrdersRow) (uuid.UUID, time.Time) { return o.ID, o.PlacedAt })

	orderIDs := make([]uuid.UUID, len(orders))
	for i := range orders {
		orderIDs[i] = orders[i].ID
	}
	returned, err := s.q.ReturnedOrders(ctx, orderIDs)
	if err != nil {
		return AccountView{}, fmt.Errorf("read returned orders: %w", err)
	}

	now := time.Now()
	for i := range orders {
		o := &orders[i]
		view.Orders = append(view.Orders, AccountOrder{
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
		return AccountView{}, fmt.Errorf("read member standing: %w", err)
	}
	view.Standing = loyalty.MemberStanding{
		SpendCents: standing.SpendCents, TierName: standing.TierName,
		MultiplierBP: standing.MultiplierBp,
		NextName:     standing.NextName, NextNeedsCents: standing.NextNeedsCents,
	}

	balance, err := s.q.StoreCreditBalance(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return AccountView{}, fmt.Errorf("read store credit: %w", err)
	}
	view.CreditCents = balance

	addrs, err := s.q.AddressesForUser(ctx, id)
	if err != nil {
		return AccountView{}, fmt.Errorf("read addresses: %w", err)
	}
	for i := range addrs {
		a := &addrs[i]
		view.Addresses = append(view.Addresses, AccountAddress{
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

func (s *Store) addressForUser(ctx context.Context, userID, addressID string) (AccountAddress, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return AccountAddress{}, err
	}
	aid, err := uuid.Parse(addressID)
	if err != nil {
		return AccountAddress{}, ErrNotFound
	}
	row, err := s.q.AddressForUser(ctx, db.AddressForUserParams{UserID: uid, AddressID: aid})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AccountAddress{}, ErrNotFound
		}
		return AccountAddress{}, err
	}
	return AccountAddress{
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

func (a *Address) Validate() []web.FieldRefusal {
	var errs []web.FieldRefusal
	appendAddressFieldRefusal(&errs, "name", a.Name, user.MaxNameRunes,
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
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
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
				ImageURL:     photo.URL(),
				ImageSrcset:  photo.Srcset(),
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
