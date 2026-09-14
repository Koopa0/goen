package readme_test

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// markdownRef is an inline link or image. Reference-style links are not used
// in these two files; if they appear, this guard will not see them and a
// dangling dest would land.
var markdownRef = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)]+)\)`)

// Linked badges have two destinations; flatten them before checking links so
// the badge's click target receives the same check as its image URL.
var linkedImage = regexp.MustCompile(`\[(!\[[^\]]*\]\([^)]+\))\]\(([^)]+)\)`)

// TestBothReadmesResolveTheirLinks holds the clone's front door to the files
// it names. A reciprocal language switch or screenshot path that 404s is a
// broken first impression, and GitHub will not tell the gate.
func TestBothReadmesResolveTheirLinks(t *testing.T) {
	t.Parallel()

	files := []string{"README.md", "README.zh-TW.md"}
	bodies := make(map[string]string, len(files))
	for _, name := range files {
		//nolint:gosec // G304: names are the two README files in this repository
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) == "" {
			t.Fatalf("%s is empty", name)
		}
		bodies[name] = string(raw)
	}

	if !strings.Contains(bodies["README.md"], "](README.zh-TW.md)") {
		t.Error("README.md has no visible link to README.zh-TW.md")
	}
	if !strings.Contains(bodies["README.zh-TW.md"], "](README.md)") {
		t.Error("README.zh-TW.md has no visible link to README.md")
	}

	for name, body := range bodies {
		wantScreenshot := "assets/readme/storefront.en.png"
		if name == "README.zh-TW.md" {
			wantScreenshot = "assets/readme/storefront.zh-TW.png"
		}
		var screenshotHits int
		body = linkedImage.ReplaceAllString(body, "$1 [badge]($2)")
		for _, m := range markdownRef.FindAllStringSubmatch(body, -1) {
			dest := strings.TrimSpace(m[2])
			if dest == "" {
				t.Errorf("%s: empty link destination", name)
				continue
			}
			href, frag, _ := strings.Cut(dest, "#")
			href = strings.TrimSpace(href)
			if href == "" {
				// In-page anchor only.
				_ = frag
				continue
			}
			if looksExternal(href) {
				if err := requireHTTPS(href); err != nil {
					t.Errorf("%s: %s: %v", name, href, err)
				}
				continue
			}
			if strings.Contains(href, "://") || strings.HasPrefix(href, "//") {
				t.Errorf("%s: %s is not a relative path or https URL", name, href)
				continue
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(name), href))
			if strings.HasPrefix(target, "..") {
				t.Errorf("%s: %s escapes the repository", name, href)
				continue
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Errorf("%s: %s does not exist", name, href)
				continue
			}
			if info.IsDir() {
				t.Errorf("%s: %s is a directory", name, href)
				continue
			}
			if isScreenshot(href) {
				if !strings.HasPrefix(m[0], "![") || href != wantScreenshot {
					t.Errorf("%s: must embed its locale screenshot %s, got %s", name, wantScreenshot, m[0])
				}
				if info.Size() == 0 {
					t.Errorf("%s: screenshot %s is empty", name, href)
				}
				screenshotHits++
			}
		}
		if screenshotHits != 1 {
			t.Errorf("%s must embed exactly one locale storefront screenshot; saw %d refs", name, screenshotHits)
		}
	}
}

func TestReadmeMetadata(t *testing.T) {
	t.Parallel()

	for name, language := range map[string]string{
		"README.md":       "English | [繁體中文](README.zh-TW.md)",
		"README.zh-TW.md": "[English](README.md) | 繁體中文",
	} {
		//nolint:gosec // G304: names are the two README files in this repository
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if !strings.Contains(body, "\n"+language+"\n") {
			t.Errorf("%s: missing active-language label and reciprocal link", name)
		}
		badges := linkedImage.FindAllString(body, -1)
		want := []string{
			"[![verify](https://github.com/Koopa0/goen/actions/workflows/verify.yml/badge.svg)](https://github.com/Koopa0/goen/actions/workflows/verify.yml)",
			"[![Go](https://img.shields.io/github/go-mod/go-version/Koopa0/goen)](go.mod)",
			"[![Apache-2.0](https://img.shields.io/badge/Apache--2.0-blue)](LICENSE)",
		}
		if len(badges) != len(want) {
			t.Errorf("%s: got %d linked badges, want %d", name, len(badges), len(want))
		}
		for _, badge := range want {
			if !strings.Contains(body, badge) {
				t.Errorf("%s: missing approved badge %s", name, badge)
			}
		}
	}
}

func looksExternal(href string) bool {
	return strings.HasPrefix(href, "https://") ||
		strings.HasPrefix(href, "http://") ||
		strings.HasPrefix(href, "mailto:")
}

func requireHTTPS(href string) error {
	if strings.HasPrefix(href, "mailto:") {
		return nil
	}
	u, err := url.Parse(href)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" {
		return errNotHTTPS
	}
	return nil
}

var errNotHTTPS = errors.New("external link must be https")

func isScreenshot(href string) bool {
	base := strings.ToLower(filepath.Base(href))
	return strings.HasPrefix(base, "storefront.") &&
		(strings.HasSuffix(base, ".png") ||
			strings.HasSuffix(base, ".webp") ||
			strings.HasSuffix(base, ".jpg") ||
			strings.HasSuffix(base, ".jpeg"))
}

// The product introduction excludes schema inventories in both languages.
func TestReadmesDoNotPublishSchemaInventories(t *testing.T) {
	t.Parallel()

	inventory := regexp.MustCompile("(?i)CHECK constraints|foreign keys|unique indexes|rule triggers|`CHECK`|外鍵|規則觸發器")
	for _, name := range []string{"README.md", "README.zh-TW.md"} {
		//nolint:gosec // G304: names are the two README files in this repository
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Match(raw) {
			t.Errorf("%s contains a schema inventory instead of product information", name)
		}
	}
}
