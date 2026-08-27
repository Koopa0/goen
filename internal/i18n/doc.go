// Package i18n decides what language a page speaks.
//
// The chrome — navigation, buttons, labels, validation messages, empty states —
// follows the visitor. Product copy and the policy documents do not.
//
// File names express ownership inside this one package. Unprefixed feature files
// contain storefront, account, and shared copy. Files named admin_<feature>.go
// own /admin back-office copy; the prefix groups that copy beside internal/admin
// and its KeyAdmin symbols. It is a sorting convention, not a subpackage boundary.
package i18n
