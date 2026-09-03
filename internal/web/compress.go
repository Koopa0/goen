package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	minCompress      = 1024
	noCompressHeader = "X-Goen-No-Compress"
)

var compressibleTypes = map[string]bool{
	"text/html":              true,
	"text/css":               true,
	"text/plain":             true,
	"text/javascript":        true,
	"application/javascript": true,
	"application/json":       true,
	"application/xml":        true,
	"text/xml":               true,
	"image/svg+xml":          true,
}

var gzipWriterPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// Compress answers a client that asked for gzip with gzip, for response types
// where it pays. The decision waits for the content type, status and enough of
// the body to know whether the gzip envelope is worthwhile.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &compressor{
			ResponseWriter: w,
			acceptsGzip:    acceptsGzip(r.Header.Values("Accept-Encoding")),
			status:         http.StatusOK,
		}
		returned := false
		defer func() {
			if !returned {
				// Panic recovery is outside this middleware. Do not close a
				// half-written gzip stream, but never let the private marker leak.
				c.Header().Del(noCompressHeader)
			}
		}()
		next.ServeHTTP(c, r)
		returned = true
		c.close()
	})
}

// NoCompress opts a handler out of dynamic compression. Secret-bearing pages
// use it to avoid a future reflection channel; handlers that negotiate their
// own representations use it to keep that decision authoritative. It must be
// called before the first Write or WriteHeader. The private marker is removed
// before any response header reaches the client.
func NoCompress(w http.ResponseWriter) {
	w.Header().Set(noCompressHeader, "1")
}

type compressor struct {
	http.ResponseWriter

	acceptsGzip  bool
	status       int
	headerCalled bool
	decided      bool

	buf      bytes.Buffer
	zw       *gzip.Writer
	writeErr error
}

func (c *compressor) WriteHeader(status int) {
	if c.headerCalled {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		// Informational responses do not commit the final status or encoding.
		// Keep the private opt-out marker off the wire while forwarding them.
		h := c.Header()
		marker, marked := h[noCompressHeader]
		delete(h, noCompressHeader)
		c.ResponseWriter.WriteHeader(status)
		if marked {
			h[noCompressHeader] = marker
		}
		return
	}
	c.headerCalled = true
	c.status = status
	if !c.couldCompress() {
		c.decide(false, false)
	}
}

func (c *compressor) Write(p []byte) (int, error) {
	if !c.headerCalled {
		c.headerCalled = true
		c.status = http.StatusOK
	}
	if c.decided {
		return c.write(p)
	}
	if !c.couldCompress() {
		c.decide(false, false)
		return c.write(p)
	}
	_, _ = c.buf.Write(p)
	if c.buf.Len() < minCompress {
		return len(p), nil
	}
	c.decide(c.acceptsGzip, true)
	if err := c.writeBuffered(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// FlushError lets ResponseController preserve streaming through the wrapper.
// A flush before the threshold commits the response as identity: holding those
// bytes back would violate the handler's explicit request to send them now.
func (c *compressor) FlushError() error {
	if !c.headerCalled {
		c.headerCalled = true
		c.status = http.StatusOK
	}
	if !c.decided {
		large := c.buf.Len() >= minCompress && c.couldCompress()
		c.decide(large && c.acceptsGzip, large)
		if err := c.writeBuffered(); err != nil {
			return err
		}
	}
	if c.zw != nil {
		if err := c.zw.Flush(); err != nil {
			c.writeErr = err
			return err
		}
	}
	return http.NewResponseController(c.ResponseWriter).Flush()
}

func (c *compressor) Flush() {
	if err := c.FlushError(); err != nil {
		c.writeErr = err
	}
}

// Unwrap lets ResponseController reach capabilities this wrapper does not
// implement itself, such as deadlines, full duplex and connection hijacking.
func (c *compressor) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *compressor) couldCompress() bool {
	if c.status < 200 || c.status == http.StatusNoContent || c.status == http.StatusPartialContent ||
		c.status == http.StatusNotModified {
		return false
	}
	h := c.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" || h.Get(noCompressHeader) != "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && compressibleTypes[mediaType]
}

func (c *compressor) decide(compress, vary bool) {
	if c.decided {
		return
	}
	c.decided = true
	h := c.Header()
	h.Del(noCompressHeader)
	if vary {
		addVary(h, "Accept-Encoding")
	}
	if compress {
		h.Del("Content-Length")
		h.Del("Accept-Ranges")
		h.Set("Content-Encoding", "gzip")
		if etag := h.Get("ETag"); etag != "" {
			h.Set("ETag", gzipETag(etag))
		}
		c.zw = takeGzipWriter(c.ResponseWriter)
	}
	if c.headerCalled {
		c.ResponseWriter.WriteHeader(c.status)
	}
}

func (c *compressor) write(p []byte) (int, error) {
	if c.zw != nil {
		n, err := c.zw.Write(p)
		if err != nil {
			c.writeErr = err
		}
		return n, err
	}
	return c.ResponseWriter.Write(p)
}

func (c *compressor) writeBuffered() error {
	if c.buf.Len() == 0 {
		return nil
	}
	body := c.buf.Bytes()
	_, err := c.write(body)
	c.buf.Reset()
	return err
}

func (c *compressor) close() {
	if !c.decided {
		if !c.headerCalled {
			// A handler that wrote nothing still must not leak the private marker.
			c.Header().Del(noCompressHeader)
			return
		}
		large := c.buf.Len() >= minCompress && c.couldCompress()
		c.decide(large && c.acceptsGzip, large)
		if err := c.writeBuffered(); err != nil {
			c.writeErr = err
		}
	}
	if c.zw == nil {
		return
	}
	err := c.zw.Close()
	if c.writeErr == nil && err == nil {
		c.zw.Reset(io.Discard)
		gzipWriterPool.Put(c.zw)
	}
	c.zw = nil
}

func gzipETag(etag string) string {
	if strings.HasSuffix(etag, `"`) && (strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, `W/"`)) {
		return strings.TrimSuffix(etag, `"`) + `-gz"`
	}
	return etag
}

func addVary(h http.Header, value string) {
	for _, line := range h.Values("Vary") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}

func takeGzipWriter(dst io.Writer) *gzip.Writer {
	zw, ok := gzipWriterPool.Get().(*gzip.Writer)
	if !ok {
		return gzip.NewWriter(dst)
	}
	zw.Reset(dst)
	return zw
}

// acceptsGzip parses the coding and quality values. An explicit gzip entry is
// more specific than a wildcard, so gzip;q=0 refuses it even beside *;q=1.
func acceptsGzip(lines []string) bool {
	var explicit, wildcard bool
	var gzipQ, wildcardQ float64
	for entry := range strings.SplitSeq(strings.Join(lines, ","), ",") {
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

var (
	_ http.Flusher                    = (*compressor)(nil)
	_ interface{ FlushError() error } = (*compressor)(nil)
)
