package web

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHTMLIsCompressedForAClientThatAcceptsIt(t *testing.T) {
	body := []byte(strings.Repeat("<p>goen</p>", 3_000))
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))

	res := serveCompressed(t, h, "gzip")
	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want absent after compression", got)
	}
	if !variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q, want Accept-Encoding", res.Header().Values("Vary"))
	}
	if got := gunzip(t, res.Body.Bytes()); !bytes.Equal(got, body) {
		t.Error("gunzipped HTML differs from the handler body")
	}
}

func TestAClientThatDoesNotAskGetsIdentityAndVary(t *testing.T) {
	body := []byte(strings.Repeat("identity ", 2_000))
	h := fixedResponse("text/html; charset=utf-8", body)

	res := serveCompressed(t, Compress(h), "")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity", got)
	}
	if !bytes.Equal(res.Body.Bytes(), body) {
		t.Error("identity response differs from the handler body")
	}
	// This selected response still has a gzip alternative for another request.
	// Without Vary a shared cache can pin identity for clients that accept gzip.
	if !variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q, want Accept-Encoding on large identity", res.Header().Values("Vary"))
	}
}

func TestGzipNegotiationHonoursQualityAndWildcard(t *testing.T) {
	body := []byte(strings.Repeat("negotiated ", 2_000))
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "gzip", header: "br, gzip", want: true},
		{name: "case insensitive", header: "GZip; Q=0.5", want: true},
		{name: "wildcard", header: "br, *;q=0.2", want: true},
		{name: "q zero", header: "gzip;q=0", want: false},
		{name: "explicit zero beats wildcard", header: "*;q=1, gzip;q=0", want: false},
		{name: "wildcard zero", header: "*;q=0", want: false},
		{name: "invalid quality", header: "gzip;q=wat", want: false},
		{name: "missing quality value", header: "gzip;q", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := serveCompressed(t, Compress(fixedResponse("text/plain", body)), tt.header)
			if got := res.Header().Get("Content-Encoding") == "gzip"; got != tt.want {
				t.Errorf("gzip selected = %v, want %v (Content-Encoding %q)",
					got, tt.want, res.Header().Get("Content-Encoding"))
			}
		})
	}
}

func TestEveryCompressibleMediaTypeIsExplicitAndWorks(t *testing.T) {
	want := []string{
		"text/html",
		"text/css",
		"text/plain",
		"text/javascript",
		"application/javascript",
		"application/json",
		"application/xml",
		"text/xml",
		"image/svg+xml",
	}
	if len(compressibleTypes) != len(want) {
		t.Fatalf("compressible type count = %d, want %d: %v", len(compressibleTypes), len(want), want)
	}
	body := bytes.Repeat([]byte("x"), 1_024)
	for _, mediaType := range want {
		t.Run(mediaType, func(t *testing.T) {
			if !compressibleTypes[mediaType] {
				t.Fatalf("compressibleTypes[%q] = false", mediaType)
			}
			res := serveCompressed(t, Compress(fixedResponse(mediaType, body)), "gzip")
			if got := res.Header().Get("Content-Encoding"); got != "gzip" {
				t.Errorf("Content-Encoding = %q, want gzip", got)
			}
		})
	}
}

func TestCompressionThresholdIsExact(t *testing.T) {
	for _, size := range []int{1_023, 1_024} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), size)
			res := serveCompressed(t, Compress(fixedResponse("text/plain", body)), "gzip")
			wantGzip := size == 1_024
			if got := res.Header().Get("Content-Encoding") == "gzip"; got != wantGzip {
				t.Errorf("gzip selected at %d bytes = %v, want %v", size, got, wantGzip)
			}
			if got := variesOnAcceptEncoding(res.Header()); got != wantGzip {
				t.Errorf("Accept-Encoding Vary at %d bytes = %v, want %v", size, got, wantGzip)
			}
		})
	}
}

func TestAResponseThatAlreadyCarriesAnEncodingIsLeftAlone(t *testing.T) {
	plaintxt := pseudoRandomBytes(30_000)
	encoded := gzipBytes(t, plaintxt)
	if len(encoded) < minCompress {
		t.Fatalf("encoded fixture is only %d bytes; it would not exercise the double-gzip guard", len(encoded))
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		_, _ = w.Write(encoded)
	})

	res := serveCompressed(t, Compress(h), "gzip")
	if got := gunzip(t, res.Body.Bytes()); !bytes.Equal(got, plaintxt) {
		t.Error("response was changed or compressed twice")
	}
	if bytes.HasPrefix(gunzip(t, res.Body.Bytes()), []byte{0x1f, 0x8b}) {
		t.Error("one gunzip left another gzip stream")
	}
}

func TestAnImageResponseIsNotCompressed(t *testing.T) {
	body := bytes.Repeat([]byte{0x52}, 30_000)
	res := serveCompressed(t, Compress(fixedResponse("image/webp", body)), "gzip")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity for image/webp", got)
	}
	if !bytes.Equal(res.Body.Bytes(), body) {
		t.Error("image response changed")
	}
}

func TestASmallResponseIsNotCompressed(t *testing.T) {
	body := bytes.Repeat([]byte("s"), 200)
	res := serveCompressed(t, Compress(fixedResponse("text/html", body)), "gzip")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity below threshold", got)
	}
	if variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q; a short response has no gzip representation", res.Header().Values("Vary"))
	}
	if !bytes.Equal(res.Body.Bytes(), body) {
		t.Error("small response changed")
	}
}

func TestWritesThatCrossTheThresholdAreCompressedTogether(t *testing.T) {
	first := bytes.Repeat([]byte("a"), 600)
	second := bytes.Repeat([]byte("b"), 600)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(first)
		_, _ = w.Write(second)
	})
	res := serveCompressed(t, Compress(h), "gzip")
	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip after writes crossed threshold", got)
	}
	if got, want := gunzip(t, res.Body.Bytes()), append(append([]byte{}, first...), second...); !bytes.Equal(got, want) {
		t.Error("the buffered writes did not round-trip")
	}
}

func TestAStatusWithNoBodyEmitsNoGzipEnvelope(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(status)
				// Even a broken handler cannot turn a bodyless status into a gzip
				// envelope; net/http will reject these bytes at the real writer.
				_, _ = w.Write(bytes.Repeat([]byte("x"), 2_000))
			})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			req.Header.Set("Accept-Encoding", "gzip")
			res := &bodylessResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
			Compress(h).ServeHTTP(res, req)
			if res.Body.Len() != 0 {
				t.Errorf("body is %d bytes, want empty", res.Body.Len())
			}
			if got := res.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want absent", got)
			}
		})
	}
}

func TestAPartialResponseIsNotCompressed(t *testing.T) {
	body := bytes.Repeat([]byte("range "), 2_000)
	tests := []struct {
		name         string
		status       int
		contentRange bool
	}{
		{name: "partial status", status: http.StatusPartialContent},
		{name: "content range on success", status: http.StatusOK, contentRange: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if tt.contentRange {
					w.Header().Set("Content-Range", "bytes 0-11999/24000")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write(body)
			})
			res := serveCompressed(t, Compress(h), "gzip")
			if got := res.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want identity for Content-Range", got)
			}
			if !bytes.Equal(res.Body.Bytes(), body) {
				t.Error("partial response body changed")
			}
		})
	}
}

func TestInformationalStatusDoesNotCommitTheFinalResponse(t *testing.T) {
	body := bytes.Repeat([]byte("early hints "), 2_000)
	underlying := newInformationalWriter()
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</static/css/app/app.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(underlying, req)

	if want := []int{http.StatusEarlyHints, http.StatusOK}; !slices.Equal(underlying.statuses, want) {
		t.Errorf("statuses = %v, want %v", underlying.statuses, want)
	}
	if got := underlying.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip on the final response", got)
	}
	if got := gunzip(t, underlying.body.Bytes()); !bytes.Equal(got, body) {
		t.Error("final response after 103 did not round-trip")
	}
}

func TestPrivateMarkerDoesNotLeakOnAnInformationalResponse(t *testing.T) {
	body := bytes.Repeat([]byte("secret "), 2_000)
	underlying := newInformationalWriter()
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		NoCompress(w)
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(underlying, req)

	if got := underlying.headers[0].Get(noCompressHeader); got != "" {
		t.Errorf("private marker on 103 = %q, want absent", got)
	}
	if got := underlying.Header().Get(noCompressHeader); got != "" {
		t.Errorf("private marker on final response = %q, want absent", got)
	}
	if got := underlying.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity for secret response", got)
	}
	if !bytes.Equal(underlying.body.Bytes(), body) {
		t.Error("secret response changed after informational status")
	}
}

func TestSwitchingProtocolsIsAFinalStatus(t *testing.T) {
	body := bytes.Repeat([]byte("not an upgraded connection "), 100)
	underlying := newInformationalWriter()
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(underlying, req)

	if want := []int{http.StatusSwitchingProtocols}; !slices.Equal(underlying.statuses, want) {
		t.Errorf("statuses = %v, want %v", underlying.statuses, want)
	}
	if got := underlying.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity after 101", got)
	}
}

func TestInformationalStatusAfterFinalIsIgnored(t *testing.T) {
	body := bytes.Repeat([]byte("final first "), 2_000)
	underlying := newInformationalWriter()
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusEarlyHints)
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(underlying, req)

	if want := []int{http.StatusOK}; !slices.Equal(underlying.statuses, want) {
		t.Errorf("statuses = %v, want %v", underlying.statuses, want)
	}
}

func TestAPageCarryingASecretIsNotCompressed(t *testing.T) {
	body := []byte(strings.Repeat("secret ", 5_000))
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		NoCompress(w)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	})
	res := serveCompressed(t, Compress(h), "gzip")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity", got)
	}
	if got := res.Header().Get(noCompressHeader); got != "" {
		t.Errorf("private no-compress marker leaked as %q", got)
	}
	if !bytes.Equal(res.Body.Bytes(), body) {
		t.Error("secret-bearing response changed")
	}
}

func TestThePrivateMarkerDoesNotLeakWhenAHandlerPanics(t *testing.T) {
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		NoCompress(w)
		panic("test panic")
	}))
	res := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	}()
	if got := res.Header().Get(noCompressHeader); got != "" {
		t.Errorf("private no-compress marker survived panic as %q", got)
	}
}

func TestThePrivateMarkerDoesNotLeakFromAnEmptyResponse(t *testing.T) {
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		NoCompress(w)
	}))
	res := serveCompressed(t, h, "gzip")
	if got := res.Header().Get(noCompressHeader); got != "" {
		t.Errorf("private no-compress marker survived an empty response as %q", got)
	}
}

func TestCompressedRepresentationChangesETagAndDropsRanges(t *testing.T) {
	body := []byte(strings.Repeat("entity ", 5_000))
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"page-v1"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	})
	identity := serveCompressed(t, Compress(h), "")
	if got := identity.Header().Get("ETag"); got != `"page-v1"` {
		t.Errorf("identity ETag = %q, want unchanged", got)
	}
	if got := identity.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("identity Accept-Ranges = %q, want bytes", got)
	}
	if got := identity.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("identity Content-Length = %q, want %d", got, len(body))
	}
	if !bytes.Equal(identity.Body.Bytes(), body) {
		t.Error("identity representation body changed")
	}

	res := serveCompressed(t, Compress(h), "gzip")
	if got := res.Header().Get("ETag"); got != `"page-v1-gz"` {
		t.Errorf("ETag = %q, want representation-specific gzip tag", got)
	}
	if got := res.Header().Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges = %q, want absent on gzip representation", got)
	}
}

func TestAWeakETagRemainsWeakOnTheGzipRepresentation(t *testing.T) {
	body := []byte(strings.Repeat("weak validator ", 2_000))
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `W/"page-v1"`)
		_, _ = w.Write(body)
	})
	res := serveCompressed(t, Compress(h), "gzip")
	if got := res.Header().Get("ETag"); got != `W/"page-v1-gz"` {
		t.Errorf("ETag = %q, want weak representation-specific gzip tag", got)
	}
}

func TestCompressionPreservesAndDeduplicatesVary(t *testing.T) {
	body := []byte(strings.Repeat("vary ", 2_000))
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Cookie, Accept-Language")
		w.Header().Add("Vary", "accept-encoding")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	})
	res := serveCompressed(t, Compress(h), "gzip")
	for _, token := range []string{"Cookie", "Accept-Language", "Accept-Encoding"} {
		if got := varyTokenCount(res.Header(), token); got != 1 {
			t.Errorf("Vary %s count = %d, want 1; lines %q", token, got, res.Header().Values("Vary"))
		}
	}
}

func TestTheFlusherAndStatusRecorderReachThroughTheCompressor(t *testing.T) {
	recorder := &flushSnapshotRecorder{ResponseRecorder: httptest.NewRecorder()}
	status := &testStatusRecorder{ResponseWriter: recorder}
	var flushErr error
	first := bytes.Repeat([]byte("x"), 2_000)
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(first)
		flushErr = http.NewResponseController(w).Flush()
		_, _ = w.Write(bytes.Repeat([]byte("y"), 2_000))
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(status, req)

	if flushErr != nil {
		t.Errorf("ResponseController.Flush: %v", flushErr)
	}
	if status.status != http.StatusOK {
		t.Errorf("outer status recorder saw %d, want 200", status.status)
	}
	if !recorder.Flushed {
		t.Error("flush did not reach the underlying writer")
	}
	if len(recorder.bodyAtFlush) == 0 {
		t.Fatal("no gzip bytes had reached the underlying writer when Flush returned")
	}
	partial, err := gzip.NewReader(bytes.NewReader(recorder.bodyAtFlush))
	if err != nil {
		t.Fatalf("open gzip bytes available at Flush: %v", err)
	}
	gotAtFlush := make([]byte, len(first))
	if _, err := io.ReadFull(partial, gotAtFlush); err != nil {
		t.Fatalf("read first body from bytes available at Flush: %v", err)
	}
	if !bytes.Equal(gotAtFlush, first) {
		t.Error("bytes available at Flush did not contain the first body")
	}
	if got := gunzip(t, recorder.Body.Bytes()); len(got) != 4_000 {
		t.Errorf("gunzipped body is %d bytes, want 4000", len(got))
	}
}

func TestAnEarlyFlushCommitsABufferedResponseAsIdentity(t *testing.T) {
	recorder := &flushSnapshotRecorder{ResponseRecorder: httptest.NewRecorder()}
	first := bytes.Repeat([]byte("a"), 600)
	second := bytes.Repeat([]byte("b"), 2_000)
	var flushErr error
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(first)
		flushErr = http.NewResponseController(w).Flush()
		_, _ = w.Write(second)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(recorder, req)

	if flushErr != nil {
		t.Errorf("ResponseController.Flush: %v", flushErr)
	}
	if !bytes.Equal(recorder.bodyAtFlush, first) {
		t.Errorf("body at Flush = %d bytes, want the first %d identity bytes", len(recorder.bodyAtFlush), len(first))
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity after an early Flush", got)
	}
	if variesOnAcceptEncoding(recorder.Header()) {
		t.Errorf("Vary = %q; an early flush removed the gzip alternative", recorder.Header().Values("Vary"))
	}
	want := append(append([]byte{}, first...), second...)
	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Error("final identity body differs from both handler writes")
	}
}

func TestUnwrapLetsResponseControllerReachAWriteDeadline(t *testing.T) {
	underlying := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	var deadlineErr error
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deadlineErr = http.NewResponseController(w).SetWriteDeadline(time.Unix(1, 0))
	}))
	h.ServeHTTP(underlying,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	if deadlineErr != nil {
		t.Fatalf("SetWriteDeadline through compressor: %v", deadlineErr)
	}
	if !underlying.called {
		t.Error("SetWriteDeadline did not reach the underlying writer")
	}
}

func fixedResponse(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	})
}

func serveCompressed(t *testing.T, h http.Handler, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func gzipBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(body); err != nil {
		t.Fatalf("gzip fixture: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return out.Bytes()
}

func pseudoRandomBytes(n int) []byte {
	body := make([]byte, n)
	state := uint32(0x9e3779b9)
	for offset := 0; offset < len(body); {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		var block [4]byte
		binary.LittleEndian.PutUint32(block[:], state)
		offset += copy(body[offset:], block[:])
	}
	return body
}

func gunzip(t *testing.T, body []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close gzip response: %v", err)
	}
	return got
}

func variesOnAcceptEncoding(h http.Header) bool {
	return varyTokenCount(h, "Accept-Encoding") > 0
}

func varyTokenCount(h http.Header, want string) int {
	var count int
	for _, line := range h.Values("Vary") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				count++
			}
		}
	}
	return count
}

type testStatusRecorder struct {
	http.ResponseWriter

	status int
}

func (w *testStatusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *testStatusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *testStatusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type deadlineWriter struct {
	*httptest.ResponseRecorder

	called bool
}

type flushSnapshotRecorder struct {
	*httptest.ResponseRecorder

	bodyAtFlush []byte
}

func (w *flushSnapshotRecorder) Flush() {
	w.bodyAtFlush = bytes.Clone(w.Body.Bytes())
	w.ResponseRecorder.Flush()
}

type informationalWriter struct {
	header   http.Header
	statuses []int
	headers  []http.Header
	body     bytes.Buffer
}

func newInformationalWriter() *informationalWriter {
	return &informationalWriter{header: make(http.Header)}
}

func (w *informationalWriter) Header() http.Header { return w.header }

func (w *informationalWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	w.headers = append(w.headers, w.header.Clone())
}

func (w *informationalWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

type bodylessResponseRecorder struct {
	*httptest.ResponseRecorder

	status int
}

func (w *bodylessResponseRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseRecorder.WriteHeader(status)
}

func (w *bodylessResponseRecorder) Write(p []byte) (int, error) {
	if w.status == http.StatusNoContent || w.status == http.StatusNotModified {
		return 0, http.ErrBodyNotAllowed
	}
	return w.ResponseRecorder.Write(p)
}

func (w *deadlineWriter) SetWriteDeadline(time.Time) error {
	w.called = true
	return nil
}
