package picture_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/picture"
)

// TestImage_URLBundledKey checks that a bundled image resolves to its embedded
// file, under a versioned URL.
func TestImage_URLBundledKey(t *testing.T) {
	t.Parallel()

	const key = "koto-pad-mini-01.webp"
	got := picture.Image{BundledKey: key}.URL()
	wantPrefix := "/static/media/products/" + key + "?v="
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("Image{BundledKey: %q}.URL() = %q; want prefix %q", key, got, wantPrefix)
	}
	if digest := strings.TrimPrefix(got, wantPrefix); len(digest) != 12 {
		t.Errorf("Image{BundledKey: %q}.URL() digest = %q; want 12 characters", key, digest)
	}
}

// TestImage_SrcsetBundledKey checks that a bundled image offers its responsive
// variants: the 400 and 800 files and the full-size one, each with its width
// descriptor.
func TestImage_SrcsetBundledKey(t *testing.T) {
	t.Parallel()

	const key = "koto-pad-mini-01.webp"
	got := picture.Image{BundledKey: key}.Srcset()
	for _, want := range []string{
		"/static/media/products/koto-pad-mini-01-400.webp?",
		" 400w, ",
		"/static/media/products/koto-pad-mini-01-800.webp?",
		" 800w, ",
		"/static/media/products/koto-pad-mini-01.webp?",
		" 1600w",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Image{BundledKey: %q}.Srcset() = %q; missing %q", key, got, want)
		}
	}
}

// TestImageBundledKeyIsAFileName checks that a bundled key is a file name and not
// a path: anything else gives no URL and no srcset.
func TestImageBundledKeyIsAFileName(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"",
		".",
		"..",
		"../hero/home-hero-01.webp",
		"nested/product.webp",
		`\windows\product.webp`,
		"product.webp?download=1",
		"product.webp#fragment",
		"not-embedded.webp",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			image := picture.Image{BundledKey: key, Width: 1600}
			if got := image.URL(); got != "" {
				t.Errorf("Image{BundledKey: %q}.URL() = %q; want empty URL", key, got)
			}
			if got := image.Srcset(); got != "" {
				t.Errorf("Image{BundledKey: %q}.Srcset() = %q; want empty srcset", key, got)
			}
		})
	}
}

// TestImageDigestVersusBundledKey checks that the field, not the shape of the
// value, says where an image is served. It holds the reason Image has two fields:
// the column a key came from decides whether internal/media serves it, so a
// bundled key never reaches the media handler whatever it looks like, and an
// upload never depends on the embedded set.
func TestImageDigestVersusBundledKey(t *testing.T) {
	t.Parallel()
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	upload := picture.Image{Digest: digest, Width: 1200}
	if got := upload.URL(); got != "/media/"+digest {
		t.Errorf("Image{Digest}.URL() = %q, want /media/%s", got, digest)
	}
	if got, want := upload.Srcset(), "/media/"+digest+"/400 400w, /media/"+digest+"/800 800w, /media/"+digest+" 1200w"; got != want {
		t.Errorf("Image{Digest}.Srcset() = %q, want %q", got, want)
	}

	for _, key := range []string{digest, "aurora-edge-7-01.webp"} {
		bundled := picture.Image{BundledKey: key, Width: 1200}
		for _, got := range []string{bundled.URL(), bundled.Srcset()} {
			if strings.HasPrefix(got, "/media/") || strings.Contains(got, " /media/") {
				t.Errorf("Image{BundledKey: %q} = %q, which reached the media handler", key, got)
			}
		}
	}

	var none picture.Image
	if none.URL() != "" || none.Srcset() != "" {
		t.Errorf("the zero Image = %q, %q; want no picture", none.URL(), none.Srcset())
	}
}

// TestImage_SrcsetOfUpload checks that the srcset of an uploaded image never
// misleads the browser. A bare candidate mixed with w-descriptors is an HTML parse
// error, and the full-size image then drops out silently.
func TestImage_SrcsetOfUpload(t *testing.T) {
	const d = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	tests := []struct {
		name  string
		width int
		want  string
	}{
		{
			"a wide original offers both renditions",
			1600,
			"/media/" + d + "/400 400w, /media/" + d + "/800 800w, /media/" + d + " 1600w",
		},
		{
			"a 2400px original also offers 1600",
			2400,
			"/media/" + d + "/400 400w, /media/" + d + "/800 800w, /media/" + d + "/1600 1600w, /media/" + d + " 2400w",
		},
		{
			// 800 is not offered: it equals the original, so the rendition
			// would be the original bytes under a second URL.
			"an 800px original offers only 400",
			800,
			"/media/" + d + "/400 400w, /media/" + d + " 800w",
		},
		{
			"a small original offers only itself",
			300,
			"/media/" + d + " 300w",
		},
		{"an unknown width offers nothing", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := picture.Image{Digest: d, Width: tt.width}.Srcset()
			if got != tt.want {
				t.Errorf("Srcset = %q\n            want %q", got, tt.want)
			}
			if got == "" {
				return
			}
			for candidate := range strings.SplitSeq(got, ", ") {
				if !strings.HasSuffix(candidate, "w") || !strings.Contains(candidate, " ") {
					t.Errorf("candidate %q has no width descriptor", candidate)
				}
			}
		})
	}
}
