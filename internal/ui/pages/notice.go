package pages

import "github.com/koopa0/goen/internal/i18n"

type NoticeLink struct {
	Href  string
	Label i18n.Key
}

type NoticeActions struct {
	Primary   NoticeLink
	Secondary NoticeLink
}

func noticeActions(next []NoticeActions) NoticeActions {
	if len(next) > 0 {
		return next[0]
	}
	return NoticeActions{Primary: NoticeLink{Href: "/", Label: i18n.KeyBackToShop}}
}
