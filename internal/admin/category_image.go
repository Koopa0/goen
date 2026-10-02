package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// MaxCategoryAltRunes bounds the header photograph's alternative text.
const MaxCategoryAltRunes = 200

// CategoryHeader reads a category's own tone and photograph, as its edit page
// shows them.
func (s *Store) CategoryHeader(ctx context.Context, slug string) (admin.CategoryView, error) {
	row, err := s.q.AdminCategoryImage(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.CategoryView{}, ErrNotFound
		}
		return admin.CategoryView{}, fmt.Errorf("read category header: %w", err)
	}
	return admin.CategoryView{
		Slug: slug, Name: row.Name, Tone: row.Tone,
		Image: admin.Header{Key: row.ImageKey, Alt: row.ImageAlt, AltEn: row.ImageAltEn, Width: row.ImageWidth},
	}, nil
}

// SetCategoryImage makes a stored upload the category's header photograph. The
// alt text is required: categories_image_has_alt refuses an image without it.
func (s *Store) SetCategoryImage(ctx context.Context, slug, digest, alt, altEn string) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxCategoryAltRunes ||
		utf8.RuneCountInString(altEn) > MaxCategoryAltRunes {
		return fmt.Errorf("%w: header alt text is required and bounded at %d runes", ErrInvalid, MaxCategoryAltRunes)
	}
	return s.audited(ctx, Event{
		Action: actionSetCategoryImage, Table: "categories", ID: uuid.NullUUID{},
		After: map[string]any{"category": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCategoryImage(ctx, db.SetCategoryImageParams{
				Slug: strings.TrimSpace(slug), ImageKey: digest, ImageAlt: alt, ImageAltEn: altEn,
			})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// ClearCategoryImage removes the photograph; the media object itself stays.
func (s *Store) ClearCategoryImage(ctx context.Context, slug string) error {
	return s.audited(ctx, Event{
		Action: actionClearCategoryImage, Table: "categories", ID: uuid.NullUUID{},
		Before: map[string]any{"category": slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.ClearCategoryImage(ctx, strings.TrimSpace(slug))
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// EditCategory serves GET /admin/categories/{slug}.
func (h *Handler) EditCategory(w http.ResponseWriter, r *http.Request) {
	h.renderCategory(w, r, http.StatusOK, noticeFor(r), nil)
}

// renderCategory draws a category's edit page. A refused image form draws it
// again at 422 with the reason at the field.
func (h *Handler) renderCategory(w http.ResponseWriter, r *http.Request, status int, notice string, errs map[string]string) {
	slug := r.PathValue("slug")
	view, err := h.store.CategoryHeader(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read category header", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice, view.Errors = notice, errs
	web.Render(w, r, h.log, status, admin.CategoryForm(layouts.Page{Title: view.Name}, view))
}

// SetCategoryImage serves POST /admin/categories/{slug}/image. Multipart: the
// picture arrives with its alt text.
func (h *Handler) SetCategoryImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.StoreUpload(w, r, "image")
	if err != nil {
		h.log.WarnContext(r.Context(), "category image upload", "error", err, "slug", slug)
		reason := i18n.KeyAdminNoticeUploadFailed
		switch {
		case errors.Is(err, media.ErrTooLarge):
			reason = i18n.KeyAdminNoticeTooBig
		case errors.Is(err, media.ErrNotAnImage):
			reason = i18n.KeyAdminNoticeNotImage
		}
		h.renderCategory(w, r, http.StatusUnprocessableEntity, "", map[string]string{"image": i18n.T(r.Context(), reason)})
		return
	}
	err = h.store.SetCategoryImage(r.Context(), slug, obj.Digest, r.PostFormValue("alt"), r.PostFormValue("alt_en"))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/categories/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
	case errors.Is(err, ErrInvalid):
		field, reason := "alt", i18n.KeyFormHeroAlt
		if utf8.RuneCountInString(strings.TrimSpace(r.PostFormValue("alt_en"))) > MaxCategoryAltRunes {
			field, reason = "alt_en", i18n.KeyFormCampaignAltEnLong
		}
		h.renderCategory(w, r, http.StatusUnprocessableEntity, "", map[string]string{field: i18n.T(r.Context(), reason)})
	default:
		h.log.ErrorContext(r.Context(), "set category image", "error", err, "slug", slug)
		h.serverError(w, r)
	}
}

// RemoveCategoryImage serves POST /admin/categories/{slug}/image/remove.
func (h *Handler) RemoveCategoryImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.ClearCategoryImage(r.Context(), slug); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/categories/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
	default:
		h.log.ErrorContext(r.Context(), "remove category image", "error", err, "slug", slug)
		h.serverError(w, r)
	}
}
