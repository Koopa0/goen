package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"html"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/mail"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/assets"
)

// parts reads a rendered letter back the way a mail client does and returns
// its two parts decoded, holding the MIME structure on the way: one
// multipart/alternative, text then HTML, each UTF-8 in base64 lines no longer
// than RFC 2045 allows.
func parts(t *testing.T, wire []byte) (text, page string) {
	t.Helper()

	msg, err := mail.ReadMessage(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("the letter is not a mail message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (%v), want multipart/alternative with a boundary",
			msg.Header.Get("Content-Type"), err)
	}
	if got := msg.Header.Get("Content-Transfer-Encoding"); got != "7bit" {
		t.Errorf("the multipart body is declared %q, want 7bit", got)
	}

	r := multipart.NewReader(msg.Body, params["boundary"])
	decoded := make([]string, 0, 2)
	for _, want := range []string{"text/plain", "text/html"} {
		// NextRawPart, because NextPart decodes quoted-printable and deletes
		// the header that says so.
		p, err := r.NextRawPart()
		if err != nil {
			t.Fatalf("reading the %s part: %v", want, err)
		}
		got, ps, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil || got != want || ps["charset"] != "utf-8" {
			t.Fatalf("part Content-Type = %q, want %s; charset=utf-8", p.Header.Get("Content-Type"), want)
		}
		if cte := p.Header.Get("Content-Transfer-Encoding"); cte != "base64" {
			t.Errorf("the %s part is %q, want base64", want, cte)
		}
		raw, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read the %s part: %v", want, err)
		}
		for line := range strings.SplitSeq(string(raw), "\r\n") {
			if len(line) > 76 {
				t.Errorf("a base64 line of the %s part is %d characters, over 76", want, len(line))
			}
		}
		body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("the %s part is not base64: %v", want, err)
		}
		decoded = append(decoded, string(body))
	}
	if _, err := r.NextRawPart(); err != io.EOF {
		t.Errorf("after the HTML part: %v, want the closing boundary", err)
	}
	return decoded[0], decoded[1]
}

var (
	href = regexp.MustCompile(`href="([^"]*)"`)
	tag  = regexp.MustCompile(`<[^>]*>`)
)

// variants writes each letter one Send method chooses by its payload that
// letters() does not reach, with each free-text field set to text.
func variants(text string) map[string]func(context.Context, Notifier) error {
	const to, number = "someone@example.com", "GO-260101-000001"
	out := map[string]func(context.Context, Notifier) error{
		"SendOrderPlaced funded": func(ctx context.Context, n Notifier) error {
			owed := int64(0)
			return n.SendOrderPlaced(ctx, &OrderPlaced{
				Email: to, Name: text, OrderNumber: number, TotalCents: 123400, OwedCents: &owed,
			})
		},
		"SendOrderPaid no card": func(ctx context.Context, n Notifier) error {
			return n.SendOrderPaid(ctx, &OrderPaid{Email: to, Name: text, OrderNumber: number, AmountCents: 123400})
		},
		"SendOrderShipped pickup": func(ctx context.Context, n Notifier) error {
			return n.SendOrderShipped(ctx, &OrderShipped{
				Email: to, Name: text, OrderNumber: number, Carrier: text, Tracking: text, Pickup: true,
			})
		},
	}
	for _, m := range []OrderTerminal{
		{Kind: TerminalCancelledByCustomer},
		{Kind: TerminalCancelledByCustomer, Refunded: true},
		{Kind: TerminalCancelledByStaff},
		{Kind: TerminalCancelledByStaff, Refunded: true},
		{Kind: TerminalCancelledByPaymentDeadline},
		{Kind: TerminalCancelledByPaymentDeadline, Refunded: true},
		{Kind: TerminalCollected},
	} {
		out["SendOrderTerminal "+string(m.Kind)+" refunded="+strconv.FormatBool(m.Refunded)] =
			func(ctx context.Context, n Notifier) error {
				m.OrderID = uuid.New()
				return n.SendOrderTerminal(ctx, &m, TerminalRecipient{Address: to, Name: text, OrderNumber: number})
			}
	}
	return out
}

// lines is the text of a letter line by line, without its blank lines. For
// an HTML part it is what a reader sees: the markup gone, a break or a
// paragraph's end a line break, and the entities read back.
func lines(s string, page bool) []string {
	if page {
		s = strings.NewReplacer("<br>", "\n", "</p>", "\n").Replace(s)
		s = html.UnescapeString(tag.ReplaceAllString(s, ""))
	}
	var out []string
	for line := range strings.SplitSeq(crlf(s), "\r\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestEveryLetterIsSentAsTextAndHTML holds every letter goen writes to its two
// parts: the text part exactly the text goen composed, and an HTML part that
// reads the same, line for line — the statutory disclosure included — with the
// header, every link the text has, and nothing a payload's free text can turn
// into markup or into a link.
func TestEveryLetterIsSentAsTextAndHTML(t *testing.T) {
	t.Parallel()
	const origin = "https://goen.test"
	header := `src="` + origin + assets.URL(assets.EmailHeader) + `"`
	// A name, a carrier or a product name is somebody else's text: markup in it
	// stays text, and a URL in it stays unlinked, even one that begins the way
	// goen's own do.
	const hostile = `<script>alert(1)</script> https://goen.test.evil.example/x`

	for _, text := range []string{"Alex", hostile} {
		all := letters(text)
		maps.Copy(all, variants(text))
		for letter, send := range all {
			t.Run(letter+"/"+text[:4], func(t *testing.T) {
				t.Parallel()
				sink := &recorded{}
				n := New(sink, origin+"/", "goen Co., Ltd.", "support@goen.example")
				if err := send(t.Context(), n); err != nil {
					t.Fatalf("send: %v", err)
				}
				if len(sink.msgs) != 1 {
					t.Fatalf("%d messages sent, want 1", len(sink.msgs))
				}
				m := sink.msgs[0]
				plain, page := parts(t, render("goen <no-reply@goen.example>", m))

				if plain != crlf(m.Body) {
					t.Errorf("the text part is not the letter goen wrote:\n got %q\nwant %q", plain, crlf(m.Body))
				}
				if got, want := lines(page, true), lines(plain, false); !slices.Equal(got, want) {
					t.Errorf("the HTML part does not read as the text part does:\n got %q\nwant %q", got, want)
				}
				if !strings.Contains(page, header) {
					t.Errorf("the HTML part has no header image at %s:\n%s", header, page)
				}
				ours := 0
				for _, link := range mailedLink.FindAllString(plain, -1) {
					if !strings.HasPrefix(link, origin+"/") {
						continue
					}
					ours++
					if !strings.Contains(page, `href="`+html.EscapeString(link)+`"`) {
						t.Errorf("the text links to %s and the HTML part does not:\n%s", link, page)
					}
				}
				if ours == 0 {
					t.Errorf("the %s letter carries no link of goen's; the link check proved nothing", letter)
				}
				for _, match := range href.FindAllStringSubmatch(page, -1) {
					if to := html.UnescapeString(match[1]); !strings.HasPrefix(to, origin+"/") {
						t.Errorf("the HTML part links to %q, outside %s", to, origin)
					}
				}
				if strings.Contains(page, "<script") {
					t.Errorf("free text reached the HTML part as markup:\n%s", page)
				}
				if strings.Contains(m.Body, "<script>") &&
					!strings.Contains(page, html.EscapeString("<script>alert(1)</script>")) {
					t.Errorf("the text carries the payload and the HTML part does not "+
						"show it as text:\n%s", page)
				}
			})
		}
	}
}

// TestTheHTMLPartIsInTheLettersLanguage: lang decides the voice a screen
// reader reads the letter in.
func TestTheHTMLPartIsInTheLettersLanguage(t *testing.T) {
	t.Parallel()
	for locale, want := range map[string]string{"en": `<html lang="en">`, "zh-Hant": `<html lang="zh-Hant">`} {
		n, sink := notifier(t)
		if err := n.SendPasswordReset(t.Context(), &PasswordReset{Locale: locale, Email: "a@b.co", Token: "tok"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		if !strings.Contains(sink.msg.HTML, want) {
			t.Errorf("the %s letter's HTML part does not open %s:\n%s", locale, want, sink.msg.HTML)
		}
	}
}

// TestParagraphsFollowTheText: a blank line ends a paragraph, a single break
// stays a break, and only goen's own URLs become links — without the sentence
// punctuation around them.
func TestParagraphsFollowTheText(t *testing.T) {
	t.Parallel()
	const origin = "https://goen.test"
	text := "Hello Alex,\r\n\r\n查看訂單：https://goen.test/orders/1\nline two https://goen.test/p/a.\r\r\n\n" +
		"see https://goen.test.evil.example/x and https://elsewhere.example/\n— goen\n"

	want := []paragraph{
		{{{text: "Hello Alex,"}}},
		{
			{{text: "查看訂單："}, {text: "https://goen.test/orders/1", link: true}},
			{{text: "line two "}, {text: "https://goen.test/p/a", link: true}, {text: "."}},
		},
		{
			{{text: "see https://goen.test.evil.example/x and https://elsewhere.example/"}},
			{{text: "— goen"}},
		},
	}
	if got := paragraphs(text, origin); !reflect.DeepEqual(got, want) {
		t.Errorf("paragraphs =\n%#v\nwant\n%#v", got, want)
	}
}

// TestAMultipartLetterEndsEveryLineInCRLF is TestEveryLineOfAMessageEndsInCRLF
// for the multipart form, which is the one every Notifier letter takes.
func TestAMultipartLetterEndsEveryLineInCRLF(t *testing.T) {
	t.Parallel()

	body := "first\rsecond\r\nthird\nfourth\r\r\nfifth\n\rsixth\r"
	wire := string(render("goen <no-reply@goen.example>", &Message{
		To: "a@b.co", Subject: "hi", Body: body, HTML: "<p>first</p>\n<p>second</p>",
	}))

	for i := range len(wire) {
		switch wire[i] {
		case '\r':
			if i+1 >= len(wire) || wire[i+1] != '\n' {
				t.Fatalf("a CR at byte %d is not followed by LF:\n%q", i, wire)
			}
		case '\n':
			if i == 0 || wire[i-1] != '\r' {
				t.Fatalf("an LF at byte %d is not preceded by CR:\n%q", i, wire)
			}
		}
	}
	plain, _ := parts(t, []byte(wire))
	want := "first\r\nsecond\r\nthird\r\nfourth\r\n\r\nfifth\r\n\r\nsixth\r\n"
	if plain != want {
		t.Errorf("the text part = %q, want %q: every break kept, each one CRLF", plain, want)
	}
}
