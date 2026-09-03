package email

import (
	"context"
	"strings"
	"testing"
	"unicode"
)

// captured is the last message a test sent.
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
	return New(sink, "https://goen.test", "", ""), sink
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// TestEveryMessageIsSentInThePayloadsLanguage is driven per topic, because each
// producer records the locale differently: some from a request, some off the
// order row.
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
			// Only the SUBJECT is asserted Han-free: a product name is the
			// shop's own data and stays as authored.
			if hasHan(sink.msg.Subject) {
				t.Errorf("the English subject is Chinese: %q", sink.msg.Subject)
			}
			// A restock notice quotes a product name, so that one is checked for
			// the sign-off instead.
			if topic != "catalogue.restocked" && hasHan(sink.msg.Body) {
				t.Errorf("the English body is Chinese:\n%s", sink.msg.Body)
			}
			if !strings.Contains(sink.msg.Body, "do not reply") {
				t.Errorf("the English body has no English sign-off:\n%s", sink.msg.Body)
			}

			// Or the assertions above would pass on a notifier that only ever
			// spoke English.
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

// TestTheConfirmationCarriesTheStatutoryDisclosure holds Consumer Protection Act
// §18 II: §18 I's six items must reach the consumer in a form they can STORE,
// and under §19 III an omission restarts the seven-day rescission window.
func TestTheConfirmationCarriesTheStatutoryDisclosure(t *testing.T) {
	t.Parallel()

	placed := &OrderPlaced{
		Locale: "zh-Hant", OrderNumber: "GO-260806-000001",
		Email: "who@example.test", Name: "王小明", TotalCents: 199900,
	}

	t.Run("configured", func(t *testing.T) {
		t.Parallel()
		sink := &captured{}
		n := New(sink, "https://goen.test",
			"goen Co., Ltd. 統編 90123456", "support@goen.tw / 02-2700-1234")
		if err := n.SendOrderPlaced(t.Context(), placed); err != nil {
			t.Fatalf("send: %v", err)
		}
		body := sink.msg.Body
		// §18 I item 1: who is selling, and how to reach them.
		for _, want := range []string{"goen Co., Ltd. 統編 90123456", "support@goen.tw"} {
			if !strings.Contains(body, want) {
				t.Errorf("the confirmation does not name %q — §18 I item 1", want)
			}
		}
		// Items 3, 4 and 5: the window and how to exercise it, what is excluded,
		// and how to complain.
		for _, want := range []string{"七日", "解除契約", "沒有任何商品排除", "消費申訴"} {
			if !strings.Contains(body, want) {
				t.Errorf("the confirmation does not carry %q; §18 I wants the "+
					"rescission window, how to use it, what is excluded from it, "+
					"and how to complain", want)
			}
		}
	})

	t.Run("English follows the payload", func(t *testing.T) {
		t.Parallel()
		sink := &captured{}
		n := New(sink, "https://goen.test", "goen Co., Ltd.", "support@goen.tw")
		en := *placed
		en.Locale = "en"
		en.Name = "Alex"
		if err := n.SendOrderPlaced(t.Context(), &en); err != nil {
			t.Fatalf("send: %v", err)
		}
		if !strings.Contains(sink.msg.Body, "seven days") {
			t.Error("the English confirmation does not state the seven-day right — " +
				"an English-reading customer in Taiwan holds it identically")
		}
		if hasHan(sink.msg.Body) {
			t.Errorf("the English confirmation still carries Han:\n%s", sink.msg.Body)
		}
	})

	t.Run("unconfigured omits it rather than printing a blank seller", func(t *testing.T) {
		t.Parallel()
		n, sink := notifier(t)
		if err := n.SendOrderPlaced(t.Context(), placed); err != nil {
			t.Fatalf("send: %v", err)
		}
		if strings.Contains(sink.msg.Body, "消費者保護法第 18 條") {
			t.Error("a disclosure naming no seller was sent; it discloses nothing " +
				"and makes the letter look compliant while saying less than silence")
		}
		if !strings.Contains(sink.msg.Body, "GO-260806-000001") {
			t.Error("the confirmation itself went missing with the disclosure")
		}
	})
}

// TestNewRefusesAnUnusableNotifier holds the constructor's invariant. Both
// omissions are silent otherwise: a nil sender panics at the first message
// instead of at wiring, and an empty base URL mails a link that resolves
// nowhere — a password reset nobody can follow.
func TestNewRefusesAnUnusableNotifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sender  Sender
		baseURL string
	}{
		{name: "no sender", sender: nil, baseURL: "https://goen.test"},
		{name: "no base URL", sender: &captured{}, baseURL: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Errorf("New(%v, %q, ...) did not panic", tt.sender, tt.baseURL)
				}
			}()
			New(tt.sender, tt.baseURL, "", "")
		})
	}
}
