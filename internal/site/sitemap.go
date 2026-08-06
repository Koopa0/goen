package site

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"
)

// MaxSitemapURLs bounds one sitemap document.
//
// The protocol's own limit is 50,000; goen stops at 5,000 because a catalogue
// that size needs a sitemap INDEX rather than a bigger file, and silently
// truncating past the protocol limit is worse than being visibly bounded well
// short of it.
const MaxSitemapURLs = 5000

// urlEntry is one <url> in the document.
type urlEntry struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq,omitempty"`
	Priority   string `xml:"priority,omitempty"`
}

// urlSet is the document.
type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	NS      string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

// Sitemap serves GET /sitemap.xml.
//
// Static pages, then categories, then products. Order matters a little — a
// crawler reading top-down meets the pages that explain the shop before the
// twelve hundred that are in it.
func (h *Handler) Sitemap(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.baseURL, "/")
	set := urlSet{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}

	// The pages that exist whatever the catalogue holds. Weekly rather than
	// daily: claiming a page changes more often than it does teaches a crawler
	// to ignore the hint.
	for _, p := range []struct{ path, freq, priority string }{
		{"/", "daily", "1.0"},
		{"/deals", "daily", "0.8"},
		{"/about", "monthly", "0.3"},
		{"/contact", "monthly", "0.3"},
	} {
		set.URLs = append(set.URLs, urlEntry{
			Loc: base + p.path, ChangeFreq: p.freq, Priority: p.priority,
		})
	}

	cats, err := h.catalogue.SitemapCategories(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "sitemap categories", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	for i := range cats {
		set.URLs = append(set.URLs, urlEntry{
			Loc: base + "/c/" + cats[i].Slug, LastMod: day(cats[i].UpdatedAt),
			ChangeFreq: "daily", Priority: "0.7",
		})
	}

	products, err := h.catalogue.SitemapProducts(r.Context(), MaxSitemapURLs)
	if err != nil {
		h.log.ErrorContext(r.Context(), "sitemap products", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	for i := range products {
		set.URLs = append(set.URLs, urlEntry{
			Loc: base + "/p/" + products[i].Slug, LastMod: day(products[i].UpdatedAt),
			ChangeFreq: "weekly", Priority: "0.6",
		})
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Half an hour. A sitemap is a hint, and regenerating it per crawl is a
	// full table scan per crawl.
	w.Header().Set("Cache-Control", "public, max-age=1800")
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		h.log.ErrorContext(r.Context(), "encode sitemap", "error", err)
	}
}

// Robots serves GET /robots.txt.
//
// The disallows are the pages that are per-visitor or per-order: a crawler
// following them wastes its budget on content it can never index, and an order
// page it somehow reached would be a customer's address in a search result.
func (h *Handler) Robots(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.baseURL, "/")
	body := strings.Join([]string{
		"User-agent: *",
		"Disallow: /cart",
		"Disallow: /checkout",
		"Disallow: /orders/",
		"Disallow: /account",
		"Disallow: /admin",
		"Disallow: /signin",
		"Disallow: /register",
		"Disallow: /search",
		"Disallow: /webhooks/",
		// The URL carries a one-use token. A crawler that indexed one would
		// publish it, and one that FOLLOWED it would spend somebody's
		// confirmation — which is why the write behind it is a POST.
		"Disallow: /newsletter/",
		"",
		"Sitemap: " + base + "/sitemap.xml",
		"",
	}, "\n")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(body))
	_ = r
}

// day formats a timestamp as the date part only, which is all lastmod means.
func day(t time.Time) string { return t.Format("2006-01-02") }
