// Package access decides who reaches a back-office handler, and gives the
// back office's 404 and 500 answers.
package access

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type Control struct {
	log *slog.Logger
	// stepUp reports whether this session proved a second factor; nil is a
	// deployment with no encryption key, where 2FA is off.
	stepUp          func(*http.Request) (bool, error)
	healthTaskCount func(context.Context) (int64, error)
}

func New(log *slog.Logger, stepUp func(*http.Request) (bool, error)) *Control {
	if log == nil {
		panic("access: New requires a logger")
	}
	return &Control{log: log, stepUp: stepUp}
}

func (c *Control) WithHealthTaskCount(read func(context.Context) (int64, error)) *Control {
	navigation := *c
	navigation.healthTaskCount = read
	return &navigation
}

// RequireStaff wraps a back-office handler. Signed out and signed-in-but-not-
// staff get the SAME answer, a 404: anything else — a 403, or a redirect to
// /signin?next=/admin — confirms that /admin is a real place.
func (c *Control) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok || !u.IsStaff() {
			NotFound(w, r, c.log)
			return
		}

		// The SECOND factor, checked here rather than at sign-in: gating the
		// login would need a half-authenticated state to live somewhere, and a
		// session that "does not count yet" eventually counts.
		if c.stepUp != nil {
			verified, err := c.stepUp(r)
			if err != nil {
				c.log.ErrorContext(r.Context(), "read second factor", "error", err)
				ServerError(w, r, c.log)
				return
			}
			if !verified {
				http.Redirect(w, r, "/admin/verify", http.StatusSeeOther)
				return
			}
		}
		ctx := layouts.WithAdmin(r.Context(), u.IsAdmin())
		next(w, r.WithContext(c.healthTaskContext(ctx)))
	}
}

func (c *Control) healthTaskContext(ctx context.Context) context.Context {
	ctx = layouts.WithHealthTaskCount(ctx, 0, false)
	if c.healthTaskCount == nil {
		return ctx
	}
	count, err := c.readHealthTaskCount(ctx)
	if err == nil {
		return layouts.WithHealthTaskCount(ctx, count, true)
	}
	if ctx.Err() == nil {
		c.log.ErrorContext(ctx, "read background task count", "error", err)
	}
	return ctx
}

func (c *Control) readHealthTaskCount(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return c.healthTaskCount(ctx)
}

// StaffOnly answers 404 to anyone who does not work here, and runs no step-up.
//
// It is what the second-factor routes need: RequireStaff redirects an unverified
// staff member TO /admin/verify, so guarding that page with it is a loop. Nothing
// weaker will do — the enrolment page inserts into staff_totp_credentials over
// the ADMIN pool.
func (c *Control) StaffOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok || !u.IsStaff() {
			NotFound(w, r, c.log)
			return
		}
		next(w, r)
	}
}

// RequireAdmin wraps a back-office handler that changes WHO WORKS HERE: gated
// on the staff predicate, /admin/staff is a self-service promotion desk.
func (c *Control) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return c.RequireStaff(func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok || !u.IsAdmin() {
			NotFound(w, r, c.log)
			return
		}
		next(w, r)
	})
}

func NotFound(w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	web.Render(w, r, log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNotFoundTitle)}, "404",
		i18n.T(r.Context(), i18n.KeyAdminNotFoundHead),
		i18n.T(r.Context(), i18n.KeyAdminNotFoundBody)))
}

// Fault is the failure page of the two-factor and staff routes, which a staff
// member reaches in a browser: a bare status code would show them an unstyled
// "500" with no way back.
func Fault(w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	web.Render(w, r, log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminFaultTitle)}, "",
		i18n.T(r.Context(), i18n.KeyAdminFaultHead),
		i18n.T(r.Context(), i18n.KeyAdminFaultBody)))
}

func ServerError(w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	web.Render(w, r, log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminErrorTitle)}, "500",
		i18n.T(r.Context(), i18n.KeyAdminErrorTitle),
		i18n.T(r.Context(), i18n.KeyAdminErrorBody)))
}
