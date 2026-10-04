//go:build integration

package invoice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/shoptime"
)

// ProcessOperation runs one operation the way a request handler does, so a
// fixture need not depend on which due operation the reconciler takes first.
func (s *Store) ProcessOperation(ctx context.Context, operationID uuid.UUID) error {
	_, err := s.processClaim(ctx, operationID)
	return err
}

// FakeECPay stands in for ECPay's B2C invoice API in other packages'
// integration fixtures, through the gateway's own cipher rather than a second
// copy of it. What it issues is dated IssuedAt; Invalid voids it.
type FakeECPay struct {
	IssuedAt time.Time

	server  *httptest.Server
	gateway *Gateway

	mu       sync.Mutex
	byRelate map[string]*fakeInvoice
	calls    map[string]int
}

type fakeInvoice struct {
	number  string
	amount  int64
	items   json.RawMessage
	invalid bool
}

// fakeInvoiceNumbers keeps the fake's invoice numbers unique in one test
// binary's database.
var fakeInvoiceNumbers atomic.Int64

func NewFakeECPay(issuedAt time.Time) (*FakeECPay, error) {
	f := &FakeECPay{IssuedAt: issuedAt, byRelate: map[string]*fakeInvoice{}, calls: map[string]int{}}
	f.server = httptest.NewServer(f)
	g, err := NewGateway("2000132", "ejCk326UnaZWKisg", "q9jcZX8Ib9LM8wYk", f.server.URL)
	if err != nil {
		f.server.Close()
		return nil, err
	}
	f.gateway = g
	return f, nil
}

func (f *FakeECPay) Gateway() *Gateway { return f.gateway }

func (f *FakeECPay) Close() { f.server.Close() }

// Calls is how many requests reached path, such as "/B2CInvoice/Invalid".
func (f *FakeECPay) Calls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *FakeECPay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var outer envelope
	if err := json.NewDecoder(r.Body).Decode(&outer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	plain, err := f.gateway.open(outer.Data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		RelateNumber string
		InvoiceNo    string
		SalesAmount  int64
		Items        json.RawMessage
	}
	if decodeErr := json.Unmarshal(plain, &req); decodeErr != nil {
		http.Error(w, decodeErr.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path]++
	reply, ok := f.answer(r.URL.Path, req.RelateNumber, req.InvoiceNo, req.SalesAmount, req.Items)
	if !ok {
		http.NotFound(w, r)
		return
	}
	payload, err := json.Marshal(reply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sealed, err := f.gateway.seal(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if writeErr := json.NewEncoder(w).Encode(response{TransCode: 1, Data: sealed}); writeErr != nil {
		http.Error(w, writeErr.Error(), http.StatusInternalServerError)
	}
}

func (f *FakeECPay) answer(
	path, relateNumber, invoiceNumber string, salesAmount int64, items json.RawMessage,
) (map[string]any, bool) {
	switch path {
	case "/B2CInvoice/Issue":
		inv := &fakeInvoice{
			number: fmt.Sprintf("RF%08d", fakeInvoiceNumbers.Add(1)), amount: salesAmount, items: items,
		}
		f.byRelate[relateNumber] = inv
		return map[string]any{"RtnCode": 1, "RtnMsg": "ok", "InvoiceNo": inv.number,
			"InvoiceDate": shoptime.Second(f.IssuedAt), "RandomNumber": "2468"}, true
	case "/B2CInvoice/GetIssue":
		inv, found := f.byRelate[relateNumber]
		if !found {
			return map[string]any{"RtnCode": 2, "RtnMsg": "not found"}, true
		}
		invalid := 0
		if inv.invalid {
			invalid = 1
		}
		return map[string]any{"RtnCode": 1, "RtnMsg": "ok", "IIS_Number": inv.number,
			"IIS_Relate_Number": relateNumber, "IIS_Sales_Amount": inv.amount,
			"IIS_Create_Date":  shoptime.Second(f.IssuedAt),
			"IIS_Issue_Status": 1 - invalid, "IIS_Invalid_Status": invalid,
			"IIS_Random_Number": "2468", "Items": inv.items}, true
	case "/B2CInvoice/Invalid":
		for _, inv := range f.byRelate {
			if inv.number == invoiceNumber {
				inv.invalid = true
			}
		}
		return map[string]any{"RtnCode": 1, "RtnMsg": "ok", "InvoiceNo": invoiceNumber}, true
	}
	return nil, false
}
