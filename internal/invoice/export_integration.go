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
// copy of it. What it issues is dated IssuedAt; Invalid voids it. An allowance
// it is asked for stays off GetAllowanceList until BuyerAgrees.
type FakeECPay struct {
	IssuedAt time.Time

	server  *httptest.Server
	gateway *Gateway

	mu          sync.Mutex
	byRelate    map[string]*fakeInvoice
	allowances  []*fakeAllowance
	calls       map[string]int
	holdInvalid bool
}

type fakeAllowance struct {
	number, invoiceNumber, notifyMail, agreedAt string
	amount                                      int64
	items                                       json.RawMessage
}

// FakeBuyerIP is the address FakeECPay reports a buyer agreeing from.
const FakeBuyerIP = "203.0.113.7"

type fakeInvoice struct {
	number  string
	amount  int64
	items   json.RawMessage
	invalid bool
}

// fakeInvoiceNumbers and fakeAllowanceNumbers keep the fake's document numbers
// unique in one test binary's database.
var fakeInvoiceNumbers, fakeAllowanceNumbers atomic.Int64

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

// HoldInvalid makes every later Invalid wait until its caller gives up, as a
// 加值中心 that has stopped answering does.
func (f *FakeECPay) HoldInvalid() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdInvalid = true
}

// BuyerAgrees has the buyer follow the link of every allowance asked for so
// far, which puts it on GetAllowanceList.
func (f *FakeECPay) BuyerAgrees() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.allowances {
		if a.agreedAt == "" {
			a.agreedAt = shoptime.Second(time.Now())
		}
	}
}

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
	var req fakeRequest
	if decodeErr := json.Unmarshal(plain, &req); decodeErr != nil {
		http.Error(w, decodeErr.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls[r.URL.Path]++
	hold := f.holdInvalid && r.URL.Path == "/B2CInvoice/Invalid"
	f.mu.Unlock()
	if hold {
		<-r.Context().Done()
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	reply, ok := f.answer(r.URL.Path, &req)
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

// fakeRequest is every field the fake reads, from whichever request it is.
type fakeRequest struct {
	RelateNumber    string
	InvoiceNo       string
	SalesAmount     int64
	AllowanceAmount int64
	NotifyMail      string
	Items           json.RawMessage
}

func (f *FakeECPay) answer(path string, req *fakeRequest) (map[string]any, bool) {
	relateNumber, invoiceNumber, salesAmount, items := req.RelateNumber, req.InvoiceNo, req.SalesAmount, req.Items
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
	case "/B2CInvoice/AllowanceByCollegiate":
		a := &fakeAllowance{
			number:        fmt.Sprintf("26%014d", fakeAllowanceNumbers.Add(1)),
			invoiceNumber: invoiceNumber, notifyMail: req.NotifyMail,
			amount: req.AllowanceAmount, items: items,
		}
		f.allowances = append(f.allowances, a)
		now := time.Now()
		return map[string]any{"RtnCode": 1, "RtnMsg": "ok", "IA_Allow_No": a.number,
			"IA_Invoice_No": invoiceNumber, "IA_TempDate": shoptime.Second(now),
			"IA_TempExpireDate": shoptime.Second(now.Add(BuyerConsentWindow))}, true
	case "/B2CInvoice/GetAllowanceList":
		var listed []map[string]any
		for _, a := range f.allowances {
			if a.invoiceNumber != invoiceNumber || a.agreedAt == "" {
				continue
			}
			listed = append(listed, map[string]any{"IA_Allow_No": a.number, "IA_Date": a.agreedAt,
				"IA_Invoice_No": a.invoiceNumber, "IA_Invalid_Status": 0,
				"IA_Total_Tax_Amount": a.amount, "IA_IP": FakeBuyerIP,
				"IA_Send_Mail": a.notifyMail, "Items": a.items})
		}
		if len(listed) == 0 {
			return map[string]any{"RtnCode": 7, "RtnMsg": "no data"}, true
		}
		return map[string]any{"RtnCode": 1, "RtnMsg": "ok", "AllowanceInfo": listed}, true
	}
	return nil, false
}
