// Probe only: renders every message the email package sends, in both
// languages, to <outdir>/<name>.<lang>.html and .txt, plus an index.tsv.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
)

type capture struct {
	dir, name, lang string
	index           *strings.Builder
}

func (c capture) Send(_ context.Context, m *email.Message) error {
	stem := filepath.Join(c.dir, c.name+"."+c.lang)
	text := "Subject: " + m.Subject + "\nTo: " + m.To + "\n\n" + m.Body
	if err := os.WriteFile(stem+".txt", []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.index, "%s\t%s\t%s\n", c.name, c.lang, m.Subject)
	return os.WriteFile(stem+".html", []byte(m.HTML), 0o644)
}

func main() {
	dir, base := os.Args[1], os.Args[2]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	var index strings.Builder
	ctx := context.Background()
	order := uuid.New()
	ends := time.Now().AddDate(0, 0, 7)
	owed := int64(0)

	for _, lang := range []string{"zh-Hant", "en"} {
		name, tw := "林雅婷", "Amelia Lin"
		if lang == "en" {
			name = tw
		}
		run := func(stem string, send func(n email.Notifier) error) {
			c := capture{dir: dir, name: stem, lang: lang, index: &index}
			n := email.New(c, base, "goen 示範商店", "support@goen.example")
			if err := send(n); err != nil {
				fmt.Fprintf(os.Stderr, "%s.%s: %v\n", stem, lang, err)
				os.Exit(1)
			}
		}
		to := "amelia@example.com"
		run("order-placed", func(n email.Notifier) error {
			return n.SendOrderPlaced(ctx, &email.OrderPlaced{Locale: lang, OrderNumber: "GO-20261007-0042", Email: to, Name: name, TotalCents: 1259000})
		})
		run("order-placed-funded", func(n email.Notifier) error {
			return n.SendOrderPlaced(ctx, &email.OrderPlaced{Locale: lang, OrderNumber: "GO-20261007-0043", Email: to, Name: name, TotalCents: 359000, OwedCents: &owed})
		})
		run("order-paid", func(n email.Notifier) error {
			return n.SendOrderPaid(ctx, &email.OrderPaid{Locale: lang, OrderNumber: "GO-20261007-0042", Email: to, Name: name, AmountCents: 1259000, Card: "Visa •••• 4242"})
		})
		run("order-paid-no-card", func(n email.Notifier) error {
			return n.SendOrderPaid(ctx, &email.OrderPaid{Locale: lang, OrderNumber: "GO-20261007-0042", Email: to, Name: name, AmountCents: 1259000})
		})
		run("order-shipped-home", func(n email.Notifier) error {
			return n.SendOrderShipped(ctx, &email.OrderShipped{Locale: lang, OrderNumber: "GO-20261007-0042", Email: to, Name: name, Carrier: "black_cat", Tracking: "123456789012"})
		})
		run("order-shipped-pickup", func(n email.Notifier) error {
			return n.SendOrderShipped(ctx, &email.OrderShipped{Locale: lang, OrderNumber: "GO-20261007-0044", Email: to, Name: name, Carrier: "seven_eleven", Tracking: "F12345678901", Pickup: true})
		})
		run("restock-notice", func(n email.Notifier) error {
			return n.SendRestockNotice(ctx, &email.RestockNotice{Locale: lang, Email: to, ProductName: "Pixelight 9 Pro", Slug: "pixelight-9-pro", SKU: "PXL-9P-1-1"})
		})
		for _, t := range []struct {
			stem     string
			kind     email.TerminalKind
			refunded bool
			ends     time.Time
		}{
			{"terminal-delivered", email.TerminalDelivered, false, ends},
			{"terminal-collected", email.TerminalCollected, false, ends},
			{"terminal-cancelled-by-customer", email.TerminalCancelledByCustomer, false, time.Time{}},
			{"terminal-cancelled-by-customer-refund", email.TerminalCancelledByCustomer, true, time.Time{}},
			{"terminal-cancelled-by-staff", email.TerminalCancelledByStaff, false, time.Time{}},
			{"terminal-cancelled-by-deadline", email.TerminalCancelledByPaymentDeadline, false, time.Time{}},
			{"terminal-cancelled-by-deadline-refund", email.TerminalCancelledByPaymentDeadline, true, time.Time{}},
		} {
			run(t.stem, func(n email.Notifier) error {
				return n.SendOrderTerminal(ctx, &email.OrderTerminal{OrderID: order, Kind: t.kind, Refunded: t.refunded},
					email.TerminalRecipient{Address: to, Name: name, Locale: lang, OrderNumber: "GO-20261007-0042", RescissionEnds: t.ends})
			})
		}
		run("password-reset", func(n email.Notifier) error {
			return n.SendPasswordReset(ctx, &email.PasswordReset{Locale: lang, Email: to, Token: "3f9a1c7e5b2d48a6"})
		})
		run("account-exists", func(n email.Notifier) error {
			return n.SendAccountExists(ctx, &email.AccountExists{Locale: lang, Email: to, Name: name})
		})
		run("address-in-use", func(n email.Notifier) error {
			return n.SendAccountExists(ctx, &email.AccountExists{Locale: lang, Email: to, Name: name, Change: true})
		})
		run("newsletter-confirm", func(n email.Notifier) error {
			return n.SendNewsletterConfirm(ctx, &email.NewsletterConfirm{Locale: lang, Email: to, Token: "3f9a1c7e5b2d48a6"})
		})
		run("newsletter-welcome", func(n email.Notifier) error {
			return n.SendNewsletterWelcome(ctx, &email.NewsletterWelcome{Locale: lang, Email: to, UnsubscribeToken: "9b8c7d6e5f4a3b2c"})
		})
		subject, body := "十月新品：Pixelight 9 Pro 到貨", "Pixelight 9 Pro 已經到貨，現在訂購，三天內送達。\n\n看看這一期的選物：\n"+base+"/deals\n\n這個月的茶與咖啡週，每天都有特價。"
		if lang == "en" {
			subject, body = "October arrivals: the Pixelight 9 Pro is here", "The Pixelight 9 Pro has landed. Order now and it reaches you within three days.\n\nThis issue's picks:\n"+base+"/deals\n\nTea and coffee week runs all month, with a new price every day."
		}
		run("newsletter-issue", func(n email.Notifier) error {
			return n.SendNewsletterIssue(ctx, &email.NewsletterIssue{Locale: lang, Email: to, Subject: subject, Body: body, UnsubscribeToken: "9b8c7d6e5f4a3b2c"})
		})
		run("address-verify", func(n email.Notifier) error {
			return n.SendAddressVerify(ctx, &email.AddressVerify{Locale: lang, Email: to, Token: "3f9a1c7e5b2d48a6"})
		})
		run("register-verify", func(n email.Notifier) error {
			return n.SendAddressVerify(ctx, &email.AddressVerify{Locale: lang, Email: to, Token: "3f9a1c7e5b2d48a6", Registration: true, Next: "/checkout"})
		})
		run("staff-invitation", func(n email.Notifier) error {
			return n.SendStaffInvitation(ctx, &email.StaffInvitation{UserID: uuid.NewString(), Locale: lang}, "staff@example.com", name)
		})
		run("staff-enrolment", func(n email.Notifier) error {
			return n.SendStaffEnrolment(ctx, &email.StaffEnrolment{Locale: lang, Email: "staff@example.com", Code: "48201937"})
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "index.tsv"), []byte(index.String()), 0o644); err != nil {
		panic(err)
	}
}
