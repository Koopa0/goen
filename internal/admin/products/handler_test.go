package products

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
)

// TestNoticesCoverRedirects: every redirect the product forms make carries a
// message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	param := regexp.MustCompile(`[?"]([a-z]+)=1`)
	sent := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: this package's own source files
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range param.FindAllStringSubmatch(string(src), -1) {
			sent[m[1]] = true
		}
	}
	if len(sent) < 5 {
		t.Fatalf("only %d redirect parameters found; the parser stopped matching", len(sent))
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

// TestAttachImageRefusal: attachImageRefusal names only the refused field.
func TestAttachImageRefusal(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		err   error
		alt   string
		field string
		key   i18n.Key
	}{
		{name: "empty primary description", err: ErrInvalid, alt: " \t ", field: "alt", key: i18n.KeyFormHeroAlt},
		{name: "long primary description", err: ErrInvalid, alt: strings.Repeat("界", 201), field: "alt", key: i18n.KeyFormHeroAlt},
		{name: "bounded primary and long English description", err: ErrInvalid, alt: strings.Repeat("界", 200), field: "alt_en", key: i18n.KeyFormCampaignAltEnLong},
		{name: "wrong product option", err: ErrNotThisProductsOption, alt: "Picture", field: "image_option", key: i18n.KeyAdminNoticeBadOption},
		{name: "attachment rule refusal", err: ErrRefused, alt: "Picture", field: "image", key: i18n.KeyAdminNoticeAttachRefused},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03"}, alt: "Picture"},
		{name: "insert fault", err: &pgconn.PgError{Code: "XX000"}, alt: "Picture"},
		{name: "permission fault", err: &pgconn.PgError{Code: "42501"}, alt: "Picture"},
		{name: "cancelled request", err: context.Canceled, alt: "Picture"},
		{name: "lost pool", err: errors.New("pool closed"), alt: "Picture"},
		{name: "no error", alt: "Picture"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, err := range []error{tt.err, fmt.Errorf("attach: %w", tt.err)} {
				field, key := attachImageRefusal(err, tt.alt)
				if diff := cmp.Diff([]string{tt.field, string(tt.key)}, []string{field, string(key)}); diff != "" {
					t.Errorf("attach image refusal (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestHandler_RespondToUploadError: upload errors distinguish bad forms from
// storage failures.
func TestHandler_RespondToUploadError(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name   string
			err    error
			status int
		}{
			{name: "bad form", err: formtext.ErrUnstorable, status: 400},
			{name: "wrapped bad form", err: fmt.Errorf("multipart: %w", formtext.ErrUnstorable), status: 400},
			{name: "storage", err: fmt.Errorf("store image: %w", errors.New("storage witness")), status: 500},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				var diagnostics bytes.Buffer
				h := &Handler{log: slog.New(slog.NewJSONHandler(&diagnostics, nil))}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/upload", nil)
				req.SetPathValue("slug", "upload-fixture")
				res := httptest.NewRecorder()
				h.respondToUploadError(res, req, tt.err)
				if res.Code != tt.status {
					t.Errorf("upload error status = %d, want %d", res.Code, tt.status)
				}
				if strings.Contains(res.Body.String(), "storage witness") || strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeUploadFailed)) {
					t.Error("non-image failure leaked its cause or blamed the file")
				}
				if tt.status == 500 && (!strings.Contains(diagnostics.String(), `"level":"ERROR"`) || !strings.Contains(diagnostics.String(), "storage witness")) {
					t.Errorf("storage diagnostics = %q, want Error with the wrapped cause", diagnostics.String())
				}
			})
		}
	}
}

// TestHandler_RespondToUploadAborted: an abandoned upload is answered with
// nothing.
//
// A browser that left mid-upload has no one to read a notice: the answer is
// empty and nothing is logged as a failure.
func TestHandler_RespondToUploadAborted(t *testing.T) {
	t.Parallel()
	var diagnostics bytes.Buffer
	h := &Handler{log: slog.New(slog.NewJSONHandler(&diagnostics, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/upload", nil)
	req.SetPathValue("slug", "upload-fixture")
	res := httptest.NewRecorder()
	err := fmt.Errorf("read upload: %w", media.ErrUploadAborted)
	h.respondToUploadError(res, req, err)
	if res.Body.Len() != 0 || diagnostics.Len() != 0 {
		t.Errorf("abandoned upload wrote body %q and log %q, want neither", res.Body.String(), diagnostics.String())
	}
}

// TestStatusNoticeSaysTheStatus: a status change says which status the product
// moved to, for every status there is.
func TestStatusNoticeSaysTheStatus(t *testing.T) {
	t.Parallel()
	want := map[product.Status]string{
		product.StatusActive:   "?published=1",
		product.StatusDraft:    "?drafted=1",
		product.StatusArchived: "?archived=1",
	}
	for _, status := range product.Statuses {
		got := statusNotice(status)
		if got != want[status] {
			t.Errorf("statusNotice(%q) = %q, want %q", status, got, want[status])
		}
		code := strings.TrimSuffix(strings.TrimPrefix(got, "?"), "=1")
		if entry, ok := notices[code]; !ok || entry.Outcome != widget.OutcomeDone {
			t.Errorf("statusNotice(%q) names ?%s=1, which is not a done notice", status, code)
		}
	}
}

// TestNoticesDoneSentencesDiffer: no two actions share a sentence, so a save says
// which change it was.
func TestNoticesDoneSentencesDiffer(t *testing.T) {
	t.Parallel()
	seen := map[i18n.Key]string{}
	for name, e := range notices {
		if e.Outcome != widget.OutcomeDone {
			continue
		}
		if first, dup := seen[e.Key]; dup {
			t.Errorf("?%s=1 and ?%s=1 both say %q", first, name, e.Key)
		}
		seen[e.Key] = name
	}
}
