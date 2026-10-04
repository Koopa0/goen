package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
)

type OrderPaid struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	// Card is "Visa •••• 4242" or empty. It is empty when Stripe could not be
	// asked for the charge; the mail then says nothing about the card rather
	// than inventing one.
	Card string `json:"card"`
}

func (n Notifier) SendOrderPaid(ctx context.Context, p *OrderPaid) error {
	if !Valid(p.Email) {
		return errors.New("an order.paid message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	paid := twd(p.AmountCents)
	if p.Card != "" {
		paid += "\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidCard), p.Card)
	}
	body := n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidBody),
		p.OrderNumber, paid, n.orderURL(p.OrderNumber)))

	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidSubject), p.OrderNumber),
		Body:    body,
	})
}

type OrderShipped struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
	// Pickup is a convenience-store order, whose parcel goes to a store.
	Pickup bool `json:"pickup"`
}

func (n Notifier) SendOrderShipped(ctx context.Context, p *OrderShipped) error {
	if !Valid(p.Email) {
		return errors.New("an order.shipped message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	body := i18n.KeyMailShippedBody
	if p.Pickup {
		body = i18n.KeyMailShippedPickupBody
	}
	code := carrier.Carrier(p.Carrier)
	letterBody := fmt.Sprintf(i18n.T(ctx, body),
		p.OrderNumber, i18n.CarrierName(ctx, code), p.Tracking, n.orderURL(p.OrderNumber))
	// A carrier outside the closed set, from a notice queued before it was
	// closed, has no page to name.
	if track := code.TrackingURL(p.Tracking); track != "" {
		letterBody += "\n\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedTrack), track)
	}
	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedSubject), p.OrderNumber),
		Body:    n.letter(ctx, p.Name, letterBody),
	})
}

type RestockNotice struct {
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

// SendRestockNotice tells somebody the thing they wanted is back. It reserves
// nothing and the copy does not pretend otherwise.
func (n Notifier) SendRestockNotice(ctx context.Context, p *RestockNotice) error {
	if !Valid(p.Email) {
		return errors.New("a restock notice has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	body := n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRestockBody),
		p.ProductName, p.SKU, strings.TrimRight(n.baseURL, "/")+"/p/"+p.Slug))

	return n.send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRestockSubject), p.ProductName),
		Body:    body,
	})
}
