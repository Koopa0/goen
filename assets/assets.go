// Package assets embeds goen's static files and serves them under [Prefix].
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
	"regexp"
	"slices"
	"strconv"
	"strings"
)

//go:embed all:brand all:css all:js all:media
var files embed.FS

// Prefix is the URL path the asset handler is mounted on.
const Prefix = "/static/"

// Asset names referenced by templates. Keep in sync with [required].
const (
	DesignSystemCSS  = "css/ds/styles.css"
	AccentsCSS       = "css/ds/themes/accents.css"
	CommerceCSS      = "css/ds/packs/commerce.css"
	AppCSS           = "css/app/app.css"
	HTMXJS           = "js/vendor/htmx.min.js"
	AppJS            = "js/goen.js"
	MarkSVG          = "brand/goen-mark.svg"
	HomeHeroImage    = "media/hero/home-hero-01.webp"
	HomeHeroImage720 = "media/hero/home-hero-01-720.webp"
)

const productMediaPrefix = "media/products/"

var required = []string{
	DesignSystemCSS,
	AccentsCSS,
	CommerceCSS,
	AppCSS,
	HTMXJS,
	AppJS,
	MarkSVG,
	HomeHeroImage,
	HomeHeroImage720,
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

	for _, name := range required {
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

// HomeHeroSrcset returns the responsive hero candidates, from the compact
// mobile/tablet rendition through the full desktop source.
func HomeHeroSrcset() string {
	return URL(HomeHeroImage720) + " 720w, " + URL(HomeHeroImage) + " 1440w"
}

// ProductImageURL maps a product_images.storage_key to its public embedded
// asset URL, or "" when there is nothing to serve.
//
// Storage keys are filenames, not paths: that boundary is enforced here so
// catalogue data cannot escape the products directory or select another
// embedded asset. A key naming a file that is not embedded also yields "" —
// the catalogue can declare its imagery before the artwork exists, and a
// broken image is worse than the tile's placeholder.
func ProductImageURL(storageKey string) string {
	// An uploaded image is a 64-character sha256 served by internal/media, not
	// from here. Checked first: a digest cannot collide with an embedded
	// filename, so it must not depend on the embedded set to fail first.
	if uploadedDigest.MatchString(storageKey) {
		return "/media/" + storageKey
	}
	name := productImageName(storageKey)
	if !Has(name) {
		return ""
	}
	return URL(name)
}

// uploadedDigest is the shape internal/media gives a stored image. Spelled out
// here rather than imported: assets must not depend on a feature package.
var uploadedDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ProductImageSrcset returns the 400, 800 and 1600 pixel-wide renditions for a
// valid embedded product image. The original storage key remains the 1600px
// source; the two derived filenames are an asset-pipeline detail.
func ProductImageSrcset(storageKey string) string {
	// An uploaded image's candidates need the original's width, which only
	// ProductImageSrcsetAt is given; guessing one misleads the browser.
	if uploadedDigest.MatchString(storageKey) {
		return ""
	}
	fullName := productImageName(storageKey)
	if !Has(fullName) {
		return ""
	}

	dot := strings.LastIndexByte(storageKey, '.')
	if dot <= 0 {
		return URL(fullName) + " 1600w"
	}
	stem, ext := storageKey[:dot], storageKey[dot:]
	smallName := productMediaPrefix + stem + "-400" + ext
	mediumName := productMediaPrefix + stem + "-800" + ext
	if !Has(smallName) || !Has(mediumName) {
		return URL(fullName) + " 1600w"
	}
	return URL(smallName) + " 400w, " +
		URL(mediumName) + " 800w, " +
		URL(fullName) + " 1600w"
}

func productImageName(storageKey string) string {
	if storageKey == "" ||
		storageKey == "." ||
		storageKey == ".." ||
		strings.ContainsAny(storageKey, `/\?#`) {
		return ""
	}
	return productMediaPrefix + storageKey
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
		body, hasGzip := catalogue.gzipped[name]
		if hasGzip {
			w.Header().Add("Vary", "Accept-Encoding")
		}
		if hasGzip && acceptsGzip(r) {
			etag := `"` + digest + `-gz"`
			w.Header().Set("ETag", etag)
			w.Header().Set("Content-Type", contentType(name))
			w.Header().Set("Content-Encoding", "gzip")
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
				log.WarnContext(r.Context(), "assets: write gzip body", "name", name, "error", err)
			}
			return
		}
		w.Header().Set("ETag", `"`+digest+`"`)
		fileServer.ServeHTTP(w, r)
	}))
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

// mediaWidths is the fixed allowlist of renditions an uploaded image may be
// requested at, in ascending order.
//
// An allowlist, not a range, and that is the security property: rendering costs
// CPU proportional to the output, so an open width parameter is a denial of
// service that costs the attacker one request.
var mediaWidths = [...]int{400, 800}

// KnownWidth reports whether w is a rendition goen will produce.
func KnownWidth(w int) bool { return slices.Contains(mediaWidths[:], w) }

// MediaURL is where an uploaded image is served at full size.
func MediaURL(digest string) string { return "/media/" + digest }

// MediaRenditionURL is where it is served at one of the allowed media widths.
func MediaRenditionURL(digest string, width int) string {
	return "/media/" + digest + "/" + strconv.Itoa(width)
}

// mediaSrcset is the candidate set for an uploaded image, derived from the
// digest and the original's width alone.
//
// Two rules, both of which MISLEAD a browser rather than merely wasting bytes
// when broken:
//
//   - Every candidate carries a `w` descriptor. HTML allows all-w, all-x, or a
//     single bare candidate; a bare one mixed with `w` candidates is a parse
//     error, and the full-size image drops silently out of the set.
//   - A rendition appears only if it is NARROWER than the original. goen never
//     upscales, so an 800w candidate for a 600px image would serve 600 pixels
//     while telling the browser it received 800 — and the browser chooses and
//     lays out on that number.
func mediaSrcset(digest string, originalWidth int) string {
	if originalWidth <= 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range mediaWidths {
		if w >= originalWidth {
			continue
		}
		b.WriteString(MediaRenditionURL(digest, w))
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(w))
		b.WriteString("w, ")
	}
	b.WriteString(MediaURL(digest))
	b.WriteString(" ")
	b.WriteString(strconv.Itoa(originalWidth))
	b.WriteString("w")
	return b.String()
}

// ProductImageSrcsetAt is the candidate set for a product image whose width is
// known. Two kinds of key reach it: an uploaded image is a sha256 digest served
// by internal/media, a seeded one an embedded filename with pre-built
// renditions.
func ProductImageSrcsetAt(storageKey string, originalWidth int) string {
	if uploadedDigest.MatchString(storageKey) {
		return mediaSrcset(storageKey, originalWidth)
	}
	return ProductImageSrcset(storageKey)
}
