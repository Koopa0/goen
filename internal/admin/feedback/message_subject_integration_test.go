//go:build integration

package feedback_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestContactSubjectsSurviveWritingAndRenderInBothLanguages(t *testing.T) {
	storePool := contactSubjectStorePool(t)
	writer := contact.NewStore(storePool)
	h := contact.NewHandler(writer, ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 100, TTL: time.Hour, MaxKeys: 10,
	}), slog.New(slog.DiscardHandler))
	desk := feedback.NewStore(admintest.AdminRolePool(t, pool))

	tests := []struct {
		value string
		zh    string
		en    string
	}{
		{"訂單問題", "訂單問題", "An order"},
		{"退換貨", "退貨", "Returns"},
		{"保固維修", "保固維修", "Warranty or repair"},
		{"商品諮詢", "商品諮詢", "A product question"},
		{"合作提案", "合作提案", "A partnership"},
	}
	for _, language := range []struct {
		locale i18n.Locale
		en     bool
	}{{i18n.ZhHant, false}, {i18n.En, true}} {
		t.Run(language.locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), language.locale)
			page := httptest.NewRecorder()
			h.Page(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/contact", nil))
			wantChoices := make([]pages.ContactSubject, 0, len(tests))
			for _, tc := range tests {
				label := tc.zh
				if language.en {
					label = tc.en
				}
				wantChoices = append(wantChoices, pages.ContactSubject{Value: tc.value, Label: label})
			}
			if diff := cmp.Diff(wantChoices, contactSubjectPicker(t, page.Body.String())); diff != "" {
				t.Errorf("contact picker (-want +got):\n%s", diff)
			}

			for _, tc := range tests {
				t.Run(tc.value, func(t *testing.T) {
					email := "contact-subject-" + uuid.NewString() + "@example.com"
					t.Cleanup(func() {
						if _, err := pool.Exec(context.WithoutCancel(t.Context()), `DELETE FROM contact_messages WHERE email=$1`, email); err != nil {
							t.Errorf("remove test-owned contact message: %v", err)
						}
					})
					form := url.Values{
						"name": {"Reader 原文"}, "email": {email}, "subject": {tc.value},
						"order_ref": {"GO-261005-123456"}, "message": {"Customer note: 訂單問題。"},
					}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/contact", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					res := httptest.NewRecorder()
					h.Submit(res, req)
					if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/contact?sent=1" {
						t.Fatalf("Submit(%q) = %d, %q, want 303 and safe acknowledgement", tc.value, res.Code, res.Header().Get("Location"))
					}
					var stored string
					if err := pool.QueryRow(t.Context(), `SELECT subject FROM contact_messages WHERE email=$1`, email).Scan(&stored); err != nil {
						t.Fatalf("read stored contact category: %v", err)
					}
					if stored != tc.value {
						t.Errorf("stored subject = %q, want locale-independent %q", stored, tc.value)
					}
					for _, reader := range []struct {
						locale i18n.Locale
						label  string
					}{{i18n.ZhHant, tc.zh}, {i18n.En, tc.en}} {
						readCtx := i18n.WithLocale(t.Context(), reader.locale)
						view, row := contactSubjectRow(t, readCtx, desk, email)
						want := []string{"Reader 原文", email, reader.label, "GO-261005-123456", "Customer note: 訂單問題。"}
						got := []string{row.Name, row.Email, row.SubjectLabel, row.OrderRef, row.Message}
						if diff := cmp.Diff(want, got); diff != "" {
							t.Errorf("Messages(%s) (-want +got):\n%s", reader.locale.Tag(), diff)
						}
						view.Rows = []admin.Message{row}
						var rendered bytes.Buffer
						if err := admin.Messages(layouts.Page{}, view).Render(readCtx, &rendered); err != nil {
							t.Fatalf("render real message view: %v", err)
						}
						if got := contactSubjectHeading(t, rendered.String()); got != reader.label {
							t.Errorf("rendered subject (%s) = %q, want %q", reader.locale.Tag(), got, reader.label)
						}
					}
				})
			}

			t.Run("unknown subject", func(t *testing.T) {
				email := "contact-unknown-" + uuid.NewString() + "@example.com"
				form := url.Values{"name": {"Reader"}, "email": {email}, "subject": {"An order"}, "message": {"An original customer note."}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/contact", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				h.Submit(res, req)
				if res.Code != http.StatusUnprocessableEntity {
					t.Errorf("Submit(unknown subject) = %d, want 422", res.Code)
				}
				var count int
				if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM contact_messages WHERE email=$1`, email).Scan(&count); err != nil || count != 0 {
					t.Errorf("unknown subject stored rows = %d, %v, want 0, nil", count, err)
				}
				if err := writer.Create(ctx, contact.Message{
					Name: "Reader", Email: email, Subject: contactsubject.Subject("An order"), Body: "An original customer note.",
				}); err == nil {
					t.Error("Create(unknown subject) = nil, want refusal")
				} else if _, driverError := errors.AsType[*pgconn.PgError](err); driverError {
					t.Errorf("Create(unknown subject) reached the database: %v", err)
				}
			})
		})
	}
}

func contactSubjectStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open store-role contact pool: %v", err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("contact writer role = %q, %v, want store, nil", role, err)
	}
	return p
}

func contactSubjectRow(t *testing.T, ctx context.Context, desk *feedback.Store, email string) (admin.MessagesView, admin.Message) {
	t.Helper()
	after := ""
	for {
		view, err := desk.Messages(ctx, after)
		if err != nil {
			t.Fatalf("read real contact inbox: %v", err)
		}
		for i := range view.Rows {
			row := &view.Rows[i]
			if row.Email == email {
				return view, *row
			}
		}
		if view.Next == "" {
			t.Fatalf("Messages() omitted the stored contact message %q", email)
		}
		next, err := url.Parse(view.Next)
		if err != nil {
			t.Fatalf("parse inbox cursor: %v", err)
		}
		after = next.Query().Get(web.KeysetParam)
	}
}

func contactSubjectPicker(t *testing.T, markup string) []pages.ContactSubject {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse contact picker: %v", err)
	}
	var choices []pages.ContactSubject
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != "select" || contactSubjectAttr(node, "name") != "subject" {
			continue
		}
		for option := range node.Descendants() {
			if option.Type == html.ElementNode && option.Data == "option" && contactSubjectAttr(option, "value") != "" {
				choices = append(choices, pages.ContactSubject{Value: contactSubjectAttr(option, "value"), Label: contactSubjectText(option)})
			}
		}
	}
	return choices
}

func contactSubjectHeading(t *testing.T, markup string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse contact message heading: %v", err)
	}
	for node := range doc.Descendants() {
		if node.Type == html.ElementNode && contactSubjectAttr(node, "class") == "goen-modqueue__title" {
			return contactSubjectText(node)
		}
	}
	t.Fatal("rendered message has no subject heading")
	return ""
}

func contactSubjectAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func contactSubjectText(node *html.Node) string {
	var text strings.Builder
	for child := range node.Descendants() {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return text.String()
}
