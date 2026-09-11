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

// TestARestockNoticeIdentifiesTheQueuedVariant: two variants of one product
// share a name and slug, so only the queued SKU tells the recipient which
// one came back.
func TestARestockNoticeIdentifiesTheQueuedVariant(t *testing.T) {
	t.Parallel()

	n, sink := notifier(t)
	first := &RestockNotice{
		Locale: "en", Email: "a@b.co",
		ProductName: "Koto Pad", Slug: "koto-pad", SKU: "KP-BLUE",
	}
	if err := n.SendRestockNotice(t.Context(), first); err != nil {
		t.Fatalf("send: %v", err)
	}
	body := sink.msg.Body
	if !strings.Contains(body, "KP-BLUE") {
		t.Errorf("the restock letter does not name the queued SKU:\n%s", body)
	}
	if strings.Contains(body, "KP-BLACK") {
		t.Errorf("the restock letter names a sibling variant:\n%s", body)
	}
	if !strings.Contains(body, "https://goen.test/p/koto-pad") {
		t.Errorf("the restock letter dropped the product link:\n%s", body)
	}
	if !strings.Contains(body, "does not reserve") {
		t.Errorf("the restock letter dropped the no-reservation warning:\n%s", body)
	}

	sibling := *first
	sibling.SKU = "KP-BLACK"
	if err := n.SendRestockNotice(t.Context(), &sibling); err != nil {
		t.Fatalf("send sibling: %v", err)
	}
	if !strings.Contains(sink.msg.Body, "KP-BLACK") {
		t.Errorf("the sibling letter does not name its queued SKU:\n%s", sink.msg.Body)
	}
	if strings.Contains(sink.msg.Body, "KP-BLUE") {
		t.Errorf("the sibling letter names the other variant:\n%s", sink.msg.Body)
	}

	n, sink = notifier(t)
	zh := *first
	zh.Locale = "zh-Hant"
	if err := n.SendRestockNotice(t.Context(), &zh); err != nil {
		t.Fatalf("send zh-Hant: %v", err)
	}
	if !strings.Contains(sink.msg.Body, "KP-BLUE") {
		t.Errorf("the Chinese letter does not name the queued SKU:\n%s", sink.msg.Body)
	}
}

func centsPtr(n int64) *int64 { return &n }

// TestAPlacedLetterNamesWhatIsStillOwed: store credit and a coupon change
// the amount payable, not the order total. The confirmation must quote the
// former and drop the pay CTA when nothing remains.
func TestAPlacedLetterNamesWhatIsStillOwed(t *testing.T) {
	t.Parallel()

	const (
		number = "GO-260101-000001"
		total  = int64(199900)
		owed   = int64(99900)
	)

	t.Run("fully funded omits the pay CTA", func(t *testing.T) {
		t.Parallel()

		n, sink := notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: "zh-Hant", Email: "a@b.co", Name: "王小明",
			OrderNumber: number, TotalCents: total, OwedCents: centsPtr(0),
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
		body := sink.msg.Body
		if strings.Contains(body, "付款") {
			t.Errorf("a fully funded Chinese letter still asks the customer to pay:\n%s", body)
		}
		if strings.Contains(body, "應付金額") {
			t.Errorf("a fully funded letter still names an amount due:\n%s", body)
		}
		if !strings.Contains(body, "查看訂單:") {
			t.Errorf("a fully funded letter dropped the order link:\n%s", body)
		}
		if !strings.Contains(body, "https://goen.test/orders/"+number) {
			t.Errorf("a fully funded letter dropped the order URL:\n%s", body)
		}

		n, sink = notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: "en", Email: "a@b.co", Name: "Alex",
			OrderNumber: number, TotalCents: total, OwedCents: centsPtr(0),
		}); err != nil {
			t.Fatalf("send en: %v", err)
		}
		en := sink.msg.Body
		if strings.Contains(strings.ToLower(en), "pay") {
			t.Errorf("a fully funded English letter still asks the customer to pay:\n%s", en)
		}
		if strings.Contains(en, "Amount due") {
			t.Errorf("a fully funded English letter still names an amount due:\n%s", en)
		}
		if !strings.Contains(en, "View the order:") {
			t.Errorf("a fully funded English letter dropped the order link:\n%s", en)
		}
	})

	t.Run("partial credit quotes owed not the total", func(t *testing.T) {
		t.Parallel()

		n, sink := notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: "zh-Hant", Email: "a@b.co", Name: "王小明",
			OrderNumber: number, TotalCents: total, OwedCents: centsPtr(owed),
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
		body := sink.msg.Body
		if !strings.Contains(body, "NT$999") {
			t.Errorf("a partly funded letter does not name the amount still owed:\n%s", body)
		}
		if strings.Contains(body, "NT$1,999") {
			t.Errorf("a partly funded letter quotes the order total instead of what is owed:\n%s", body)
		}
		if !strings.Contains(body, "查看訂單與付款") {
			t.Errorf("a partly funded letter dropped the pay CTA:\n%s", body)
		}

		n, sink = notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: "en", Email: "a@b.co", Name: "Alex",
			OrderNumber: number, TotalCents: total, OwedCents: centsPtr(owed),
		}); err != nil {
			t.Fatalf("send en: %v", err)
		}
		en := sink.msg.Body
		if !strings.Contains(en, "NT$999") {
			t.Errorf("the English letter does not name the amount still owed:\n%s", en)
		}
		if strings.Contains(en, "NT$1,999") {
			t.Errorf("the English letter quotes the order total instead of what is owed:\n%s", en)
		}
		if !strings.Contains(en, "View it and pay") {
			t.Errorf("the English letter dropped the pay CTA:\n%s", en)
		}
	})

	t.Run("a queued row without owed_cents keeps the total", func(t *testing.T) {
		t.Parallel()

		n, sink := notifier(t)
		if err := n.SendOrderPlaced(t.Context(), &OrderPlaced{
			Locale: "en", Email: "a@b.co", Name: "Alex",
			OrderNumber: number, TotalCents: total,
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
		if !strings.Contains(sink.msg.Body, "NT$1,999") {
			t.Errorf("an already-queued letter did not fall back to the total:\n%s", sink.msg.Body)
		}
		if !strings.Contains(sink.msg.Body, "View it and pay") {
			t.Errorf("an already-queued letter dropped the pay CTA:\n%s", sink.msg.Body)
		}
	})
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
