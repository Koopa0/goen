//go:build integration

package content_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
)

func TestNewsletterCompositionAcceptsItsFullTextLimits(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
	}{
		{name: "Han", text: "\u6587"},
		{name: "four-byte Unicode", text: "\U0001F331"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			p := admintest.AdminRolePool(t, pool)
			log := slog.New(slog.DiscardHandler)
			h := content.NewHandler(content.NewStore(p), media.NewHandler(media.NewStore(p), log), newsletter.NewStore(p), log)
			mux := http.NewServeMux()
			h.Routes(mux, admintest.BackOffice)
			subject := uuid.NewString()[:8] + strings.Repeat(tt.text, 112)
			body := strings.Repeat(tt.text, 20000)
			form := url.Values{"subject": {subject}, "body": {body}}
			encoded := form.Encode()
			if len(encoded) <= 65536 {
				t.Fatal("fixture does not exceed the old form cap")
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/newsletter", strings.NewReader(encoded))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/newsletter?saved=1" {
				t.Fatalf("legal newsletter status/location=%d/%q, want 303 saved", res.Code, res.Header().Get("Location"))
			}
			var savedSubject, savedBody string
			var sent bool
			if err := pool.QueryRow(ctx, `SELECT subject,body,sent_at IS NOT NULL FROM newsletter_issues WHERE subject=$1`, subject).Scan(&savedSubject, &savedBody, &sent); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff([]string{subject, body}, []string{savedSubject, savedBody}); diff != "" {
				t.Errorf("saved newsletter text (-want +got):\n%s", diff)
			}
			if sent {
				t.Error("composition sent the draft")
			}
			for _, locale := range i18n.Locales() {
				refusedSubject := uuid.NewString()[:8] + strings.Repeat(tt.text, 113)
				refusedBody := strings.Repeat(tt.text, 20001)
				form.Set("subject", refusedSubject)
				form.Set("body", refusedBody)
				req = httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/newsletter", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res = httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("over-limit newsletter (%s) status=%d, want 422", locale.Tag(), res.Code)
				}
				admintest.AssertRefusedInput(t, res.Body.String(), "issue-subject", refusedSubject)
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				var draft strings.Builder
				var invalid, described string
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode || n.Data != "textarea" {
						continue
					}
					for _, a := range n.Attr {
						if a.Key == "aria-invalid" {
							invalid = a.Val
						}
						if a.Key == "aria-describedby" {
							described = a.Val
						}
					}
					for child := range n.Descendants() {
						if child.Type == html.TextNode {
							draft.WriteString(child.Data)
						}
					}
				}
				if draft.String() != refusedBody || invalid != "true" || described != "issue-body-error" {
					t.Errorf("refused newsletter body length/invalid/described=%d/%q/%q, want retained text and accessible error", draft.Len(), invalid, described)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM newsletter_issues WHERE subject=$1`, refusedSubject).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("refused draft rows=%d, want 0", count)
				}
			}
		})
	}
}
