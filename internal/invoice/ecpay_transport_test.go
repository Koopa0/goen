package invoice

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const replyProbePath = "/B2CInvoice/GetIssue"

// sealedReply is a successful ECPay reply sealed under key and iv, which need
// not be the merchant's.
func sealedReply(t *testing.T, key, iv string) string {
	t.Helper()
	sealer := &Gateway{merchantID: testMerchantID, hashKey: []byte(key), hashIV: []byte(iv)}
	sealed, err := sealer.seal([]byte(`{"RtnCode":1,"RtnMsg":"OK"}`))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	outer, err := json.Marshal(response{TransCode: 1, Data: sealed})
	if err != nil {
		t.Fatalf("encode reply: %v", err)
	}
	return string(outer)
}

func gatewayAnswering(t *testing.T, reply string) *Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return g
}

// TestOnlyAReplySealedUnderTheMerchantKeyIsRead holds the envelope as the only
// way an ECPay verdict enters goen: the outer TransCode says ECPay could read
// the request, and the operation's result counts only once it opens under the
// merchant's own key.
func TestOnlyAReplySealedUnderTheMerchantKeyIsRead(t *testing.T) {
	tests := []struct {
		name  string
		reply func(t *testing.T) string
		ok    bool
	}{
		{
			name:  "sealed under the merchant key",
			reply: func(t *testing.T) string { t.Helper(); return sealedReply(t, testHashKey, testHashIV) },
			ok:    true,
		},
		{
			name:  "sealed under another key",
			reply: func(t *testing.T) string { t.Helper(); return sealedReply(t, "0123456789abcdef", "fedcba9876543210") },
		},
		{
			name: "a result in the clear",
			reply: func(t *testing.T) string {
				t.Helper()
				outer, err := json.Marshal(response{TransCode: 1, Data: `{"RtnCode":1,"RtnMsg":"OK"}`})
				if err != nil {
					t.Fatalf("encode reply: %v", err)
				}
				return string(outer)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gatewayAnswering(t, tt.reply(t))
			res, err := g.call[resultStatus](t.Context(), replyProbePath, map[string]string{})
			if tt.ok {
				if err != nil || res.RtnCode != 1 {
					t.Fatalf("call() = %+v, %v; want the sealed success", res, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("call() trusted %+v from a reply the merchant key did not seal", res)
			}
		})
	}
}

// TestAnECPayReplyPastTheBoundIsRefused holds the read bound. The oversized
// reply is a genuine sealed success behind leading whitespace, so only the
// bound can refuse it.
func TestAnECPayReplyPastTheBoundIsRefused(t *testing.T) {
	g := gatewayAnswering(t, strings.Repeat(" ", 1<<20)+sealedReply(t, testHashKey, testHashIV))
	if res, err := g.call[resultStatus](t.Context(), replyProbePath, map[string]string{}); err == nil {
		t.Fatalf("call() read a reply past the bound whole and returned %+v", res)
	}
}

// TestECPayIsReachedOnlyOverVerifiedTLSWithADeadline holds the client under
// every ECPay call: a finite timeout, and certificate verification, so a
// server no system root vouches for is never sent a sealed request.
func TestECPayIsReachedOnlyOverVerifiedTLSWithADeadline(t *testing.T) {
	var served atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		_, _ = io.WriteString(w, sealedReply(t, testHashKey, testHashIV))
	}))
	t.Cleanup(srv.Close)

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if g.http.Timeout <= 0 {
		t.Errorf("the ECPay client has timeout %v, want a finite one", g.http.Timeout)
	}
	if _, err := g.call[resultStatus](t.Context(), replyProbePath, map[string]string{}); err == nil {
		t.Fatal("call() succeeded against a certificate nothing vouches for")
	}
	if n := served.Load(); n != 0 {
		t.Errorf("the untrusted server received %d requests", n)
	}
}
