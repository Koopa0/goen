package email

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// OrderPaid is what an order.paid message carries.
//
// Declared here rather than imported from internal/payment, for the reason
// OrderPlaced is: a consumer that imports the producer's types is a consumer
// that cannot be deployed a version behind.
type OrderPaid struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
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
//
// A receipt is the one email a customer looks for later, and until now goen
// sent none: the topic constant existed, nothing produced it, and nothing
// handled it. A payment page that says 已付款 is a page; an email is a record.
func (n Notifier) SendOrderPaid(ctx context.Context, p *OrderPaid) error {
	if !Valid(p.Email) {
		return errors.New("an order.paid message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	body := n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidBody),
		p.OrderNumber, twd(p.AmountCents), n.orderURL(p.OrderNumber)))
	if p.Card != "" {
		// Appended rather than woven into the message: a payment method is often
		// absent — checkout.session.completed does not expand the charge — and a
		// sentence with a hole in it reads worse than one line fewer.
		body += "\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidCard), p.Card)
	}

	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailPaidSubject), p.OrderNumber),
		Body:    body,
	})
}

// OrderShipped is what an order.shipped message carries.
type OrderShipped struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
}

// SendOrderShipped tells somebody their parcel is on its way.
//
// The tracking number is IN the mail rather than only on the order page. It is
// what the customer takes to the carrier's own site, and making them sign in to
// find it is the difference between a notification and a nudge to come back.
func (n Notifier) SendOrderShipped(ctx context.Context, p *OrderShipped) error {
	if !Valid(p.Email) {
		return errors.New("an order.shipped message has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedSubject), p.OrderNumber),
		Body: n.letter(ctx, p.Name, fmt.Sprintf(i18n.T(ctx, i18n.KeyMailShippedBody),
			p.OrderNumber, p.Carrier, p.Tracking, n.orderURL(p.OrderNumber))),
	})
}

// RestockNotice is what a catalogue.restocked message carries.
//
// The product SLUG rather than an id: the link is the whole point of the mail,
// and a slug is what a URL is made of.
type RestockNotice struct {
	// Locale is the language to send in, recorded by the producer. See
	// [Notifier.locale].
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

// SendRestockNotice tells somebody the thing they wanted is back.
//
// It says the stock is limited and does not pretend to hold any: goen reserves
// nothing for a notice, so promising otherwise would be a promise the shop
// breaks for everybody after the first.
func (n Notifier) SendRestockNotice(ctx context.Context, p *RestockNotice) error {
	if !Valid(p.Email) {
		return errors.New("a restock notice has no usable email address")
	}

	ctx = n.locale(ctx, p.Locale)
	body := n.letter(ctx, "", fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRestockBody),
		p.ProductName, strings.TrimRight(n.BaseURL, "/")+"/p/"+p.Slug))

	return n.Sender.Send(ctx, &Message{
		To:      p.Email,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRestockSubject), p.ProductName),
		Body:    body,
	})
}
