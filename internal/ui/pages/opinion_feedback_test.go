package pages

import (
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestEmptyReviewsExplainWhoCanReviewWithoutAnInvitationToEveryone(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, standing := range []ReviewStanding{ReviewSignedOut, ReviewNotDelivered, ReviewOpen} {
			t.Run(locale.Tag()+"/"+strconv.Itoa(int(standing)), func(t *testing.T) {
				t.Parallel()
				body := renderComponent(t, i18n.WithLocale(t.Context(), locale), productReviews(&ProductView{Slug: "sample", ReviewStanding: standing}))
				want := "No reviews yet. Once an order with this product is delivered, its buyer can review it here."
				if locale == i18n.ZhHant {
					want = "還沒有評價。買過這個商品、訂單送達後，就可以在這裡評價。"
				}
				if !strings.Contains(body, want) {
					t.Errorf("empty reviews omit the delivered-buyer rule %q", want)
				}
				for _, invitation := range []string{"你可以是第一個", "You could be the first", "後可以留下評價", " to leave a review."} {
					if strings.Contains(body, invitation) {
						t.Errorf("empty reviews invite an ineligible reader: %q", invitation)
					}
				}
			})
		}
	}
}

func TestUndeliveredReviewRefusalKeepsTheDraftAndItsReasonInTheForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			reason := "You can review this after your order is delivered. What you wrote was not posted."
			if locale == i18n.ZhHant {
				reason = "訂單送達後才能評價，你寫的內容沒有送出。"
			}
			v := &ProductView{Slug: "sample", SignedIn: true, ReviewStanding: ReviewNotDelivered,
				ReviewErrors: map[string]string{"body": reason}, ReviewDraft: ReviewDraft{Rating: 4, Title: "My <& title", Body: "My <& draft is still here"}}
			doc := opinionDocument(t, renderComponent(t, ctx, reviewForm(v)))
			form := opinionElement(doc, "form", "id", "write-review")
			if form == nil || attrValue(form, "method") != "post" || attrValue(form, "action") != "/p/sample/reviews#write-review" {
				t.Fatal("undelivered refusal loses the native review form")
			}
			title := opinionElement(form, "input", "id", "review-title")
			body := opinionElement(form, "textarea", "id", "review-body")
			if title == nil || attrValue(title, "value") != v.ReviewDraft.Title || body == nil || nodeText(body) != v.ReviewDraft.Body || attrValue(body, "aria-invalid") != "true" {
				t.Error("undelivered refusal loses the draft or body refusal")
			}
			rating := findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "rating" && attrValue(n, "value") == "4"
			})
			var checked bool
			if rating != nil {
				for _, attribute := range rating.Attr {
					if attribute.Key == "checked" {
						checked = true
					}
				}
			}
			if !checked {
				t.Error("undelivered refusal loses the selected rating")
			}
			errorText := opinionElement(form, "p", "id", "review-body-error")
			if errorText == nil || nodeText(errorText) != reason || body != nil && attrValue(body, "aria-describedby") != "review-body-error" {
				t.Error("review body is not associated with its delivery refusal")
			}
			if opinionElement(form, "p", "id", "review-body-hint") != nil {
				t.Error("review repeats its ordinary hint beside a refusal")
			}
		})
	}
}

func TestUndeliveredReviewWithATitleRefusalKeepsItsForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			v := &ProductView{Slug: "sample", SignedIn: true, ReviewStanding: ReviewNotDelivered, ReviewErrors: map[string]string{"title": i18n.T(ctx, i18n.KeyReviewTitleTooLong)}, ReviewDraft: ReviewDraft{Rating: 4, Title: strings.Repeat("x", 81), Body: "  My review draft  "}}
			doc := opinionDocument(t, renderComponent(t, ctx, reviewForm(v)))
			form := opinionElement(doc, "form", "id", "write-review")
			if form == nil {
				t.Fatal("a title-only refusal hides the undelivered buyer's review form")
			}
			title := opinionElement(form, "input", "id", "review-title")
			body := opinionElement(form, "textarea", "id", "review-body")
			if title == nil || attrValue(title, "value") != v.ReviewDraft.Title || attrValue(title, "aria-invalid") != "true" || body == nil || nodeText(body) != v.ReviewDraft.Body {
				t.Error("title-only refusal loses the raw draft or invalid field")
			}
			if opinionElement(form, "p", "id", "review-title-error") == nil {
				t.Error("title-only refusal loses its explanation")
			}
		})
	}
}

func TestQuestionFeedbackStaysAtTheQuestionAndKeepsItsLengthHint(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, refused := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+strconv.FormatBool(refused), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				v := &ProductView{Slug: "sample", SignedIn: true, AskDraft: "My <& question"}
				if refused {
					v.AskOutcome = "bad"
				}
				doc := opinionDocument(t, renderComponent(t, ctx, askForm(v)))
				form := opinionElement(doc, "form", "method", "post")
				if form == nil || attrValue(form, "action") != "/p/sample/questions#questions" {
					t.Fatal("question POST does not return to its section")
				}
				field := opinionElement(form, "textarea", "id", "ask-body")
				if field == nil || nodeText(field) != v.AskDraft || attrValue(field, "maxlength") != "300" {
					t.Fatal("question form loses the draft or length limit")
				}
				length, example, reason := "Up to 300 characters", "For example: size, materials or how to use it", "Write your question first."
				if locale == i18n.ZhHant {
					length, example, reason = "最多 300 字", "例如：尺寸、材質或使用方式", "請先寫下你的問題。"
				}
				hint := opinionElement(form, "p", "id", "ask-body-hint")
				if hint == nil || nodeText(hint) != length || attrValue(field, "placeholder") != example {
					t.Error("question form omits its generic example or pre-submit length hint")
				}
				if !strings.Contains(" "+attrValue(field, "aria-describedby")+" ", " ask-body-hint ") {
					t.Error("question length hint is not associated with the field before submission")
				}
				for _, description := range strings.Fields(attrValue(field, "aria-describedby")) {
					if findDescendant(form, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == description }) == nil {
						t.Errorf("question description %s has no text", description)
					}
				}
				if refused {
					errorText := opinionElement(form, "p", "id", "ask-body-error")
					if errorText == nil || nodeText(errorText) != reason || attrValue(field, "aria-invalid") != "true" {
						t.Error("question refusal does not ask for the missing question")
					}
				}
			})
		}
	}
}

func TestPostedReviewShowsThanksAndMarksOnlyTheReadersReview(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			v := &ProductView{SignedIn: true, ReviewStanding: ReviewAlreadyWritten, ReviewPosted: true, Rating: 4, RatingCount: 2,
				Reviews: []ProductReview{{Rating: 4, Author: "Same name", Body: "Mine", Own: true}, {Rating: 4, Author: "Same name", Body: "Another reader"}}}
			doc := opinionDocument(t, renderComponent(t, ctx, productReviews(v)))
			if strings.Count(nodeText(doc), i18n.T(ctx, i18n.KeyReviewThanks)) != 1 || strings.Contains(nodeText(doc), i18n.T(ctx, i18n.KeyAlreadyReviewed)) {
				t.Error("posted review repeats its one-review limit beside the thanks")
			}
			var rows int
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "article" {
					rows++
					own := strings.Contains(nodeText(n), "Mine")
					if marked := strings.Contains(nodeText(n), i18n.T(ctx, i18n.KeyYourReview)); marked != own {
						t.Error("review ownership tag is missing or marks another reader")
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			if rows != 2 {
				t.Errorf("review rows = %d, want 2", rows)
			}
		})
	}
}

func TestSignedOutWishlistActionNamesTheWishlist(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			doc := opinionDocument(t, renderComponent(t, ctx, productLinks(&ProductView{Slug: "sample"})))
			want := "Sign in to add to your wishlist"
			if locale == i18n.ZhHant {
				want = "登入後加入願望清單"
			}
			if !strings.Contains(nodeText(doc), want) {
				t.Errorf("signed-out wishlist action omits %q", want)
			}
		})
	}
}

func opinionDocument(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func opinionElement(root *html.Node, tag, attribute, value string) *html.Node {
	return findDescendant(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag && attrValue(n, attribute) == value
	})
}
