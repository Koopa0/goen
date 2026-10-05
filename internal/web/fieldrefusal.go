package web

import "github.com/koopa0/goen/internal/i18n"

// FieldRefusal is a refusal of one form field, worded by its catalogue key.
type FieldRefusal struct {
	Field      string
	MessageKey i18n.Key
}
