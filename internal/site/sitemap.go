package site

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
)

// MaxSitemapURLs bounds the single sitemap document this service emits. The
// protocol permits more, but this endpoint intentionally caps database work.
const MaxSitemapURLs = 5000

type urlEntry struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq,omitempty"`
	Priority   string `xml:"priority,omitempty"`
}

type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	NS      string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

// Sitemap serves GET /sitemap.xml.
func (h *Handler) Sitemap(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.baseURL, "/")
	set := urlSet{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}

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

	remaining := int32(MaxSitemapURLs)
	for range set.URLs {
		remaining--
	}
	cats, err := h.catalogue.SitemapCategories(r.Context(), remaining)
	if err != nil {
		h.log.ErrorContext(r.Context(), "sitemap categories", "error", err)
		http.Error(w, "500", http.StatusInternalServerError)
		return
	}
	for i := range cats {
		if remaining == 0 {
			break
		}
		set.URLs = append(set.URLs, urlEntry{
			Loc: base + "/c/" + cats[i].Slug, LastMod: day(cats[i].UpdatedAt),
			ChangeFreq: "daily", Priority: "0.7",
		})
		remaining--
	}

	if remaining > 0 {
		products, err := h.catalogue.SitemapProducts(r.Context(), remaining)
		if err != nil {
			h.log.ErrorContext(r.Context(), "sitemap products", "error", err)
			http.Error(w, "500", http.StatusInternalServerError)
			return
		}
		for i := range products {
			if remaining == 0 {
				break
			}
			set.URLs = append(set.URLs, urlEntry{
				Loc: base + "/p/" + products[i].Slug, LastMod: day(products[i].UpdatedAt),
				ChangeFreq: "weekly", Priority: "0.6",
			})
			remaining--
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
		// These URLs carry a one-use token a crawler would publish or spend.
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
func day(t time.Time) string { return shoptime.Day(t) }
