package site

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// dismissCookie holds a digest of the banner's id, never the id, because it
// travels on every request.
const dismissCookie = "__Host-goen_promo"

// insecureDismissCookie is the development name: a __Host- cookie is never sent
// back over plain http://.
const insecureDismissCookie = "goen_promo"

// dismissMaxAge is a year; the cookie is keyed to one banner, so the promotion
// ending is what brings the strip back, not the cookie expiring.
const dismissMaxAge = 365 * 24 * 60 * 60

// Banner returns nothing when there is nothing to show; dismissed is the digest
// the request carried.
func (s *Store) Banner(ctx context.Context, dismissed string) (frame.Banner, error) {
	row, err := s.q.CurrentPromoBanner(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No fallback, unlike the hero: a shop with nothing to announce
			// announces nothing.
			return frame.Banner{}, nil
		}
		return frame.Banner{}, fmt.Errorf("read promo banner: %w", err)
	}

	id := row.ID.String()
	if dismissed != "" && dismissed == dismissDigest(id) {
		return frame.Banner{}, nil
	}

	// Typed by a person and rendered into an href at the top of every page.
	href, ok := siteurl.Path(row.CtaHref)
	if !ok {
		href = ""
	}
	label := row.CtaLabel
	if href == "" {
		label = ""
	}

	return frame.Banner{
		ID: id, Message: row.Message, MessageShort: row.MessageShort,
		Code: row.Code, CTALabel: label, CTAHref: href,
	}, nil
}

func dismissDigest(id string) string {
	sum := sha256.Sum256([]byte("goen-promo:" + id))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

func readDismissal(r *http.Request, secure bool) string {
	name := insecureDismissCookie
	if secure {
		name = dismissCookie
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func writeDismissal(w http.ResponseWriter, id string, secure bool) {
	name := insecureDismissCookie
	if secure {
		name = dismissCookie
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     name,
		Value:    dismissDigest(id),
		Path:     "/",
		MaxAge:   dismissMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// DismissBanner serves the strip's close button.
func (h *Handler) DismissBanner(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	// The form echoes the id, so the cookie names the banner that was on screen
	// rather than whatever is current when the POST lands.
	if id := r.PostFormValue("banner"); id != "" {
		writeDismissal(w, id, h.secure)
	}
	//nolint:gosec // G710: siteurl.PathOr accepts only a same-site path
	http.Redirect(w, r, siteurl.PathOr(r.PostFormValue("return"), "/"), http.StatusSeeOther)
}

// WithBanner attaches the promotional strip to the request. Not cached,
// because a shop that switches a promotion off expects it gone.
func (h *Handler) WithBanner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		banner, err := h.content.Banner(r.Context(), readDismissal(r, h.secure))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // the caller left; there is nobody to serve
			}
			// Not fatal: an absent banner renders as nothing at all.
			h.log.ErrorContext(r.Context(), "read promo banner", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(frame.WithBanner(r.Context(), banner)))
	})
}

// WithTopNav loads the header's category row. A failure degrades to a nav with
// no links.
func (h *Handler) WithTopNav(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := h.content.Nav(r.Context())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // the caller left; there is nobody to serve
			}
			h.log.ErrorContext(r.Context(), "read nav categories", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withNav(r.Context(), items, h.dealsOnOffer(r.Context()))))
	})
}

func withNav(ctx context.Context, items []frame.NavItem, deals bool) context.Context {
	return frame.WithDeals(frame.WithTopNav(ctx, items), deals)
}

// dealsOnOffer is false when the read fails, like the department row beside it: a link
// to a page with nothing on it is the worse mistake.
func (h *Handler) dealsOnOffer(ctx context.Context) bool {
	offered, err := h.deals.DealsOnOffer(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			h.log.ErrorContext(ctx, "read whether deals are on offer", "error", err)
		}
		return false
	}
	return offered
}

// Departments reads the top-level categories in the order the header lists
// them, and how many active products each holds across its subtree. The home
// page reads them here too, so its tiles and the header cannot disagree.
func (s *Store) Departments(ctx context.Context) ([]db.RootCategoriesRow, map[uuid.UUID]int64, error) {
	rows, err := s.q.RootCategories(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return nil, nil, fmt.Errorf("read root categories: %w", err)
	}
	stock, err := s.q.DepartmentStock(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("read department sizes: %w", err)
	}
	held := make(map[uuid.UUID]int64, len(stock))
	for i := range stock {
		held[stock[i].ID] = stock[i].Products
	}
	return rows, held, nil
}

// Nav is read by middleware, like Banner, rather than by a page's own handler.
func (s *Store) Nav(ctx context.Context) ([]frame.NavItem, error) {
	locale := string(i18n.FromContext(ctx))
	rows, held, err := s.Departments(ctx)
	if err != nil {
		return nil, fmt.Errorf("read nav categories: %w", err)
	}
	subs, err := s.q.ChildCategories(ctx, locale)
	if err != nil {
		return nil, fmt.Errorf("read nav sub-categories: %w", err)
	}
	children := make(map[uuid.UUID][]frame.NavItem, len(rows))
	for i := range subs {
		c := &subs[i]
		children[c.ParentID.UUID] = append(children[c.ParentID.UUID], frame.NavItem{
			Slug: c.Slug, Name: c.Name, Href: "/c/" + c.Slug,
		})
	}
	picks, err := s.navPicks(ctx, locale)
	if err != nil {
		return nil, err
	}
	items := make([]frame.NavItem, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		if held[r.ID] == 0 {
			continue
		}
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		items = append(items, frame.NavItem{
			Slug: r.Slug, Name: r.Name, Href: "/c/" + r.Slug, ProductCount: int(held[r.ID]), Children: children[r.ID], Picks: picks[r.ID],
			PhotoURL: photo.URL(), PhotoSrcset: photo.Srcset(),
		})
	}
	return items, nil
}

func (s *Store) navPicks(ctx context.Context, locale string) (map[uuid.UUID][]frame.NavPick, error) {
	rows, err := s.q.NavPicks(ctx, locale)
	if err != nil {
		return nil, fmt.Errorf("read nav picks: %w", err)
	}
	out := make(map[uuid.UUID][]frame.NavPick)
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		price := money.TWD(r.PriceCents)
		if r.PriceVaries {
			price = fmt.Sprintf(i18n.T(ctx, i18n.KeyFromPrice), price)
		}
		out[r.RootID] = append(out[r.RootID], frame.NavPick{
			Slug:        r.Slug,
			Name:        r.Name,
			Price:       price,
			ImageURL:    photo.URL(),
			ImageSrcset: photo.Srcset(),
		})
	}
	return out, nil
}
