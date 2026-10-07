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
	"github.com/koopa0/goen/internal/web"
)

func TestEveryRedirectTheProductFormsMakeCarriesAMessage(t *testing.T) {
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

func TestAttachImageRefusalNamesOnlyTheRefusedField(t *testing.T) {
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

func TestUploadErrorsDistinguishBadFormsFromStorageFailures(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name   string
			err    error
			status int
		}{
			{name: "bad form", err: web.ErrFormText, status: 400},
			{name: "wrapped bad form", err: fmt.Errorf("multipart: %w", web.ErrFormText), status: 400},
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
