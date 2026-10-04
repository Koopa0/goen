//go:build integration

package invoice

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExemptInvoiceAndAllowanceKeepTheirFrozenLineTerms(t *testing.T) {
	for _, lostAllowance := range []bool{false, true} {
		name := "buyer agreed"
		if lostAllowance {
			name = "invalid after lost response"
		}
		t.Run(name, func(t *testing.T) {
			ctx := filingTestContext(t, t.Context())
			invoiceNumber, allowanceNumber := "ZX62500001", "2026100515227101"
			if lostAllowance {
				invoiceNumber, allowanceNumber = "ZX62500002", "2026100515227102"
			}
			number := ownedOrderToInvoiceWithLineTerms(t, uuid.NullUUID{}, 10000, 0, 0, PreferenceMember, "王小明", "", LineTerms{TaxType: Exempt, Unit: "六字中文單位"})
			var providerMu sync.Mutex
			var sentIssue *issueRequest
			var sentAllowance *allowanceRequest
			var firstAllowance *allowanceRequest
			invalidKnown := false
			allowanceSends := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerMu.Lock()
				defer providerMu.Unlock()
				switch r.URL.Path {
				case "/B2CInvoice/GetIssue":
					if sentIssue == nil {
						replyNoIssue(t, w)
					} else {
						replyIssueLookup(t, w, sentIssue, invoiceNumber, "1234", false)
					}
				case "/B2CInvoice/Issue":
					var request issueRequest
					if err := json.Unmarshal(openProviderPayload(t, r), &request); err != nil {
						t.Error(err)
						return
					}
					sentIssue = &request
					if request.TaxType != "3" || request.SpecialTaxType != 8 || len(request.Items) != 1 {
						t.Errorf("exempt issue header=%q/%d, lines=%d", request.TaxType, request.SpecialTaxType, len(request.Items))
					}
					for i, item := range request.Items {
						unit := "個"
						if i == 0 {
							unit = "六字中文單位"
						}
						if item.ItemTaxType != "3" || item.ItemWord != unit {
							t.Errorf("issued line %d tax/unit=%q/%q", i, item.ItemTaxType, item.ItemWord)
						}
					}
					reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber, InvoiceDate: "2026-08-21 10:00:00", RandomNumber: "1234"})
				case "/B2CInvoice/GetAllowanceList":
					if sentAllowance == nil {
						replyNoAllowances(t, w)
						return
					}
					row := agreedAllowance(*firstAllowance, allowanceNumber, "2026-08-21 11:00:00")
					if lostAllowance || invalidKnown {
						row["IA_Invalid_Status"] = 1
					}
					rows := []map[string]any{row}
					if allowanceSends > 1 {
						rows = append(rows, agreedAllowance(*sentAllowance, "2026100515227103", "2026-08-21 12:00:00"))
					}
					reply(t, w, map[string]any{"RtnCode": 1, "AllowanceInfo": rows})
				case "/B2CInvoice/AllowanceByCollegiate":
					request := openAllowanceRequest(t, r)
					sentAllowance = &request
					allowanceSends++
					if firstAllowance == nil {
						firstAllowance = &request
					}
					if len(request.Items) != 1 || request.Items[0].ItemTaxType != "3" || request.Items[0].ItemWord != "個" {
						t.Errorf("exempt allowance items=%+v", request.Items)
					}
					if lostAllowance {
						_, _ = w.Write([]byte("lost response"))
						return
					}
					replyAllowanceRequested(t, w, request.InvoiceNo, allowanceNumber)
				default:
					t.Errorf("unexpected provider path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			gateway, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if gatewayErr != nil {
				t.Fatal(gatewayErr)
			}
			adminPool := invoiceAdminPool(t)
			store := NewStore(adminPool, gateway)
			document, err := store.Issue(ctx, number)
			if err != nil {
				t.Fatalf("issue exempt invoice: %v", err)
			}
			if len(document.Lines) != 1 {
				t.Fatalf("stored invoice lines=%d, want one exempt item", len(document.Lines))
			}
			for i, line := range document.Lines {
				unit := ItemUnit("個")
				if i == 0 {
					unit = "六字中文單位"
				}
				if line.TaxType != Exempt || line.Unit != unit {
					t.Errorf("stored invoice line %d terms=%q/%q", i, line.TaxType, line.Unit)
				}
			}
			addCardRefund(t, number, 5000)
			operation := uuid.New()
			_, err = store.FileAllowance(ctx, number, operation)
			if lostAllowance {
				if !errors.Is(err, ErrPending) {
					t.Fatalf("lost allowance response=%v", err)
				}
			} else if !errors.Is(err, ErrAwaitingBuyer) {
				t.Fatalf("request allowance=%v", err)
			}
			allowance, err := passAgain(t, store, ctx, operation)
			if lostAllowance {
				if !errors.Is(err, ErrRejected) {
					t.Fatalf("invalid unknown allowance=%v", err)
				}
			} else if err != nil {
				t.Fatalf("settle exempt allowance=%v", err)
			}
			documents, err := store.Documents(ctx, number)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range documents {
				if d.Kind != DocumentAllowance {
					continue
				}
				found = true
				if len(d.Lines) != 1 || d.Lines[0].TaxType != Exempt || d.Lines[0].Unit != DefaultUnit {
					t.Fatalf("stored allowance terms=%+v", d.Lines)
				}
				if lostAllowance && d.Status != DocumentVoided {
					t.Errorf("invalid allowance status=%q", d.Status)
				}
			}
			if !found {
				t.Fatal("settlement did not record the allowance")
			}
			if !lostAllowance && allowance.Lines[0].TaxType != Exempt {
				t.Error("settlement returned a taxable allowance")
			}
			if lostAllowance {
				return
			}
			providerMu.Lock()
			invalidKnown = true
			providerMu.Unlock()
			addCardRefund(t, number, 5000)
			replacement := uuid.New()
			_, err = store.FileAllowance(ctx, number, replacement)
			if !errors.Is(err, ErrPending) {
				t.Fatalf("reconcile invalid exempt allowance=%v", err)
			}
			_, err = passAgain(t, store, ctx, replacement)
			if !errors.Is(err, ErrAwaitingBuyer) {
				t.Fatalf("send refrozen exempt allowance=%v", err)
			}
			replaced, err := passAgain(t, store, ctx, replacement)
			if err != nil {
				t.Fatalf("settle replacement exempt allowance=%v", err)
			}
			if replaced.AmountCents != 10000 || len(replaced.Lines) != 1 || replaced.Lines[0].TaxType != Exempt || replaced.Lines[0].Unit != DefaultUnit {
				t.Fatalf("refrozen replacement=%+v", replaced)
			}
			documents, err = store.Documents(ctx, number)
			if err != nil {
				t.Fatal(err)
			}
			var voided, active int
			for _, d := range documents {
				if d.Kind != DocumentAllowance {
					continue
				}
				if d.Status == DocumentVoided {
					voided++
				} else {
					active++
				}
				if d.Lines[0].TaxType != Exempt || d.Lines[0].Unit != DefaultUnit {
					t.Fatalf("invalidation lost local terms: %+v", d.Lines)
				}
			}
			if voided != 1 || active != 1 {
				t.Fatalf("allowance history voided/active=%d/%d", voided, active)
			}
		})
	}
}

func invoiceAdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config := pool.Config().Copy()
	config.ConnConfig.RuntimeParams["role"] = "admin"
	adminPool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	var role string
	if err := adminPool.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("invoice filing role=%q: %v", role, err)
	}
	return adminPool
}

func TestCanonicalInvoiceDeliveryAndRoundingFollowTheExemptOrder(t *testing.T) {
	number := ownedOrderToInvoiceWithLineTerms(t, uuid.NullUUID{}, 10050, 8050, 0, PreferenceMember, "王小明", "", LineTerms{TaxType: Exempt, Unit: "包"})
	rows, err := pool.Query(t.Context(), `SELECT description,tax_type,unit FROM canonical_invoice_lines((SELECT id FROM orders WHERE order_number=$1)) ORDER BY line_position`, number)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var description, taxType, unit string
		if err := rows.Scan(&description, &taxType, &unit); err != nil {
			t.Fatal(err)
		}
		expectedUnit := "個"
		if count == 0 {
			expectedUnit = "包"
		}
		if taxType != "exempt" || unit != expectedUnit {
			t.Errorf("canonical %q tax/unit=%q/%q, want exempt/%q", description, taxType, unit, expectedUnit)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("canonical lines=%d, want item, delivery and rounding", count)
	}
}

func TestInvalidationRefreezesTheLocalExemptAllowance(t *testing.T) {
	ctx := t.Context()
	number := ownedOrderToInvoiceWithLineTerms(t, uuid.NullUUID{}, 10000, 0, 0, PreferenceMember, "王小明", "", LineTerms{TaxType: Exempt, Unit: "包"})
	addCardRefund(t, number, 7000)
	var original, known uuid.UUID
	if err := pool.QueryRow(ctx, `
        WITH document AS (
            INSERT INTO invoice_documents (order_id,kind,number,amount_cents,issued_at)
            SELECT id,'invoice','ZX62500003',10000,'2026-08-21 10:00:00+08' FROM orders WHERE order_number=$1 RETURNING id
        ) INSERT INTO invoice_document_lines (document_id,description,quantity,unit_price_cents,amount_cents,tax_type,unit,position)
        SELECT id,'免稅測試商品',1,10000,10000,'exempt','包',0 FROM document RETURNING document_id`, number).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
        WITH document AS (
            INSERT INTO invoice_documents (order_id,kind,original_id,number,amount_cents,issued_at)
            SELECT order_id,'allowance',id,'2026100515227104',5000,'2026-08-21 11:00:00+08' FROM invoice_documents WHERE id=$1 RETURNING id
        ) INSERT INTO invoice_document_lines (document_id,description,quantity,unit_price_cents,amount_cents,tax_type,unit,position)
        SELECT id,'退貨折讓',1,5000,5000,'exempt','個',0 FROM document RETURNING document_id`, original).Scan(&known); err != nil {
		t.Fatal(err)
	}
	adminPool := invoiceAdminPool(t)
	operation := uuid.New()
	if err := adminPool.QueryRow(ctx, `SELECT claim_invoice_allowance($1,$2,$3,'exempt-invalidation')`, original, operation, filingActor).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	var leased uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT lease_invoice_operation($1,$2,interval '1 minute')`, operation, owner).Scan(&leased); err != nil || leased != operation {
		t.Fatalf("lease=%s: %v", leased, err)
	}
	var amount int64
	if err := adminPool.QueryRow(ctx, `SELECT reconcile_invalid_invoice_allowance($1,$2,$3,'ZX62500003','2026100515227104','2026-08-21 11:00:00+08',5000,ARRAY['退貨折讓'],ARRAY[1],ARRAY[5000]::bigint[],ARRAY[5000]::bigint[])`, operation, owner, known).Scan(&amount); err != nil {
		t.Fatalf("reconcile known exempt allowance: %v", err)
	}
	var taxType, unit, status string
	if err := adminPool.QueryRow(ctx, `SELECT op.request_payload->'lines'->0->>'tax_type',op.request_payload->'lines'->0->>'unit',d.status FROM invoice_operations op JOIN invoice_documents d ON d.id=$2 WHERE op.id=$1`, operation, known).Scan(&taxType, &unit, &status); err != nil {
		t.Fatal(err)
	}
	if amount != 7000 || taxType != "exempt" || unit != "個" || status != "voided" {
		t.Fatalf("refrozen allowance amount/tax/unit, old status=%d/%q/%q/%q", amount, taxType, unit, status)
	}
}
