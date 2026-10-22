package assets

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"path"
	"strconv"
	"testing"
)

// TestPrecompressCatalogueRoundTrips checks that every precompressible asset
// round-trips. It derives its corpus from the embedded catalogue. It deliberately
// rejects an allowlisted file that does not shrink: the outer dynamic middleware
// must never become a second compression path for a static representation omitted
// from this catalogue.
func TestPrecompressCatalogueRoundTrips(t *testing.T) {
	for name := range catalogue.digests {
		raw, err := files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !compressible[path.Ext(name)] || len(raw) < minPrecompress {
			continue
		}
		encoded, ok := catalogue.gzipped[name]
		if !ok {
			t.Errorf("%s has no gzip representation", name)
			continue
		}
		if len(encoded) >= len(raw) {
			t.Errorf("%s gzip is %d bytes, raw is %d; gzip did not shrink it", name, len(encoded), len(raw))
			continue
		}
		r, err := gzip.NewReader(bytes.NewReader(encoded))
		if err != nil {
			t.Errorf("open %s gzip: %v", name, err)
			continue
		}
		got, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Errorf("read %s gzip: read=%v close=%v", name, readErr, closeErr)
			continue
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s gzip expands to different bytes", name)
		}
	}
}

// TestPrecompressOmitsNonShrinking checks that precompress omits a file that does
// not shrink.
func TestPrecompressOmitsNonShrinking(t *testing.T) {
	raw := make([]byte, 4_096)
	state := uint32(0x9e3779b9)
	for i := 0; i < len(raw); i += 4 {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		binary.LittleEndian.PutUint32(raw[i:i+4], state)
	}
	encoded, err := precompress("fixture.txt", raw)
	if err != nil {
		t.Fatalf("precompress high-entropy fixture: %v", err)
	}
	if encoded != nil {
		t.Errorf("precompress returned %d bytes for an input that does not shrink; want no representation", len(encoded))
	}
}

// TestCompressibleExcludesCompressedFormats checks that compressible excludes
// already-compressed formats, so that they fail closed.
func TestCompressibleExcludesCompressedFormats(t *testing.T) {
	for _, ext := range []string{".webp", ".png", ".jpg", ".jpeg", ".gif", ".woff2", ".gz"} {
		if compressible[ext] {
			t.Errorf("compressible[%q] = true; already-compressed formats must fail closed", ext)
		}
	}
}

// TestCompressibleExplicitExtensions checks that every precompressible extension
// is listed explicitly and that precompress works for each.
func TestCompressibleExplicitExtensions(t *testing.T) {
	want := []string{".css", ".js", ".svg", ".json", ".xml", ".txt", ".map"}
	if len(compressible) != len(want) {
		t.Fatalf("compressible extension count = %d, want %d: %v", len(compressible), len(want), want)
	}
	raw := bytes.Repeat([]byte("x"), 1_024)
	for _, ext := range want {
		t.Run(ext, func(t *testing.T) {
			if !compressible[ext] {
				t.Fatalf("compressible[%q] = false", ext)
			}
			encoded, err := precompress("fixture"+ext, raw)
			if err != nil {
				t.Fatalf("precompress: %v", err)
			}
			if encoded == nil {
				t.Fatal("precompress returned no representation")
			}
		})
	}
}

// TestPrecompressThreshold checks that the precompression threshold is exact: 1023
// bytes are not stored, 1024 are.
func TestPrecompressThreshold(t *testing.T) {
	for _, size := range []int{1_023, 1_024} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			raw := bytes.Repeat([]byte("x"), size)
			encoded, err := precompress("fixture.txt", raw)
			if err != nil {
				t.Fatalf("precompress: %v", err)
			}
			wantStored := size == 1_024
			if got := encoded != nil; got != wantStored {
				t.Errorf("representation stored at %d bytes = %v, want %v", size, got, wantStored)
			}
		})
	}
}
