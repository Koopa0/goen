// Package i18n decides what language a page speaks.
//
// The chrome — navigation, buttons, labels, validation messages, empty states —
// follows the visitor. Product copy and the policy documents do not.
//
// Catalogue files follow business concepts, not routes or audiences. Customer-
// and staff-facing copy for the same rule stays together; a separate file marks
// a real subdomain such as fulfillment or product images.
//
// The catalogue remains one package so every key enters the same duplicate-
// checked registry. Splitting declarations into side-effect packages would make
// catalogue completeness depend on which packages a binary happened to import.
package i18n
