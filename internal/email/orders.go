package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// OrderPaid is what an order.paid message carries.
type OrderPaid struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	// Card is "VISA ****4242" or empty. checkout.session.completed does not
	// expand the charge, so the usual capture has neither brand nor last4 —
	// the mail says nothing about the card rather than inventing one.
	Card string `json:"card"`
}

// SendOrderPaid tells somebody their money arrived.
func (n Notifier) SendOrderPaid(ctx context.Context, p *OrderPaid) error {
	if !Valid(p.Email) {
		return errors.New("an order.paid message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	body := n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidBody),
		p.OrderNumber, twd(p.AmountCents), n.orderURL(p.OrderNumber)))
	if p.Card != "" {
		body += "\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidCard), p.Card)
	}

	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidSubject), p.OrderNumber),
		Body:    body,
	})
}

// OrderShipped is what an order.shipped message carries.
type OrderShipped struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
}

// SendOrderShipped tells somebody their parcel is on its way.
func (n Notifier) SendOrderShipped(ctx context.Context, p *OrderShipped) error {
	if !Valid(p.Email) {
		return errors.New("an order.shipped message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedSubject), p.OrderNumber),
		Body: n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedBody),
			p.OrderNumber, p.Carrier, p.Tracking, n.orderURL(p.OrderNumber))),
	})
}

// RestockNotice is what a catalogue.restocked message carries.
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

	return n.sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRestockSubject), p.ProductName),
		Body:    body,
	})
}
