// Package i18n is goen's message catalogue and the locale a request speaks.
//
// Every piece of interface text goen shows or mails is a Key declared here with
// its Traditional Chinese and English text; T renders it in the locale the
// request carries (Detect, WithLocale, FromContext). The chrome — navigation,
// buttons, labels, validation messages, empty states — follows the visitor;
// product copy and the policy documents do not.
//
// The catalogue is one package on purpose. Only this package can register a
// message; every key enters one registry, which panics at init on a duplicate
// id or a missing translation; and the copy rules in this package's tests read
// every message at once. Files follow business concepts, not routes or
// audiences.
//
// This package imports no other goen package: a closed set names its own
// labels with the keys declared here, as shipping.Carrier.Name does.
package i18n
