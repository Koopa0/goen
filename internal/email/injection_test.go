package email

import (
	"context"
	"net/url"
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

// letters sends one of every letter goen writes, with each free-text field
// the payload carries set to text.
func letters(text string) map[string]func(context.Context, Notifier) error {
	const to = "someone@example.com"
	return map[string]func(context.Context, Notifier) error{
		"order.placed": func(ctx context.Context, n Notifier) error {
			return n.SendOrderPlaced(ctx, &OrderPlaced{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", TotalCents: 123400,
			})
		},
		"order.paid": func(ctx context.Context, n Notifier) error {
			return n.SendOrderPaid(ctx, &OrderPaid{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", AmountCents: 123400, Card: text,
			})
		},
		"order.shipped": func(ctx context.Context, n Notifier) error {
			return n.SendOrderShipped(ctx, &OrderShipped{
				Email: to, Name: text, OrderNumber: "GO-260101-000001", Carrier: text, Tracking: text,
			})
		},
		"order.terminal": func(ctx context.Context, n Notifier) error {
			return n.SendOrderTerminal(ctx, &OrderTerminal{OrderID: uuid.New(), Kind: TerminalDelivered},
				TerminalRecipient{Address: to, Name: text, OrderNumber: "GO-260101-000001"})
		},
		"catalogue.restocked": func(ctx context.Context, n Notifier) error {
			return n.SendRestockNotice(ctx, &RestockNotice{
				Email: to, ProductName: text, Slug: "koto-pad", SKU: "KP-1",
			})
		},
		"account.password_reset": func(ctx context.Context, n Notifier) error {
			return n.SendPasswordReset(ctx, &PasswordReset{Email: to, Token: "tok"})
		},
		"account.email_verify": func(ctx context.Context, n Notifier) error {
			return n.SendAddressVerify(ctx, &AddressVerify{Email: to, Token: "tok"})
		},
		"staff.invitation": func(ctx context.Context, n Notifier) error {
			return n.SendStaffInvitation(ctx, &StaffInvitation{UserID: uuid.NewString()}, to, text)
		},
		"newsletter.confirm": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterConfirm(ctx, &NewsletterConfirm{Email: to, Token: "tok"})
		},
		"newsletter.welcome": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterWelcome(ctx, &NewsletterWelcome{Email: to, UnsubscribeToken: "tok"})
		},
		"newsletter.issue": func(ctx context.Context, n Notifier) error {
			return n.SendNewsletterIssue(ctx, &NewsletterIssue{
				Email: to, Subject: text, Body: "本期內容", UnsubscribeToken: "tok",
			})
		},
	}
}

// wireHeaders is the header block of m as render puts it on the wire.
func wireHeaders(m *Message) []string {
	wire := string(render("goen <no-reply@goen.example>", m))
	head, _, _ := strings.Cut(wire, "\r\n\r\n")
	return strings.Split(head, "\r\n")
}

// TestNoTextInALetterCanAddAMailHeader holds the header block of every letter
// to the six headers render writes. A subject, a name, a product name, a card
// label or a carrier carrying a line break is text in a header or in the body,
// never a header of its own, and a recipient carrying one is never sent to.
func TestNoTextInALetterCanAddAMailHeader(t *testing.T) {
	t.Parallel()
	want := []string{"From: ", "To: ", "Subject: ", "MIME-Version: ", "Content-Type: ", "Content-Transfer-Encoding: "}

	for i, text := range []string{
		"Alex\r\nBcc: harvest@example.net",
		"Alex\nBcc: harvest@example.net",
		"Alex\rBcc: harvest@example.net",
		"王小明\r\n\r\nContent-Type: text/html",
		"Alex\u2028Bcc: harvest@example.net",
	} {
		for topic, send := range letters(text) {
			t.Run(topic+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				sink := &recorded{}
				if err := send(t.Context(), New(sink, "https://goen.test", "", "")); err != nil {
					t.Fatalf("send: %v", err)
				}
				if len(sink.msgs) != 1 {
					t.Fatalf("%d messages sent, want 1", len(sink.msgs))
				}
				headers := wireHeaders(sink.msgs[0])
				if len(headers) != len(want) {
					t.Fatalf("the letter has %d header lines, want %d:\n%s",
						len(headers), len(want), strings.Join(headers, "\n"))
				}
				for i, line := range headers {
					if !strings.HasPrefix(line, want[i]) {
						t.Errorf("header line %d is %q, want it to start %q", i, line, want[i])
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

	for topic, send := range letters("Alex") {
		t.Run(topic, func(t *testing.T) {
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
					topic, sink.msgs[0].Body)
			}
			for _, link := range links {
				u, err := url.Parse(link)
				if err != nil || u.Scheme+"://"+u.Host != origin || u.User != nil ||
					!strings.HasPrefix(link, origin+"/") {
					t.Errorf("the %s letter links to %q, outside %s", topic, link, origin)
				}
			}
		})
	}
}
