package assets

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// compressible is an allowlist: a newly embedded image must default to its
// original representation rather than spending CPU to make it larger.
var compressible = map[string]bool{
	".css":  true,
	".js":   true,
	".svg":  true,
	".json": true,
	".xml":  true,
	".txt":  true,
	".map":  true,
}

// minPrecompress is the size below which gzip's envelope and a second
// representation are not worthwhile.
const minPrecompress = 1024

func precompress(name string, raw []byte) ([]byte, error) {
	if !compressible[path.Ext(name)] || len(raw) < minPrecompress {
		return nil, nil
	}
	var out bytes.Buffer
	w, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("build gzip writer for %s: %w", name, err)
	}
	if _, err := w.Write(raw); err != nil {
		return nil, fmt.Errorf("compress %s: %w", name, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("finish compressing %s: %w", name, err)
	}
	if out.Len() < len(raw) {
		return bytes.Clone(out.Bytes()), nil
	}
	return nil, nil
}

func contentType(name string) string {
	if typ := mime.TypeByExtension(path.Ext(name)); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

func acceptsGzip(r *http.Request) bool {
	var explicit, wildcard bool
	var gzipQ, wildcardQ float64
	for entry := range strings.SplitSeq(strings.Join(r.Header.Values("Accept-Encoding"), ","), ",") {
		coding, quality, ok := parseEncodingPreference(entry)
		if !ok {
			continue
		}
		switch coding {
		case "gzip":
			explicit = true
			if quality > gzipQ {
				gzipQ = quality
			}
		case "*":
			wildcard = true
			if quality > wildcardQ {
				wildcardQ = quality
			}
		}
	}
	if explicit {
		return gzipQ > 0
	}
	return wildcard && wildcardQ > 0
}

func parseEncodingPreference(entry string) (coding string, quality float64, ok bool) {
	parts := strings.Split(entry, ";")
	coding = strings.ToLower(strings.TrimSpace(parts[0]))
	if coding != "gzip" && coding != "*" {
		return "", 0, false
	}
	return coding, parseQuality(parts[1:]), true
}

func parseQuality(params []string) float64 {
	quality := 1.0
	for _, param := range params {
		key, value, ok := strings.Cut(param, "=")
		if !ok {
			return 0
		}
		if !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || parsed < 0 || parsed > 1 {
			return 0
		}
		quality = parsed
	}
	return quality
}
