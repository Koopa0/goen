package email

import (
	"context"
	"strings"
	"testing"
	"unicode"
)

// captured is the last message a test sent. A plain struct rather than a mock:
// what is asserted is the OUTPUT, never that Send was called.
type captured struct {
	msg *Message
}

func (c *captured) Send(_ context.Context, m *Message) error {
	c.msg = m
	return nil
}

func notifier(t *testing.T) (Notifier, *captured) {
	t.Helper()
	sink := &captured{}
	return Notifier{Sender: sink, BaseURL: "https://goen.test"}, sink
}

// hasHan reports whether s carries a Chinese character.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// TestEveryMessageIsSentInThePayloadsLanguage is the whole point of
// orders.locale.
//
// An English customer used to get an English checkout, an English confirmation
// PAGE, and then a Chinese receipt — the half-translated failure the locale work
// exists to stop, arriving by email where nobody would see it in review. The
// worker has no locale of its own, so if this is wrong there is no second place
// that could be right.
//
// Driven per topic rather than once, because each producer records the locale
// differently: two from a request and two off the order row.
func TestEveryMessageIsSentInThePayloadsLanguage(t *testing.T) {
	t.Parallel()

	send := map[string]func(Notifier, context.Context, string) error{
		"order.placed": func(n Notifier, ctx context.Context, l string) error {
			return n.SendOrderPlaced(ctx, &OrderPlaced{
				Locale: l, Email: "a@b.co", Name: "Alex", OrderNumber: "GO-260101-000001",
				TotalCents: 123400,
			})
		},
		"order.paid": func(n Notifier, ctx context.Context, l string) error {
			return n.SendOrderPaid(ctx, &OrderPaid{
				Locale: l, Email: "a@b.co", Name: "Alex", OrderNumber: "GO-260101-000001",
				AmountCents: 123400, Card: "visa 4242",
			})
		},
		"order.shipped": func(n Notifier, ctx context.Context, l string) error {
			return n.SendOrderShipped(ctx, &OrderShipped{
				Locale: l, Email: "a@b.co", Name: "Alex", OrderNumber: "GO-260101-000001",
				Carrier: "Black Cat", Tracking: "TW123",
			})
		},
		"catalogue.restocked": func(n Notifier, ctx context.Context, l string) error {
			return n.SendRestockNotice(ctx, &RestockNotice{
				Locale: l, Email: "a@b.co", ProductName: "Koto Pad", Slug: "koto-pad", SKU: "KP-1",
			})
		},
		"account.password_reset": func(n Notifier, ctx context.Context, l string) error {
			return n.SendPasswordReset(ctx, &PasswordReset{Locale: l, Email: "a@b.co", Token: "tok"})
		},
		"newsletter.confirm": func(n Notifier, ctx context.Context, l string) error {
			return n.SendNewsletterConfirm(ctx, &NewsletterConfirm{
				Locale: l, Email: "a@b.co", Token: "tok",
			})
		},
		"newsletter.welcome": func(n Notifier, ctx context.Context, l string) error {
			return n.SendNewsletterWelcome(ctx, &NewsletterWelcome{
				Locale: l, Email: "a@b.co", UnsubscribeToken: "tok",
			})
		},
	}

	for topic, fn := range send {
		t.Run(topic, func(t *testing.T) {
			t.Parallel()

			n, sink := notifier(t)
			if err := fn(n, t.Context(), "en"); err != nil {
				t.Fatalf("send: %v", err)
			}
			if sink.msg == nil {
				t.Fatal("nothing was sent")
			}
			// The product name is the customer's own data and stays as authored,
			// so only the SUBJECT is asserted Han-free — it is goen's words plus
			// an order number.
			if hasHan(sink.msg.Subject) {
				t.Errorf("the English subject is Chinese: %q", sink.msg.Subject)
			}
			// The body carries the shop's sentences. A restock notice quotes a
			// product name, so that one is checked for the SENTENCES instead.
			if topic != "catalogue.restocked" && hasHan(sink.msg.Body) {
				t.Errorf("the English body is Chinese:\n%s", sink.msg.Body)
			}
			if !strings.Contains(sink.msg.Body, "do not reply") {
				t.Errorf("the English body has no English sign-off:\n%s", sink.msg.Body)
			}

			// And the same message in Chinese is Chinese, or the test above would
			// pass on a notifier that only ever spoke English.
			n, sink = notifier(t)
			if err := fn(n, t.Context(), "zh-Hant"); err != nil {
				t.Fatalf("send zh-Hant: %v", err)
			}
			if !hasHan(sink.msg.Subject) {
				t.Errorf("the Chinese subject is not Chinese: %q", sink.msg.Subject)
			}
		})
	}
}

// TestAnUnknownLocaleFallsBackRatherThanFailing proves a row written before
// orders.locale existed still produces a letter.
//
// The fallback is the language the shop is written in, and it is silent on
// purpose: a message nobody can send is a customer nobody tells, and the outbox
// would retry it until Stuck() surfaced it.
func TestAnUnknownLocaleFallsBackRatherThanFailing(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"", "kling-on"} {
		n, sink := notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: tag, Email: "a@b.co", Name: "王小明", OrderNumber: "GO-260101-000001",
		}); err != nil {
			t.Fatalf("locale %q: %v", tag, err)
		}
		if !hasHan(sink.msg.Subject) {
			t.Errorf("locale %q did not fall back to the default: %q", tag, sink.msg.Subject)
		}
	}
}

// TestTheGreetingDropsAnEmptyName proves a letter to an address nobody named
// does not address them as nothing.
//
// A restock notice and a password reset both go to an address rather than to a
// person, and "Hello ," is the kind of detail that makes a shop look automated.
func TestTheGreetingDropsAnEmptyName(t *testing.T) {
	t.Parallel()

	n, sink := notifier(t)
	if err := n.SendPasswordReset(t.Context(), &PasswordReset{
		Locale: "en", Email: "a@b.co", Token: "tok",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if strings.HasPrefix(sink.msg.Body, "Hello ,") || strings.Contains(sink.msg.Body, "Hello ,") {
		t.Errorf("the greeting has an empty name in it:\n%s", sink.msg.Body)
	}
	if !strings.HasPrefix(sink.msg.Body, "Hello,") {
		t.Errorf("want a bare greeting, got:\n%s", sink.msg.Body)
	}
}
