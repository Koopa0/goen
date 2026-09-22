//go:build integration

package cart_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice"
)

// The store-role checkout, frozen preference and actual encrypted filing share
// one fixture, so clearing a company barcode in either layer loses evidence.
func TestCompanyDeliverySurvivesCheckoutSnapshotAndProviderWire(t *testing.T) {
	for _, delivery := range []invoice.CompanyDelivery{invoice.CompanyDeliveryEmail, invoice.CompanyDeliveryMobile} {
		t.Run(string(delivery), func(t *testing.T) {
			ctx := t.Context()
			s := cart.NewStore(storeRolePool(t))
			id := newCart(t, s)
			if err := s.Add(ctx, id, freshVariant(t, "company-delivery-"+uuid.NewString()), 1); err != nil {
				t.Fatal(err)
			}
			shippingID := shipVersionFor(t, "home_delivery")
			addr := &cart.Address{Email: "company@example.invalid", Name: "Buyer", Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"}
			inv := &cart.Invoice{Type: invoice.PreferenceCompany, CompanyName: "Buyer Company", TaxID: "04595252", CompanyDelivery: delivery, Carrier: "/ABC+123"}
			if errs := inv.Validate(); len(errs) != 0 {
				t.Fatalf("validate: %+v", errs)
			}
			shown := checkoutQuote(t, s, id, uuid.NullUUID{}, shippingID, addr, "")
			key := checkoutAttemptKey("company-delivery-" + uuid.NewString())
			number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shippingID, addr, inv, "", shown, key)
			if err != nil {
				t.Fatal(err)
			}
			wantCarrier, wantType := "", invoice.CarrierMember
			if delivery == invoice.CompanyDeliveryMobile {
				wantCarrier, wantType = "/ABC+123", invoice.CarrierMobile
			}
			inv.Carrier, inv.CompanyName, addr.Email = "/ZZZ+999", "Changed Company", "changed@example.invalid"
			if retry, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shippingID, addr, inv, "", shown, key); err != nil || retry != number {
				t.Fatalf("placement retry changed identity: %v", err)
			}
			view, err := s.Order(ctx, number)
			if err != nil {
				t.Fatal(err)
			}
			if view.Invoice.Carrier != wantCarrier || view.Invoice.CompanyName != "Buyer Company" || view.Invoice.TaxID != "04595252" || view.InvoiceEmail != "company@example.invalid" {
				t.Fatal("order display lost the frozen invoice preference")
			}
			if _, err := pool.Exec(ctx, `INSERT INTO payments (order_id,provider,provider_ref,intended_amount_cents,captured_amount_cents,status,paid_at) SELECT id,'stripe','cs_company_'||id,order_amount_owed(id),order_amount_owed(id),'succeeded',now() FROM orders WHERE order_number=$1`, number); err != nil {
				t.Fatal(err)
			}
			var actor uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO users(email,role) VALUES ('company-invoice-'||gen_random_uuid()||'@goen.invalid','admin') RETURNING id`).Scan(&actor); err != nil {
				t.Fatal(err)
			}
			var filed map[string]any
			calls := 0
			documentNumber := "CE12345678"
			if delivery == invoice.CompanyDeliveryMobile {
				documentNumber = "CM12345678"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetIssue":
					if filed == nil {
						companyInvoiceReply(t, w, map[string]any{"RtnCode": 2, "RtnMsg": "not found"})
					} else {
						companyInvoiceReply(t, w, map[string]any{
							"RtnCode": 1, "IIS_Number": documentNumber,
							"IIS_Relate_Number": filed["RelateNumber"], "IIS_Sales_Amount": filed["SalesAmount"],
							"IIS_Create_Date": "2026-09-01 09:35:00", "IIS_Issue_Status": 1,
							"IIS_Invalid_Status": 0, "IIS_Check_Number": "P", "IIS_Random_Number": "4816", "Items": filed["Items"],
						})
					}
				case "/B2CInvoice/Issue":
					filed = companyInvoicePayload(t, r)
					calls++
					companyInvoiceReply(t, w, map[string]any{"RtnCode": 1, "InvoiceNo": documentNumber, "InvoiceDate": "2026-09-01 09:35:00", "RandomNumber": "4816"})
				default:
					t.Errorf("unexpected provider path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			gateway, err := invoice.NewGateway("2000132", "ejCk326UnaZWKisg", "q9jcZX8Ib9LM8wYk", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			issuer := invoice.NewStore(pool, gateway)
			filing := invoice.WithFilingIdentity(ctx, actor, "company-invoice:"+uuid.NewString())
			if _, err := issuer.Issue(filing, number); err != nil {
				t.Fatal(err)
			}
			if _, err := issuer.Issue(filing, number); !errors.Is(err, invoice.ErrAlreadyIssued) {
				t.Fatal(err)
			}
			for field, want := range map[string]string{"CustomerName": "Buyer Company", "CustomerIdentifier": "04595252", "CustomerEmail": "company@example.invalid", "CarrierType": wantType, "Print": "0", "Donation": "0"} {
				if filed[field] != want {
					t.Errorf("%s did not retain the chosen buyer/delivery", field)
				}
			}
			if got, _ := filed["CarrierNum"].(string); got != wantCarrier {
				t.Error("wire carrier differs from the checkout snapshot")
			}
			if calls != 1 {
				t.Errorf("retry filed %d invoices", calls)
			}
		})
	}
}

func companyInvoicePayload(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var envelope struct{ Data string }
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher([]byte("ejCk326UnaZWKisg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		t.Fatal("invalid encrypted fixture")
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, []byte("q9jcZX8Ib9LM8wYk")).CryptBlocks(plain, raw)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > block.BlockSize() {
		t.Fatal("invalid fixture padding")
	}
	decoded, err := url.QueryUnescape(string(plain[:len(plain)-padding]))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(decoded), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func companyInvoiceReply(t *testing.T, w http.ResponseWriter, body map[string]any) {
	t.Helper()
	plain, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(strings.ReplaceAll(url.QueryEscape(string(plain)), "+", "%20"))
	block, err := aes.NewCipher([]byte("ejCk326UnaZWKisg"))
	if err != nil {
		t.Fatal(err)
	}
	padding := block.BlockSize() - len(encoded)%block.BlockSize()
	encoded = append(encoded, bytes.Repeat([]byte{byte(padding)}, padding)...)
	ciphertext := make([]byte, len(encoded))
	cipher.NewCBCEncrypter(block, []byte("q9jcZX8Ib9LM8wYk")).CryptBlocks(ciphertext, encoded)
	if err := json.NewEncoder(w).Encode(map[string]any{"TransCode": 1, "Data": base64.StdEncoding.EncodeToString(ciphertext)}); err != nil {
		t.Fatal(err)
	}
}
