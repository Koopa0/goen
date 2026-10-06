package returnpage

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"

	"github.com/google/uuid"
)

// TestValidateAcceptsABlankReason holds Consumer Protection Act §19 I at
// the form gate: a blank reason is legal, and the policy page says so.
func TestValidateAcceptsABlankReason(t *testing.T) {
	t.Parallel()

	for _, reason := range []string{"", "   \t "} {
		req := Request{
			Reason: reason,
			Lines:  map[string]int32{"line": 1},
		}
		if err := req.Validate(); err != nil {
			t.Errorf("reason %q was refused: %v", reason, err)
		}
	}
}

func TestParseWantedDistinguishesQuantityFromMalformedInput(t *testing.T) {
	lineID := uuid.New()
	allowed := map[string]int32{lineID.String(): 1}

	if _, err := parseWanted(lineID.String(), 2, allowed); !errors.Is(err, ErrTooMany) ||
		errors.Is(err, ErrInvalid) {
		t.Fatalf("quantity 2 with ceiling 1 = %v, want only ErrTooMany", err)
	}
	if _, err := parseWanted(uuid.NewString(), 1, allowed); !errors.Is(err, ErrInvalid) ||
		errors.Is(err, ErrTooMany) {
		t.Fatalf("unknown line = %v, want only ErrInvalid", err)
	}
}

func TestReturnQuantityRefusalsKeepCompleteDrafts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, first, reason, message string
		firstMax                     int32
		invalid                      bool
		inputType                    string
	}{
		{name: "malformed", first: "not a number", reason: "Keep this reason", firstMax: 1, invalid: true, inputType: "text", message: "Enter a whole number of zero or more."},
		{name: "negative", first: "-1", reason: "Keep this reason", firstMax: 1, invalid: true, inputType: "number", message: "Enter a whole number of zero or more."},
		{name: "overflow", first: "2147483648", reason: "Keep this reason", firstMax: 1, invalid: true, inputType: "text", message: "Enter a whole number of zero or more."},
		{name: "stale ceiling", first: "2", reason: "Keep this reason", firstMax: 1, invalid: true, inputType: "number", message: "That is more than can be returned."},
		{name: "no longer returnable", first: "1", reason: "Keep this reason", firstMax: 0, invalid: true, inputType: "number", message: "That is more than can be returned."},
		{name: "explicit positive sign", first: "+1", reason: "", firstMax: 1, inputType: "text"},
		{name: "valid optional reason", first: "1", reason: "", firstMax: 1, inputType: "number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			order := &Order{Number: "GO-RETURN", Lines: []Line{{ID: "first", Returnable: tt.firstMax}, {ID: "later", Returnable: 3}}}
			form := url.Values{"qty_first": {tt.first}, "qty_later": {"02"}, "reason": {tt.reason}}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			draft := returnDraftFromForm(r, order)
			_, refusals := draft.request(order)
			var body bytes.Buffer
			if err := pages.Returns(pages.ReturnsMeta(ctx, order.Number), viewOf(ctx, order, draft, refusals)).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			first := returnControl(t, doc, "qty_first")
			want := map[string]string{"type": tt.inputType, "value": tt.first, "min": "0", "max": map[int32]string{0: "0", 1: "1"}[tt.firstMax], "step": "1", "inputmode": "numeric"}
			if tt.invalid {
				want["aria-invalid"] = "true"
				want["aria-describedby"] = "qty_first-error"
			}
			got := returnAttributes(first, "type", "value", "min", "max", "step", "inputmode", "aria-invalid", "aria-describedby")
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("refused quantity mismatch (-want +got):\n%s", diff)
			}
			later := returnControl(t, doc, "qty_later")
			if diff := cmp.Diff(map[string]string{"value": "02"}, returnAttributes(later, "value", "aria-invalid", "aria-describedby")); diff != "" {
				t.Errorf("later quantity mismatch (-want +got):\n%s", diff)
			}
			reason := returnControl(t, doc, "reason")
			if (reason.FirstChild == nil && tt.reason != "") || (reason.FirstChild != nil && reason.FirstChild.Data != tt.reason) {
				t.Errorf("reason did not retain %q", tt.reason)
			}
			if got := returnAttributes(reason, "aria-invalid", "aria-describedby"); len(got) > 0 {
				t.Errorf("valid optional reason marked invalid: %v", got)
			}
			if tt.message != "" && !strings.Contains(body.String(), tt.message) {
				t.Errorf("refusal missing %q", tt.message)
			}
		})
	}
}

func TestReturnRefusalsDistinguishReasonFromEmptySelection(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		lengthMessage, controlMessage := returnReasonMessages(locale)
		for _, tt := range []struct {
			name, quantity, reason, message string
		}{
			{name: "empty selection", quantity: "0", reason: "Optional and valid"},
			{name: "ordinary overlong", quantity: "1", reason: "  " + strings.Repeat("界", 501) + "  ", message: lengthMessage},
			{name: "unsupported control", quantity: "1", reason: "  bad\x01reason  ", message: controlMessage},
			{name: "both failures prefer length", quantity: "1", reason: strings.Repeat("x", 500) + "\x01", message: lengthMessage},
			{name: "blank optional reason", quantity: "1"},
			{name: "valid reason", quantity: "1", reason: "  optional\nreason\twith\rspacing  "},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				assertReturnReasonRendering(t, locale, tt.quantity, tt.reason, tt.message)
			})
		}
	}
}

func returnReasonMessages(locale i18n.Locale) (lengthMessage, controlMessage string) {
	if locale == i18n.ZhHant {
		return "退貨原因最多 500 字。", "請移除退貨原因中不支援的控制字元。"
	}
	return "Keep the optional reason within 500 characters.", "Remove unsupported control characters from the optional reason."
}

func assertReturnReasonRendering(t *testing.T, locale i18n.Locale, quantity, raw, message string) {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), locale)
	order := &Order{Number: "GO-RETURN", Lines: []Line{{ID: "first", Returnable: 2}, {ID: "later", Returnable: 3}}}
	form := url.Values{"qty_first": {quantity}, "qty_later": {"0"}, "reason": {raw}}
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	draft := returnDraftFromForm(r, order)
	_, refusals := draft.request(order)
	var body bytes.Buffer
	if err := pages.Returns(pages.ReturnsMeta(ctx, order.Number), viewOf(ctx, order, draft, refusals)).Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	assertReturnReasonDraft(t, doc, body.String(), raw, message)
	for field, value := range map[string]string{"qty_first": quantity, "qty_later": "0"} {
		control := returnControl(t, doc, field)
		if diff := cmp.Diff(map[string]string{"value": value}, returnAttributes(control, "value", "aria-invalid", "aria-describedby")); diff != "" {
			t.Errorf("%s draft/refusal (-want +got):\n%s", field, diff)
		}
	}
	lengthMessage, controlMessage := returnReasonMessages(locale)
	for _, candidate := range []string{lengthMessage, controlMessage} {
		if candidate != message && strings.Contains(body.String(), candidate) {
			t.Errorf("unrelated refusal %q", candidate)
		}
	}
	formMessage := "Choose at least one item and check the entered details."
	if locale == i18n.ZhHant {
		formMessage = "請至少選擇一件商品，並確認填寫的資料。"
	}
	if quantity == "0" && !strings.Contains(body.String(), formMessage) {
		t.Errorf("missing empty-selection refusal %q", formMessage)
	}
}

func assertReturnReasonDraft(t *testing.T, doc *html.Node, body, raw, message string) {
	t.Helper()
	reason := returnControl(t, doc, "reason")
	want := map[string]string{}
	if message != "" {
		want["aria-invalid"], want["aria-describedby"] = "true", "return-reason-error"
	}
	if diff := cmp.Diff(want, returnAttributes(reason, "aria-invalid", "aria-describedby")); diff != "" {
		t.Errorf("reason accessibility (-want +got):\n%s", diff)
	}
	domReason := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	got := ""
	if reason.FirstChild != nil {
		got = reason.FirstChild.Data
	}
	if got != domReason {
		t.Errorf("reason draft=%q, want %q", got, domReason)
	}
	if raw != "" && !strings.Contains(body, raw) {
		t.Errorf("response lost raw reason %q", raw)
	}
	assertReturnReasonErrorText(t, doc, message)
}

func assertReturnReasonErrorText(t *testing.T, doc *html.Node, want string) {
	t.Helper()
	count, text := 0, ""
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		for _, attr := range n.Attr {
			if attr.Key == "id" && attr.Val == "return-reason-error" {
				count++
				if n.FirstChild != nil {
					text = n.FirstChild.Data
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	wantCount := 0
	if want != "" {
		wantCount = 1
	}
	if count != wantCount || text != want {
		t.Errorf("reason error=%d/%q, want %d/%q", count, text, wantCount, want)
	}
}

func TestReturnReasonBoundaryKeepsValidationPolicy(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, reason string
		invalid      bool
	}{
		{name: "blank"},
		{name: "whitespace blank", reason: " \n\t\r "},
		{name: "500 multibyte runes", reason: strings.Repeat("界", 500)},
		{name: "trimmed boundary", reason: " \t" + strings.Repeat("界", 500) + "\r\n "},
		{name: "allowed internal controls", reason: strings.Repeat("界", 496) + "\n\t\rx"},
		{name: "501 multibyte runes", reason: strings.Repeat("界", 501), invalid: true},
		{name: "forbidden control", reason: "bad\x01reason", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := Request{Reason: tt.reason, Lines: map[string]int32{"line": 1}}
			err := req.Validate()
			if tt.invalid && !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate(%q)=%v, want ErrInvalid", tt.reason, err)
			}
			if !tt.invalid && err != nil {
				t.Errorf("Validate(%q)=%v, want nil", tt.reason, err)
			}
			if req.Reason != strings.TrimSpace(tt.reason) {
				t.Errorf("normalized reason=%q, want trimmed draft", req.Reason)
			}
		})
	}
}

func returnControl(t *testing.T, doc *html.Node, name string) *html.Node {
	t.Helper()
	var found *html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "textarea") {
			for _, a := range n.Attr {
				if a.Key == "name" && a.Val == name {
					if found != nil {
						t.Errorf("duplicate control %s", name)
					}
					found = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if found == nil {
		t.Fatalf("missing control %s", name)
	}
	id := returnAttributes(found, "aria-describedby")["aria-describedby"]
	if id == "" {
		return found
	}
	count := 0
	var targets func(*html.Node)
	targets = func(n *html.Node) {
		if returnAttributes(n, "id")["id"] == id {
			count++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			targets(c)
		}
	}
	targets(doc)
	if count != 1 {
		t.Errorf("%s error target %q count=%d, want 1", name, id, count)
	}

	return found
}

func returnAttributes(n *html.Node, keys ...string) map[string]string {
	out := map[string]string{}
	for _, a := range n.Attr {
		for _, key := range keys {
			if a.Key == key {
				out[key] = a.Val
			}
		}
	}
	return out
}

func TestReturnDraftRemainsVisibleAfterAvailabilityIsLost(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	original := &Order{Number: "GO-RETURN", Lines: []Line{{ID: "first", Returnable: 3}, {ID: "later", Returnable: 3}}}
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(url.Values{"qty_first": {"1"}, "qty_later": {"2"}, "reason": {"Keep this reason"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	draft := returnDraftFromForm(r, original)
	tests := []struct {
		name    string
		open    bool
		refusal i18n.Key
	}{
		{name: "all quantities exhausted", refusal: i18n.KeyReturnNothingShort},
		{name: "another request remains open", open: true, refusal: i18n.KeyReturnAlreadyOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fresh := &Order{Number: "GO-RETURN", HasOpen: tt.open, Lines: []Line{{ID: "first"}, {ID: "later"}}}
			refusals := returnRefusals(fresh, draft, []web.FieldRefusal{{MessageKey: tt.refusal}})
			var body bytes.Buffer
			if err := pages.Returns(pages.ReturnsMeta(ctx, fresh.Number), viewOf(ctx, fresh, draft, refusals)).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			for i, field := range []string{"qty_first", "qty_later"} {
				got := returnAttributes(returnControl(t, doc, field), "value", "max", "aria-invalid", "aria-describedby")
				want := map[string]string{"value": []string{"1", "2"}[i], "max": "0"}
				if !tt.open {
					want["aria-invalid"] = "true"
					want["aria-describedby"] = field + "-error"
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("lost availability draft mismatch (-want +got):\n%s", diff)
				}
			}
			var disabled bool
			var visit func(*html.Node, bool)
			visit = func(n *html.Node, inForm bool) {
				if n.Type == html.ElementNode && n.Data == "form" {
					inForm = returnAttributes(n, "action")["action"] == "/orders/GO-RETURN/return"
				}
				if inForm && n.Type == html.ElementNode && n.Data == "button" {
					attrs := returnAttributes(n, "type", "disabled")
					if attrs["type"] == "submit" {
						_, disabled = attrs["disabled"]
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					visit(c, inForm)
				}
			}
			visit(doc, false)
			if !disabled {
				t.Error("submission remains enabled after all returnable quantities are exhausted")
			}
		})
	}
}

func FuzzReturnDraftCapturesLaterSelections(f *testing.F) {
	for _, raw := range []string{"", "0", "-1", "not a number", "2147483648", "2", "+1"} {
		f.Add(raw, "Optional reason")
	}
	f.Fuzz(func(t *testing.T, raw, reason string) {
		order := &Order{Lines: []Line{{ID: "first", Returnable: 1}, {ID: "later", Returnable: 3}}}
		form := url.Values{"qty_first": {raw}, "qty_later": {"02"}, "reason": {reason}}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		draft := returnDraftFromForm(r, order)
		if diff := cmp.Diff(&returnDraft{Reason: reason, Quantities: map[string]string{"first": raw, "later": "02"}}, draft); diff != "" {
			t.Errorf("captured form mismatch (-want +got):\n%s", diff)
		}
		draft.request(order)
	})
}
