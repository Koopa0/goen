package email

import (
	"context"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// recorded keeps every message a test sent.
type recorded struct{ msgs []*Message }

func (r *recorded) Send(_ context.Context, m *Message) error {
	r.msgs = append(r.msgs, m)
	return nil
}

// letters sends one of every letter goen writes, keyed by the Notifier method
// that writes it, and by that name, a space and a variant for each other
// letter one method writes, with each free-text field the payload carries set
// to text.
func letters(text string) map[string]func(context.Context, Notifier) error {
	const to = "someone@example.com"
	return map[string]func(context.Context, Notifier) error{
		"SendOrderPlaced": func(ctx context.Context, n Notifier) error {
			return n.SendOrderPlaced(ctx, &OrderPlaced{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", TotalCents: 123400,
			})
		},
		"SendOrderPaid": func(ctx context.Context, n Notifier) error {
			return n.SendOrderPaid(ctx, &OrderPaid{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", AmountCents: 123400, Card: text,
			})
		},
		"SendOrderShipped": func(ctx context.Context, n Notifier) error {
			return n.SendOrderShipped(ctx, &OrderShipped{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", Carrier: text, Tracking: text,
			})
		},
		"SendOrderTerminal": func(ctx context.Context, n Notifier) error {
			return n.SendOrderTerminal(ctx, &OrderTerminal{OrderID: uuid.New(), Kind: TerminalDelivered},
				TerminalRecipient{Address: to, Name: text, OrderNumber: "GO-260101-000001"})
		},
		"SendRestockNotice": func(ctx context.Context, n Notifier) error {
			return n.SendRestockNotice(ctx, &RestockNotice{
				Email: to, ProductName: text, Slug: "koto-pad", SKU: "KP-1",
			})
		},
		"SendPasswordReset": func(ctx context.Context, n Notifier) error {
			return n.SendPasswordReset(ctx, &PasswordReset{Email: to, Token: "tok"})
		},
		"SendAddressVerify": func(ctx context.Context, n Notifier) error {
			return n.SendAddressVerify(ctx, &AddressVerify{Email: to, Token: "tok"})
		},
		"SendAddressVerify registration": func(ctx context.Context, n Notifier) error {
			return n.SendAddressVerify(ctx, &AddressVerify{Email: to, Token: "tok", Registration: true, Next: text})
		},
		"SendAccountExists": func(ctx context.Context, n Notifier) error {
			return n.SendAccountExists(ctx, &AccountExists{Email: to, Name: text})
		},
		"SendAccountExists change": func(ctx context.Context, n Notifier) error {
			return n.SendAccountExists(ctx, &AccountExists{Email: to, Name: text, Change: true})
		},
		"SendStaffInvitation": func(ctx context.Context, n Notifier) error {
			return n.SendStaffInvitation(ctx, &StaffInvitation{UserID: uuid.NewString()}, to, text)
		},
		"SendStaffEnrolment": func(ctx context.Context, n Notifier) error {
			return n.SendStaffEnrolment(ctx, &StaffEnrolment{Email: to, Code: "01234567"})
		},
		"SendNewsletterConfirm": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterConfirm(ctx, &NewsletterConfirm{Email: to, Token: "tok"})
		},
		"SendNewsletterWelcome": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterWelcome(ctx, &NewsletterWelcome{Email: to, UnsubscribeToken: "tok"})
		},
		"SendNewsletterIssue": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterIssue(ctx, &NewsletterIssue{
				Email: to, Subject: text, Body: "本期內容", UnsubscribeToken: "tok",
			})
		},
	}
}

// TestLettersWritesEveryLetterNotifierSends holds letters to Notifier's Send
// methods, so a letter added to Notifier is held to the header and link tests
// below rather than passing them unseen.
func TestLettersWritesEveryLetterNotifierSends(t *testing.T) {
	t.Parallel()
	table := letters("Alex")
	notifier := reflect.TypeFor[*Notifier]()
	sends := make(map[string]bool)
	for i := range notifier.NumMethod() {
		name := notifier.Method(i).Name
		if !strings.HasPrefix(name, "Send") {
			continue
		}
		sends[name] = true
		if _, ok := table[name]; !ok {
			t.Errorf("Notifier.%s sends a letter that letters() never writes", name)
		}
	}
	for name := range table {
		if method, _, _ := strings.Cut(name, " "); !sends[method] {
			t.Errorf("letters() writes %q, which is no Send method of Notifier", name)
		}
	}
}

// wireHeaders is the header block of m as render puts it on the wire.
func wireHeaders(m *Message) []string {
	wire := string(render("goen <no-reply@goen.example>", m))
	head, _, _ := strings.Cut(wire, "\r\n\r\n")
	return strings.Split(head, "\r\n")
}

// TestNoTextInALetterCanAddAMailHeader holds the header block of every letter
// to the fixed headers render writes. A subject, a name, a product name, a card
// label or a carrier carrying a line break is text in a header or in the body,
// never a header of its own, and a recipient carrying one is never sent to.
func TestNoTextInALetterCanAddAMailHeader(t *testing.T) {
	t.Parallel()
	want := []string{"From: ", "To: ", "Subject: ", "Date: ", "Message-ID: ", "MIME-Version: ", "Content-Type: ", "Content-Transfer-Encoding: "}

	for i, text := range []string{
		"Alex\r\nBcc: harvest@example.net",
		"Alex\nBcc: harvest@example.net",
		"Alex\rBcc: harvest@example.net",
		"王小明\r\n\r\nContent-Type: text/html",
		"Alex\u2028Bcc: harvest@example.net",
	} {
		for letter, send := range letters(text) {
			t.Run(letter+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				sink := &recorded{}
				if err := send(t.Context(), New(sink, "https://goen.test", "", "")); err != nil {
					t.Fatalf("send: %v", err)
				}
				if len(sink.msgs) != 1 {
					t.Fatalf("%d messages sent, want 1", len(sink.msgs))
				}
				headers := wireHeaders(sink.msgs[0])
				wantHeaders := want
				if letter == "SendNewsletterIssue" {
					wantHeaders = append(append([]string{}, want[:6]...),
						"List-Unsubscribe: <https://goen.test/newsletter/unsubscribe?token=tok>",
						"List-Unsubscribe-Post: List-Unsubscribe=One-Click")
					wantHeaders = append(wantHeaders, want[6:]...)
				}
				if len(headers) != len(wantHeaders) {
					t.Fatalf("the letter has %d header lines, want %d:\n%s",
						len(headers), len(wantHeaders), strings.Join(headers, "\n"))
				}
				for i, line := range headers {
					if !strings.HasPrefix(line, wantHeaders[i]) {
						t.Errorf("header line %d is %q, want it to start %q", i, line, wantHeaders[i])
					}
					// Text that reached a header is encoded there; a raw line
					// break would end the header and start another.
					if strings.ContainsAny(line, "\r\n ") {
						t.Errorf("header line %d carries a raw line break: %q", i, line)
					}
				}
				if got := headers[1]; got != "To: someone@example.com" {
					t.Errorf("the recipient header is %q", got)
				}
				if got := headers[4]; !strings.HasSuffix(got, "@goen.example>") {
					t.Errorf("the Message-ID header is %q, want an id in the sender's domain", got)
				}
			})
		}
	}

	// A recipient is never text that could carry a header of its own.
	for _, to := range []string{
		"someone@example.com\r\nBcc: harvest@example.net",
		"someone@example.com\nBcc: harvest@example.net",
		"Someone <someone@example.com>",
	} {
		sink := &recorded{}
		n := New(sink, "https://goen.test", "", "")
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Email: to, OrderNumber: "GO-260101-000001", TotalCents: 100,
		}); err == nil || len(sink.msgs) != 0 {
			t.Errorf("SendOrderPlaced to %q = %v and sent %d messages, want a refusal", to, err, len(sink.msgs))
		}

		ln := listener(t)
		dialled := make(chan struct{}, 1)
		go func() {
			if c, err := ln.Accept(); err == nil {
				dialled <- struct{}{}
				_ = c.Close()
			}
		}()
		if err := (SMTPSender{Addr: ln.Addr().String(), From: "no-reply@goen.example"}).Send(t.Context(),
			&Message{To: to, Subject: "s", Body: "b"}); err == nil {
			t.Errorf("SMTPSender.Send accepted recipient %q", to)
		}
		select {
		case <-dialled:
			t.Errorf("SMTPSender dialled the relay for recipient %q before refusing it", to)
		default:
		}
	}
}

var mailedLink = regexp.MustCompile(`https?://[^\s)」）]+`)

// TestEveryMailedLinkStartsAtTheConfiguredOrigin holds that a letter links
// only to the base URL goen was configured with. Letters are written by the
// outbox worker, where no request exists, so no Host header can reach one;
// this keeps that true of every letter goen sends.
func TestEveryMailedLinkStartsAtTheConfiguredOrigin(t *testing.T) {
	t.Parallel()
	const origin = "https://shop.configured.example"

	for letter, send := range letters("Alex") {
		t.Run(letter, func(t *testing.T) {
			t.Parallel()
			sink := &recorded{}
			if err := send(t.Context(), New(sink, origin+"/", "", "")); err != nil {
				t.Fatalf("send: %v", err)
			}
			if len(sink.msgs) != 1 {
				t.Fatalf("%d messages sent, want 1", len(sink.msgs))
			}
			links := mailedLink.FindAllString(sink.msgs[0].Body, -1)
			if len(links) == 0 {
				t.Fatalf("the %s letter carries no link; the test would prove nothing:\n%s",
					letter, sink.msgs[0].Body)
			}
			for _, link := range links {
				u, err := url.Parse(link)
				if err != nil || u.Scheme+"://"+u.Host != origin || u.User != nil ||
					!strings.HasPrefix(link, origin+"/") {
					t.Errorf("the %s letter links to %q, outside %s", letter, link, origin)
				}
			}
		})
	}
}
