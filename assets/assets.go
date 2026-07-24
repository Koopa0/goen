// Package assets embeds goen's static files and serves them under [Prefix].
//
// Every asset URL carries a ?v= content digest so a changed file is fetched
// immediately while an unchanged one stays in the browser cache for a year.
// The names templates reference are declared as constants here and verified
// against the embedded filesystem at start-up, so a renamed or deleted asset
// stops the binary instead of shipping a dead link.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
)

//go:embed all:brand all:css all:js
var files embed.FS

// Prefix is the URL path the asset handler is mounted on.
const Prefix = "/static/"

// Asset names referenced by templates. Keep in sync with [required].
const (
	DesignSystemCSS = "css/ds/styles.css"
	AccentsCSS      = "css/ds/themes/accents.css"
	CommerceCSS     = "css/ds/packs/commerce.css"
	AppCSS          = "css/app/app.css"
	HTMXJS          = "js/vendor/htmx.min.js"
	AppJS           = "js/goen.js"
	MarkSVG         = "brand/goen-mark.svg"
)

var required = []string{
	DesignSystemCSS,
	AccentsCSS,
	CommerceCSS,
	AppCSS,
	HTMXJS,
	AppJS,
	MarkSVG,
}

var digests = map[string]string{}

func init() {
	if err := index(); err != nil {
		panic("assets: " + err.Error())
	}
}

func index() error {
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := files.Open(name)
		if err != nil {
			return err
		}
		defer closeQuietly(f)

		sum := sha256.New()
		if _, err := io.Copy(sum, f); err != nil {
			return fmt.Errorf("hash %s: %w", name, err)
		}
		digests[name] = hex.EncodeToString(sum.Sum(nil))[:12]
		return nil
	})
	if err != nil {
		return err
	}

	for _, name := range required {
		if _, ok := digests[name]; !ok {
			return fmt.Errorf("required asset %s is not embedded", name)
		}
	}
	return nil
}

// closeQuietly closes a file opened only for reading. A failure there cannot
// affect the digest already computed, but swallowing it silently would hide a
// filesystem going wrong, so it is reported and the walk continues.
func closeQuietly(f fs.File) {
	if err := f.Close(); err != nil {
		slog.Warn("assets: close embedded file", "error", err)
	}
}

// URL returns the versioned public URL for an embedded asset. An unknown name
// yields an unversioned URL, which the handler answers with 404 rather than a
// silently stale response.
func URL(name string) string {
	if d, ok := digests[name]; ok {
		return Prefix + name + "?v=" + d
	}
	return Prefix + name
}

// Handler serves the embedded assets. It answers only files: a directory path
// is a 404 rather than a listing.
func Handler() http.Handler {
	fileServer := http.FileServerFS(files)
	strip := strings.TrimSuffix(Prefix, "/")

	return http.StripPrefix(strip, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		digest, known := digests[name]
		if !known {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("v") == digest {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	}))
}
