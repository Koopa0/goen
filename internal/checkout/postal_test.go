package checkout

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestPostalMismatchNote holds that a postcode whose districts are not the city and
// district typed with it is named with them, as the shopper typed it, and that the note
// stays silent wherever the table cannot tell: 台 and 臺 are one spelling, a blank city or
// district, a postcode shorter than a prefix and a prefix the table does not hold.
func TestPostalMismatchNote(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		addr Address
		zh   string
		en   string
	}{
		"another county": {
			addr: Address{PostalCode: "880", City: "台北市", District: "大安區"},
			zh:   "880 是澎湖縣馬公市，與台北市大安區不符。",
			en:   "Postcode 880 is in 澎湖縣馬公市, which does not match 台北市大安區.",
		},
		"another district of the city": {
			addr: Address{PostalCode: "10663", City: "臺北市", District: "信義區"},
			zh:   "106 是臺北市大安區，與臺北市信義區不符。",
			en:   "Postcode 106 is in 臺北市大安區, which does not match 臺北市信義區.",
		},
		"two districts share the prefix": {
			addr: Address{PostalCode: "600", City: "台北市", District: "大安區"},
			zh:   "600 是嘉義市西區、嘉義市東區，與台北市大安區不符。",
			en:   "Postcode 600 is in 嘉義市西區 and 嘉義市東區, which does not match 台北市大安區.",
		},
		"typed with 台":       {addr: Address{PostalCode: "106", City: "台北市", District: "大安區"}},
		"typed with 臺":       {addr: Address{PostalCode: "106", City: "臺北市", District: "大安區"}},
		"a longer postcode":  {addr: Address{PostalCode: "106615", City: "台北市", District: "大安區"}},
		"the second of two":  {addr: Address{PostalCode: "600", City: "嘉義市", District: "東區"}},
		"no district typed":  {addr: Address{PostalCode: "880", City: "台北市"}},
		"no city typed":      {addr: Address{PostalCode: "880", District: "大安區"}},
		"a short postcode":   {addr: Address{PostalCode: "88", City: "台北市", District: "大安區"}},
		"an unknown prefix":  {addr: Address{PostalCode: "999", City: "台北市", District: "大安區"}},
		"no postcode typed":  {addr: Address{City: "台北市", District: "大安區"}},
		"letters for digits": {addr: Address{PostalCode: "abc", City: "台北市", District: "大安區"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for locale, want := range map[i18n.Locale]string{i18n.ZhHant: tt.zh, i18n.En: tt.en} {
				if got := postalMismatchNote(i18n.WithLocale(t.Context(), locale), tt.addr); got != want {
					t.Errorf("postalMismatchNote(%+v) in %s = %q, want %q", tt.addr, locale.Tag(), got, want)
				}
			}
		})
	}
}

// TestPagePostalNote holds that the note sits under the postcode and is read with it, after
// the sentence that field already has, and that a page without a note describes the field
// exactly as before.
func TestPagePostalNote(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, tt := range map[string]struct {
		note   string
		errs   map[string]string
		wantBy string
	}{
		"no note":              {wantBy: "postal_code-error"},
		"a note":               {note: "880 是澎湖縣馬公市，與台北市大安區不符。", wantBy: "postal_code-error postal_code-note"},
		"a note and a refusal": {note: "880 是澎湖縣馬公市，與台北市大安區不符。", errs: map[string]string{"postal_code": "x"}, wantBy: "postal_code-error postal_code-note"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			view := couponTestView()
			view.PostalNote = tt.note
			view.Errors = tt.errs
			doc, err := html.Parse(strings.NewReader(renderToString(t, Page(Meta(ctx), &view))))
			if err != nil {
				t.Fatal(err)
			}
			input := nodeByID(doc, "postal_code")
			if input == nil {
				t.Fatal("the page renders no postcode field")
			}
			if got := attrValue(input, "aria-describedby"); got != tt.wantBy {
				t.Errorf("the postcode is described by %q, want %q", got, tt.wantBy)
			}
			note := nodeByID(doc, "postal_code-note")
			if (note != nil) != (tt.note != "") {
				t.Fatalf("note present = %v, want %v", note != nil, tt.note != "")
			}
			if note == nil {
				return
			}
			if got := nodeText(note); got != tt.note {
				t.Errorf("the note reads %q, want %q", got, tt.note)
			}
			if input.Parent != note.Parent {
				t.Error("the note is not in the postcode's own field")
			}
			if hasClass(note, "ui-error-text") || attrValue(note, "role") != "" {
				t.Error("the note is dressed as a refusal, and refuses nothing")
			}
		})
	}
}

// TestHandler_RenderCheckoutPostalNote holds that renderCheckout adds the note to a 422 and to
// nothing else: a form that is merely re-shown, or is shown for the first time, has not been
// refused, so a postcode the shopper has not finished with is not argued with.
func TestHandler_RenderCheckoutPostalNote(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for status, wantNote := range map[int]bool{
		http.StatusUnprocessableEntity: true,
		http.StatusOK:                  false,
	} {
		view := couponTestView()
		view.Address = Address{PostalCode: "880", City: "台北市", District: "大安區"}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", http.NoBody)
		res := httptest.NewRecorder()
		h.renderCheckout(res, req, status, &view)
		if got := strings.Contains(res.Body.String(), `id="postal_code-note"`); got != wantNote {
			t.Errorf("renderCheckout(%d) shows the postcode note = %v, want %v", status, got, wantNote)
		}
	}
}
