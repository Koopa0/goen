package admin

import "github.com/koopa0/goen/internal/i18n"

type MissingRecordView struct {
	Section     string
	Heading     string
	Body        string
	BackLabel   i18n.Key
	OrderNumber string
}
