package web

import "github.com/koopa0/goen/internal/i18n"

// FieldError is a refusal of one form field, worded by its catalogue key.
type FieldError struct {
	Field      string
	MessageKey i18n.Key
}
