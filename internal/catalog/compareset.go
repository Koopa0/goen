package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// The compare set is the products a shopper keeps to compare while browsing. It
// lives in a cookie, so the header can count it on every page and /compare can
// show it without a link. Only POST /compare/items writes it: a GET reads it and
// never writes, so following a link or a prefetch cannot change what somebody is
// comparing. A comparison named by ?p= is a link's and stays one; it is shown in
// place of the set and leaves the set as it was.
const (
	// compareCookie carries the __Host- prefix, which binds it to this origin;
	// a development server over plain http cannot set a __Host- cookie, so it
	// uses compareCookieDev.
	compareCookie    = "__Host-goen_compare"
	compareCookieDev = "goen_compare"
	// compareSetRetain is how long a browser keeps the set after the last change.
	compareSetRetain = 30 * 24 * time.Hour
	// compareSetJoin separates the slugs in the cookie; a slug never holds one.
	compareSetJoin = "."
)

// The two values the set's form posts as action. Anything but remove adds, as a
// form that says nothing means the button it carries.
const (
	compareAddAction    = "add"
	compareRemoveAction = "remove"
)

func compareCookieName(secure bool) string {
	if secure {
		return compareCookie
	}
	return compareCookieDev
}

// parseCompareSet reads the slugs a cookie names, in order, each once and at most
// MaxCompare: a cookie is whatever the browser sends, so a malformed or overlong
// one reads as what it can and never as an error.
func parseCompareSet(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, compareSetJoin)
	// No product's slug is longer (products_slug_bounded), so a longer one names
	// nothing and is not carried into a query.
	parts = slices.DeleteFunc(parts, func(s string) bool { return len(s) > maxSlugBytes })
	return boundedSlugs(parts)
}

// maxSlugBytes is products_slug_bounded's limit; a slug is ASCII, so its
// characters are its bytes.
const maxSlugBytes = 120

func readCompareSet(r *http.Request, secure bool) []string {
	c, err := r.Cookie(compareCookieName(secure))
	if err != nil {
		return nil
	}
	return parseCompareSet(c.Value)
}

// writeCompareSet sets the cookie to slugs, or expires it when there are none,
// with the attributes it was set with: a browser replaces a cookie only by one of
// the same name and path, and refuses a __Host- name without Secure.
func writeCompareSet(w http.ResponseWriter, slugs []string, secure bool) {
	c := &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     compareCookieName(secure),
		Value:    strings.Join(slugs, compareSetJoin),
		Path:     "/",
		MaxAge:   int(compareSetRetain / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	if len(slugs) == 0 {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

type compareSetKey struct{}

func withCompareSet(ctx context.Context, slugs []string) context.Context {
	return context.WithValue(ctx, compareSetKey{}, slugs)
}

func compareSetFrom(ctx context.Context) []string {
	slugs, ok := ctx.Value(compareSetKey{}).([]string)
	if !ok {
		return nil
	}
	return slugs
}

// inCompareSet reports whether slug is in the visitor's compare set as
// CompareSet.WithCount read it.
func inCompareSet(ctx context.Context, slug string) bool {
	return slices.Contains(compareSetFrom(ctx), slug)
}

// CompareSet serves the one write to the compare set and reads it for every
// page. secure decides the cookie's name and its Secure attribute, as the cart's
// does.
type CompareSet struct {
	store  *Store
	log    *slog.Logger
	secure bool
}

func NewCompareSet(store *Store, log *slog.Logger, secure bool) *CompareSet {
	if store == nil || log == nil {
		panic("catalog: NewCompareSet requires a store and a logger")
	}
	return &CompareSet{store: store, log: log, secure: secure}
}

// Routes registers the write. clearSpeculations is the deployment's: the set's
// count is in the shared header, so a page a browser speculated before the write
// would show the count from before it.
func (c *CompareSet) Routes(mux *http.ServeMux, clearSpeculations func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /compare/items", clearSpeculations(c.Update))
}

// WithCount reads the set into the request for the header's count, for /compare
// and for any CompareToggle on the page. It reads a cookie and nothing else.
func (c *CompareSet) WithCount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set := readCompareSet(r, c.secure)
		if len(set) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx := frame.WithCompareCount(withCompareSet(r.Context(), set), len(set))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Update adds the posted product to the set, or takes it out when action is
// "remove", and goes back to the page named in return. A product the set cannot
// show is not added, and a full set takes nothing more: it never drops one the
// shopper chose to make room. htmx is answered with the control and the header,
// so the button and the count change where they are.
func (c *CompareSet) Update(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// siteurl.PathOr refuses anything but a same-site path, which makes a
	// redirect target read off a form safe.
	back := siteurl.PathOr(r.PostFormValue("return"), "/compare")
	slug := r.PostFormValue("slug")
	if !siteurl.ValidSlug(slug) {
		http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
		return
	}

	remove := r.PostFormValue("action") == compareRemoveAction
	// Removing asks nothing of the catalogue, so a product taken off sale since
	// still leaves the set; adding asks whether the set may take it, and htmx's
	// answer names the product on its button.
	var name string
	offered := false
	if !remove || respond.IsHTMX(r) {
		var err error
		name, err = c.store.compareSetName(r.Context(), slug)
		if err != nil && !errors.Is(err, ErrNotFound) {
			c.log.ErrorContext(r.Context(), "read a product for the compare set", "error", err, "slug", slug)
			respond.Component(w, r, c.log, http.StatusInternalServerError, frame.Notice(
				frame.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
				i18n.T(r.Context(), i18n.KeyBusyTitle), i18n.T(r.Context(), i18n.KeyBusyBody)))
			return
		}
		offered = err == nil
	}

	before := readCompareSet(r, c.secure)
	set := changeCompareSet(before, slug, remove, offered)
	if !slices.Equal(set, before) {
		writeCompareSet(w, set, c.secure)
	}
	if !respond.IsHTMX(r) {
		http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
		return
	}
	ctx := frame.WithCompareCount(withCompareSet(r.Context(), set), len(set))
	ctx = frame.WithReturnPath(ctx, back)
	if offered && !remove && !slices.Contains(set, slug) {
		ctx = withCompareRefused(ctx)
	}
	respond.Component(w, r.WithContext(ctx), c.log, http.StatusOK, compareToggleSwap(slug, name))
}

type compareRefusedKey struct{}

// withCompareRefused marks the answer to an add the full set refused, which the
// toggle that answers it says in its status.
func withCompareRefused(ctx context.Context) context.Context {
	return context.WithValue(ctx, compareRefusedKey{}, true)
}

func compareRefused(ctx context.Context) bool {
	refused, ok := ctx.Value(compareRefusedKey{}).(bool)
	return ok && refused
}

// changeCompareSet is the set after one press: remove takes slug out wherever it
// is, add appends an offered slug that is not there while there is room. It
// returns a new slice and leaves set as it was.
func changeCompareSet(set []string, slug string, remove, offered bool) []string {
	out := slices.Clone(set)
	if remove {
		return slices.DeleteFunc(out, func(s string) bool { return s == slug })
	}
	if !offered || slices.Contains(out, slug) || len(out) >= MaxCompare {
		return out
	}
	return append(out, slug)
}

// compareSetName is the name of a product the compare set can take, in the
// request's language, and ErrNotFound for any slug it cannot.
func (s *Store) compareSetName(ctx context.Context, slug string) (string, error) {
	name, err := s.q.CompareSetProduct(ctx, db.CompareSetProductParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read compare set product %q: %w", slug, err)
	}
	return name, nil
}
