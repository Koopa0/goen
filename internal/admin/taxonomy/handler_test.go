package taxonomy

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestEveryRedirectTheTaxonomyFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
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
