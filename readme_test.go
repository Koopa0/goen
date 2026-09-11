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
		if len(strings.TrimSpace(string(raw))) == 0 {
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

	var screenshotHits int
	for name, body := range bodies {
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
				if info.Size() == 0 {
					t.Errorf("%s: screenshot %s is empty", name, href)
				}
				screenshotHits++
			}
		}
	}
	if screenshotHits < 2 {
		t.Errorf("both README files must embed the storefront screenshot; saw %d image refs", screenshotHits)
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
