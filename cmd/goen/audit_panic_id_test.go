package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditPanicRequestID(t *testing.T) {
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("audit panic")
	})

	tests := []struct {
		name     string
		supplied string
	}{
		{name: "generated request ID"},
		{name: "accepted incoming request ID", supplied: "abc-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&logs, nil))
			h := withRequestTracing(panicHandler, log)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/audit", http.NoBody)
			if tt.supplied != "" {
				req.Header.Set("X-Request-Id", tt.supplied)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}

			var event map[string]any
			if err := json.Unmarshal(bytes.SplitN(bytes.TrimSpace(logs.Bytes()), []byte("\n"), 2)[0], &event); err != nil {
				t.Fatal(err)
			}
			id := response.Header().Get("X-Request-Id")
			t.Logf("response request ID=%s; panic log=%s", id, logs.String())
			if id == "" {
				t.Fatal("no X-Request-Id on the response")
			}
			if tt.supplied != "" && id != tt.supplied {
				t.Fatalf("X-Request-Id = %q, want honoured %q", id, tt.supplied)
			}
			if event["request_id"] != id {
				t.Errorf("panic log request_id=%q, want response X-Request-Id=%q", event["request_id"], id)
			}
		})
	}
}

// TestRecoveredPanicCarriesItsStackAndLeavesARequestLine: a panic's message
// alone names no file or line, and a request that panicked must still appear in
// the request log, at the 500 recovery sent.
func TestRecoveredPanicCarriesItsStackAndLeavesARequestLine(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := withRequestTracing(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("audit panic")
	}), log)

	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/audit", http.NoBody))

	var panicLine, requestLine map[string]any
	for line := range bytes.SplitSeq(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		switch event["msg"] {
		case "panic serving request":
			panicLine = event
		case "request":
			requestLine = event
		}
	}
	if stack, _ := panicLine["stack"].(string); !strings.Contains(stack, "audit_panic_id_test.go") {
		t.Errorf("panic log stack = %q, want it to name the panicking file", stack)
	}
	if requestLine == nil || requestLine["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("request log = %v, want a line at status 500", requestLine)
	}
}

// TestRecoveredPanicAfterTheHeaderLeavesTheSentStatus: http.Error after a
// WriteHeader would append an error body to a response that already said 200.
func TestRecoveredPanicAfterTheHeaderLeavesTheSentStatus(t *testing.T) {
	h := withRequestTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("late panic")
	}), slog.New(slog.DiscardHandler))

	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/audit", http.NoBody))

	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Errorf("response = %d %q, want the sent 200 with no appended body", response.Code, response.Body.String())
	}
}

// TestAbortHandlerIsNotTurnedIntoA500 keeps http.ErrAbortHandler what net/http
// defines it to be: a request to abandon the response without noise.
func TestAbortHandlerIsNotTurnedIntoA500(t *testing.T) {
	var logs bytes.Buffer
	h := withRequestTracing(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), slog.New(slog.NewJSONHandler(&logs, nil)))

	response := httptest.NewRecorder()
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("http.ErrAbortHandler was not re-panicked")
		}
		if response.Body.Len() != 0 || logs.Len() != 0 {
			t.Errorf("abort wrote body %q and log %q", response.Body.String(), logs.String())
		}
	}()
	h.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/audit", http.NoBody))
}

func TestMalformedRequestIDIsRejectedBeforePanicRecovery(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := withRequestTracing(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("audit panic")
	}), log)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/audit", http.NoBody)
	req.Header.Set("X-Request-Id", "forged\nlog line")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)

	id := response.Header().Get("X-Request-Id")
	if id == "" || id == "forged\nlog line" {
		t.Fatalf("X-Request-Id = %q, want a generated replacement", id)
	}
	if logs.Len() == 0 {
		t.Fatal("expected a panic log")
	}
}
