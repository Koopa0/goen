// Package layouts renders goen's shared page chrome: the document shell, the
// site header and the site footer. Pages compose their own content inside
// [Base] and never render <html>, <head>, the header or the footer themselves.
package layouts

import "strconv"

// Page is the chrome-level view model every goen page supplies.
type Page struct {
	// Title is the page title without the site suffix.
	Title string
	// Description fills the meta description tag; empty omits the tag.
	Description string
	// Nav is the slug of the top-level category to mark as current, or "" for
	// pages that sit outside the category tree.
	Nav string
	// CartCount is the number of items in the visitor's cart.
	CartCount int
}

// NavItem is one top-level category entry in the header.
type NavItem struct {
	Slug string
	Name string
	Href string
}

// TopNav is the header's category row. The routes it points at are built in
// later batches; until then they resolve to the not-found page.
var TopNav = []NavItem{
	{Slug: "phones", Name: "手機", Href: "/c/phones"},
	{Slug: "laptops", Name: "筆電", Href: "/c/laptops"},
	{Slug: "headphones", Name: "耳機", Href: "/c/headphones"},
	{Slug: "wearables", Name: "穿戴", Href: "/c/wearables"},
	{Slug: "accessories", Name: "配件", Href: "/c/accessories"},
}

// FooterLink is one entry in a footer link column.
type FooterLink struct {
	Name string
	Href string
}

// FooterShopping and FooterAbout are the footer's two link columns.
var (
	FooterShopping = []FooterLink{
		{Name: "運送方式", Href: "/shipping"},
		{Name: "付款方式", Href: "/payment"},
		{Name: "退換貨政策", Href: "/returns"},
		{Name: "保固服務", Href: "/warranty"},
	}
	FooterAbout = []FooterLink{
		{Name: "關於 goen", Href: "/about"},
		{Name: "聯絡我們", Href: "/contact"},
		{Name: "常見問題", Href: "/faq"},
		{Name: "服務條款", Href: "/terms"},
		{Name: "隱私權政策", Href: "/privacy"},
	}
)

// title composes the document title. A page without its own title gets the
// site's own name rather than a stray separator.
func (p Page) title() string {
	if p.Title == "" {
		return "goen · 讓買家與對的好商品相遇"
	}
	return p.Title + " · goen"
}

// current reports whether item is the page's active navigation entry. It is
// false for an empty slug so an unrelated page never highlights a category.
func (p Page) current(item NavItem) bool {
	return p.Nav != "" && p.Nav == item.Slug
}

// ariaCurrent renders the aria-current value for a navigation entry.
func (p Page) ariaCurrent(item NavItem) string {
	if p.current(item) {
		return "page"
	}
	return "false"
}

// boolAttr renders a Go bool as the literal an ARIA state attribute expects.
func boolAttr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// cartLabel names the cart link for assistive technology, which cannot read
// the count badge because the badge is decorative.
func cartLabel(count int) string {
	if count == 0 {
		return "購物車,目前是空的"
	}
	return "購物車," + strconv.Itoa(count) + " 件商品"
}
