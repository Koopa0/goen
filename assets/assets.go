// Package assets is goen's static files, embedded in the binary and served
// under [Prefix]; it holds the stylesheets, scripts, fonts, brand marks and
// bundled photographs with their versioned URLs; it does not hold an image a
// page shows or the URLs of uploads, which are package picture.
//
// Every asset URL carries a ?v= content digest, so a changed file is fetched
// immediately while an unchanged one stays cached for a year. The names
// templates reference are declared as constants here and verified against the
// embedded filesystem at start-up, so a renamed asset stops the binary.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

//go:embed all:brand all:css all:fonts all:js all:media all:speculation
var files embed.FS

const Prefix = "/static/"

// Asset names referenced by templates. Keep in sync with [required].
const (
	// BaseCSS carries the tokens, the element defaults and the shared
	// primitives, and is linked before AppCSS and AdminCSS: a value set in
	// either is settled by source order.
	BaseCSS = "css/app/base.css"
	// AppCSS is the storefront's stylesheet; AdminCSS is the back office's,
	// and the back office does not link AppCSS.
	AppCSS   = "css/app/app.css"
	AdminCSS = "css/app/admin.css"
	FontsCSS = "css/app/fonts.css"
	// InstrumentSansLatinWOFF2 is the one face worth a preload: every page
	// paints Latin before it paints anything else, and the browser cannot
	// discover a font until it has parsed the stylesheet that names it.
	InstrumentSansLatinWOFF2 = "fonts/instrument-sans/latin.woff2"
	// SpeculationRules is the document the Speculation-Rules header names. It
	// is an asset rather than an inline <script type="speculationrules">
	// because the policy admits no inline script, and it is one document for
	// every page because the rules are about where a link goes, not about
	// which page the visitor is on.
	SpeculationRules = "speculation/rules.json"
	HTMXJS           = "js/vendor/htmx.min.js"
	AppJS            = "js/goen.js"
	// TransitionsJS is not deferred: pagereveal fires at the first render, and
	// a deferred script can run after it.
	TransitionsJS = "js/transitions.js"
	MarkSVG       = "brand/goen-mark.svg"
	// FaviconSVG is the mark for a browser tab. It carries a dark-scheme rule
	// the page-level MarkSVG must not: the rule follows the operating system,
	// not the page, so on a light page under a dark OS the ring would vanish.
	FaviconSVG = "brand/favicon.svg"
	// FaviconICO is served at /favicon.ico, where a browser that finds no
	// <link rel="icon"> asks; AppleTouchIcon is the home-screen icon. Both are
	// PNG on a white ground, because neither honours a dark-mode rule.
	FaviconICO       = "brand/favicon.ico"
	AppleTouchIcon   = "brand/apple-touch-icon.png"
	HomeHeroImage    = "media/hero/home-hero-01.webp"
	HomeHeroImage720 = "media/hero/home-hero-01-720.webp"
	// EmailHeader heads every HTML letter. PNG, not WebP: a mail client
	// renders what it can decode, and not every one decodes WebP.
	EmailHeader = "brand/email-header.png"
	// OGDefaultImage is the share-preview picture of a page that has none of
	// its own. PNG because link-preview crawlers read it more reliably than
	// WebP.
	OGDefaultImage = "brand/og-default.png"
)

// OG image geometry, stated in the tags so a crawler need not fetch it first.
const (
	OGDefaultWidth  = 1200
	OGDefaultHeight = 630
)

// requiredMedia is the photography the storefront names by file: the six
// department photographs (1600px, each with a -800 and a -400 rendition) and
// the campaign headers. The headers sit under media/products/ because a
// campaign's bundled_key is resolved as a product photograph's is, by
// picture.Image.
var requiredMedia = append(departmentMedia(
	"books-stationery", "home-living", "beauty", "fashion", "food-drink", "tech",
),
	"media/products/campaign-autumn.webp",
	"media/products/campaign-autumn-400.webp",
	"media/products/campaign-autumn-800.webp",
	"media/products/campaign-tea-week.webp",
	"media/products/campaign-tea-week-400.webp",
	"media/products/campaign-tea-week-800.webp",
)

// departmentMedia lists a department photograph's three files: the 1600px
// source and its -800 and -400 renditions.
func departmentMedia(slugs ...string) []string {
	names := make([]string, 0, 3*len(slugs))
	for _, slug := range slugs {
		base := "media/products/department-" + slug
		names = append(names, base+".webp", base+"-800.webp", base+"-400.webp")
	}
	return names
}

var required = []string{
	BaseCSS,
	AppCSS,
	AdminCSS,
	FontsCSS,
	InstrumentSansLatinWOFF2,
	SpeculationRules,
	HTMXJS,
	AppJS,
	TransitionsJS,
	MarkSVG,
	FaviconSVG,
	FaviconICO,
	AppleTouchIcon,
	HomeHeroImage,
	HomeHeroImage720,
	EmailHeader,
	OGDefaultImage,
}

type assetIndex struct {
	digests map[string]string
	gzipped map[string][]byte
}

var catalogue = mustIndex()

func mustIndex() assetIndex {
	indexed, err := index()
	if err != nil {
		// The embedded corpus cannot be repaired after the process starts.
		panic("assets: " + err.Error())
	}
	return indexed
}

func index() (assetIndex, error) {
	indexed := assetIndex{
		digests: make(map[string]string),
		gzipped: make(map[string][]byte),
	}
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(raw)
		indexed.digests[name] = hex.EncodeToString(sum[:])[:12]
		encoded, err := precompress(name, raw)
		if err != nil {
			return err
		}
		if encoded != nil {
			indexed.gzipped[name] = encoded
		}
		return nil
	})
	if err != nil {
		return assetIndex{}, err
	}

	for _, name := range slices.Concat(required, requiredMedia) {
		if _, ok := indexed.digests[name]; !ok {
			return assetIndex{}, fmt.Errorf("required asset %s is not embedded", name)
		}
	}
	return indexed, nil
}

// URL returns the versioned public URL for an embedded asset. An unknown name
// yields an unversioned URL, which the handler answers with 404 rather than a
// silently stale response.
func URL(name string) string {
	if d, ok := catalogue.digests[name]; ok {
		return Prefix + name + "?v=" + d
	}
	return Prefix + name
}

// Has reports whether name is an embedded asset, so a caller that can render a
// placeholder instead does not emit a URL the handler will 404.
func Has(name string) bool {
	_, ok := catalogue.digests[name]
	return ok
}

// Handler serves the embedded assets. It answers only files: a directory path
// is a 404 rather than a listing.
func Handler(log *slog.Logger) http.Handler {
	if log == nil {
		panic("assets: Handler requires a logger")
	}
	fileServer := http.FileServerFS(files)
	strip := strings.TrimSuffix(Prefix, "/")

	return http.StripPrefix(strip, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		digest, known := catalogue.digests[name]
		if !known {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("v") == digest {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		gzipped, hasGzip := catalogue.gzipped[name]
		if hasGzip {
			w.Header().Add("Vary", "Accept-Encoding")
		}
		if hasGzip && acceptsGzip(r) {
			w.Header().Set("Content-Encoding", "gzip")
			writeBody(w, r, log, name, `"`+digest+`-gz"`, gzipped)
			return
		}
		w.Header().Set("ETag", `"`+digest+`"`)
		// Set here rather than left to the file server, which would sniff the
		// extension and answer application/json. A browser refuses a
		// speculation rules document that does not arrive as
		// application/speculationrules+json, and that check is what stops any
		// JSON a site happens to serve from becoming rules. ServeContent keeps
		// a Content-Type that is already set.
		w.Header().Set("Content-Type", contentType(name))
		fileServer.ServeHTTP(w, r)
	}))
}

// Alias serves one embedded asset at the path it is mounted on, for a file a
// client asks for by a fixed name, as it does /favicon.ico.
func Alias(log *slog.Logger, name string) http.Handler {
	assetHandler := Handler(log)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aliased := r.Clone(r.Context())
		aliased.URL = &url.URL{Path: Prefix + name}
		assetHandler.ServeHTTP(w, aliased)
	})
}

// writeBody answers with bytes this package is holding rather than with a file,
// which is the case for a precompressed representation. It owns the conditional
// request and the HEAD, so those two answers cannot drift apart between the
// callers.
func writeBody(
	w http.ResponseWriter, r *http.Request, log *slog.Logger, name, etag string, body []byte,
) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", contentType(name))
	if ifNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(body); err != nil {
		log.WarnContext(r.Context(), "assets: write body", "name", name, "error", err)
	}
}

func ifNoneMatch(r *http.Request, etag string) bool {
	for _, line := range r.Header.Values("If-None-Match") {
		for candidate := range strings.SplitSeq(line, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
				return true
			}
		}
	}
	return false
}
