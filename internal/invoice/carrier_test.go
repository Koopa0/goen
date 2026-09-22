package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCarrierCheckRequiresAnExistenceVerdict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		exists  string
		want    CarrierStatus
		wantErr bool
	}{
		{"exists", 1, "Y", CarrierExists, false},
		{"missing", 1, "N", CarrierMissing, false},
		{"successful operation without verdict", 1, "", CarrierUnknown, true},
		{"unexpected verdict", 1, "yes", CarrierUnknown, true},
		{"maintenance is not missing", 9000001, "N", CarrierUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/B2CInvoice/CheckBarcode" {
					t.Errorf("carrier request = %s %s", r.Method, r.URL.Path)
				}
				g, err := NewGateway(testMerchantID, testHashKey, testHashIV, "")
				if err != nil {
					t.Error(err)
					return
				}
				var env envelope
				if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
					t.Error(err)
					return
				}
				plain, err := g.open(env.Data)
				if err != nil {
					t.Error(err)
					return
				}
				var request map[string]string
				if err := json.Unmarshal(plain, &request); err != nil {
					t.Error(err)
					return
				}
				if len(request) != 2 || request["MerchantID"] != testMerchantID || request["BarCode"] != "/ABC+123" {
					t.Errorf("unexpected carrier payload: %v", request)
				}
				reply(t, w, map[string]any{"RtnCode": tc.code, "IsExist": tc.exists})
			}))
			defer server.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			got, err := g.CheckBarcode(t.Context(), "/ABC+123")
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("CheckBarcode = %v, %v; want %v, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestCarrierCheckDoesNotCallForBadShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("bad shape reached provider") }))
	defer server.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := g.CheckBarcode(t.Context(), "bad"); status != CarrierUnknown || err == nil {
		t.Fatalf("malformed = %v, %v", status, err)
	}
	var disabled *Gateway
	if status, err := disabled.CheckBarcode(t.Context(), "/ABC+123"); status != CarrierUnknown || !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled = %v, %v", status, err)
	}
}

func TestCarrierCheckTransportFailureIsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := g.CheckBarcode(t.Context(), "/ABC+123"); status != CarrierUnknown || err == nil {
		t.Fatalf("unavailable = %v, %v", status, err)
	}
}

func TestCarrierCheckHasAShortDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	status, err := g.CheckBarcode(t.Context(), "/ABC+123")
	if status != CarrierUnknown || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v, %v", status, err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("carrier lookup exceeded checkout's short budget")
	}
}
