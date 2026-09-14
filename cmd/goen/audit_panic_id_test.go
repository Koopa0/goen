package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
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
