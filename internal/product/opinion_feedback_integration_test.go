//go:build integration

package product_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/user"
)

func TestUndeliveredReviewPOSTKeepsTheRawDraftAndExplainsTheRefusal(t *testing.T) {
	slug := activeSlug(t)
	for _, locale := range i18n.Locales() {
		for _, paid := range []bool{false, true} {
			name := "never bought"
			if paid {
				name = "paid, not delivered"
			}
			t.Run(locale.Tag()+"/"+name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				who := reviewer(t, "raw-refused-review")
				if paid {
					buy(t, who, slug)
				}
				app := opinionStorePool(t)
				h := product.NewHandler(product.NewStore(app, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
				title, draft := "  My <& title  ", "  My <& review stays here  \n"
				form := url.Values{"rating": {"4"}, "title": {title}, "body": {draft}}
				req := httptest.NewRequestWithContext(user.NewContext(ctx, user.User{ID: who.String()}), http.MethodPost, "/p/"+slug+"/reviews", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.SetPathValue("slug", slug)
				res := httptest.NewRecorder()
				h.Review(res, req)
				if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
					t.Fatalf("undelivered review POST = %d, Location %q; want 422 without redirect", res.Code, res.Header().Get("Location"))
				}
				doc := feedbackDocument(t, res.Body.String())
				titleInput, bodyInput := feedbackElement(doc, "review-title"), feedbackElement(doc, "review-body")
				if titleInput == nil || bodyInput == nil || feedbackAttribute(titleInput, "value") != title || feedbackText(bodyInput) != draft || feedbackAttribute(bodyInput, "aria-invalid") != "true" {
					t.Fatal("undelivered refusal loses raw title/body or field refusal")
				}
				reason := "You can review this after your order is delivered. What you wrote was not posted."
				if locale == i18n.ZhHant {
					reason = "訂單送達後才能評價，你寫的內容沒有送出。"
				}
				errorText := feedbackElement(doc, "review-body-error")
				if errorText == nil || feedbackText(errorText) != reason || feedbackAttribute(bodyInput, "aria-describedby") != "review-body-error" {
					t.Error("undelivered refusal has no linked delivery reason")
				}
				if feedbackElement(doc, "review-body-hint") != nil {
					t.Error("undelivered refusal repeats the ordinary body hint")
				}
				var rows int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_reviews WHERE user_id=$1`, who).Scan(&rows); err != nil {
					t.Fatal(err)
				}
				if rows != 0 {
					t.Errorf("undelivered review wrote %d rows, want zero", rows)
				}
			})
		}
	}
}

func TestReviewOwnershipTagUsesTheSignedInIdentityEvenWhenNamesMatch(t *testing.T) {
	slug := activeSlug(t)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			app := opinionStorePool(t)
			s := product.NewStore(app, slog.New(slog.DiscardHandler))
			h := product.NewHandler(s, slog.New(slog.DiscardHandler), "https://goen.example")
			first, second, outsider := reviewer(t, "own-review"), reviewer(t, "other-review"), reviewer(t, "outsider-review")
			firstBody, secondBody := "First review "+uuid.NewString(), "Second review "+uuid.NewString()
			for _, writer := range []struct {
				id   uuid.UUID
				body string
			}{{first, firstBody}, {second, secondBody}} {
				receive(t, writer.id, slug)
				form := url.Values{"rating": {"4"}, "body": {writer.body}}
				req := httptest.NewRequestWithContext(user.NewContext(ctx, user.User{ID: writer.id.String()}), http.MethodPost, "/p/"+slug+"/reviews", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.SetPathValue("slug", slug)
				res := httptest.NewRecorder()
				h.Review(res, req)
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/p/"+slug+"?reviewed=1#reviews" {
					t.Fatalf("delivered review POST = %d, Location %q; want existing 303", res.Code, res.Header().Get("Location"))
				}
			}
			for _, reader := range []struct {
				name    string
				id      uuid.UUID
				ownBody string
			}{
				{"first buyer", first, firstBody}, {"second buyer", second, secondBody}, {"another reader", outsider, ""}, {"signed out", uuid.Nil, ""},
			} {
				t.Run(reader.name, func(t *testing.T) {
					readCtx := ctx
					if reader.id != uuid.Nil {
						readCtx = user.NewContext(ctx, user.User{ID: reader.id.String()})
					}
					view, err := s.Load(readCtx, slug, product.Selection{})
					if err != nil {
						t.Fatal(err)
					}
					var marked int
					var firstSeen, secondSeen bool
					for _, review := range view.Reviews {
						firstSeen = firstSeen || review.Body == firstBody
						secondSeen = secondSeen || review.Body == secondBody
						if review.Own {
							marked++
							if review.Body != reader.ownBody {
								t.Error("identity projection marks another review as the reader's")
							}
						}
					}
					wantMarked := 0
					if reader.ownBody != "" {
						wantMarked = 1
					}
					if !firstSeen || !secondSeen || marked != wantMarked {
						t.Fatalf("ownership projection = %d tags with both rows %t/%t, want %d", marked, firstSeen, secondSeen, wantMarked)
					}
					req := httptest.NewRequestWithContext(readCtx, http.MethodGet, "/p/"+slug+"?reviewed=1", http.NoBody)
					req.SetPathValue("slug", slug)
					res := httptest.NewRecorder()
					h.Detail(res, req)
					if res.Code != http.StatusOK {
						t.Fatalf("review GET = %d, want 200", res.Code)
					}
					body := feedbackText(feedbackDocument(t, res.Body.String()))
					if got := strings.Count(body, i18n.T(ctx, i18n.KeyYourReview)); got != wantMarked {
						t.Errorf("rendered ownership tags = %d, want %d", got, wantMarked)
					}
					if strings.Count(body, i18n.T(ctx, i18n.KeyReviewThanks)) != 1 || strings.Contains(body, i18n.T(ctx, i18n.KeyAlreadyReviewed)) {
						t.Error("posted feedback repeats the per-product limit beside thanks")
					}
				})
			}
		})
	}
}

func TestQuestionRefusalPOSTKeepsTheDraftAndReturnsToItsSection(t *testing.T) {
	slug := activeSlug(t)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			who := reviewer(t, "empty-question")
			h := product.NewHandler(product.NewStore(opinionStorePool(t), slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
			draft := " \n "
			form := url.Values{"body": {draft}}
			req := httptest.NewRequestWithContext(user.NewContext(ctx, user.User{ID: who.String()}), http.MethodPost, "/p/"+slug+"/questions", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("slug", slug)
			res := httptest.NewRecorder()
			h.Ask(res, req)
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("empty question POST = %d, want 422", res.Code)
			}
			doc := feedbackDocument(t, res.Body.String())
			field := feedbackElement(doc, "ask-body")
			if field == nil || feedbackText(field) != draft || feedbackAttribute(field, "aria-invalid") != "true" {
				t.Fatal("question refusal loses the draft or field refusal")
			}
			reason, length := "Write your question first.", "Up to 300 characters"
			if locale == i18n.ZhHant {
				reason, length = "請先寫下你的問題。", "最多 300 字"
			}
			errorText, hint := feedbackElement(doc, "ask-body-error"), feedbackElement(doc, "ask-body-hint")
			if errorText == nil || feedbackText(errorText) != reason || hint == nil || feedbackText(hint) != length {
				t.Error("question refusal omits its missing-question reason or length hint")
			}
			questionForm := field.Parent
			for questionForm != nil && questionForm.Data != "form" {
				questionForm = questionForm.Parent
			}
			if questionForm == nil || feedbackAttribute(questionForm, "action") != "/p/"+slug+"/questions#questions" {
				t.Error("question refusal form does not return to its section")
			}
			var rows int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_questions WHERE user_id=$1`, who).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if rows != 0 {
				t.Errorf("empty question wrote %d rows, want zero", rows)
			}
		})
	}
}

func opinionStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, `SET ROLE store`)
		return roleErr
	}
	app, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func feedbackDocument(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func feedbackElement(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && feedbackAttribute(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := feedbackElement(child, id); found != nil {
			return found
		}
	}
	return nil
}

func feedbackAttribute(n *html.Node, name string) string {
	for _, attribute := range n.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func feedbackText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var text strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(feedbackText(child))
	}
	return text.String()
}
