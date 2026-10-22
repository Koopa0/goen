package issuer

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
)

// TestDecodeOperationDurableFacts: decodeOperation keeps every durable fact of
// an operation row, for each kind and status.
func TestDecodeOperationDurableFacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []struct {
		name string
		want OperationKind
	}{{"issue", OperationIssue}, {"allowance", OperationAllowance}, {"void", OperationVoid}} {
		for _, status := range []struct {
			name string
			want OperationStatus
		}{{"pending", OperationPending}, {"attention", OperationAttention}, {"succeeded", OperationSucceeded}, {"rejected", OperationRejected}} {
			t.Run(kind.name+"/"+status.name, func(t *testing.T) {
				t.Parallel()
				row := db.InvoiceOperationRow{ID: uuid.New(), OrderID: uuid.New(), Kind: kind.name, Status: status.name, ProviderKey: "provider-key", AmountCents: 12300, RequestPayload: []byte(`{"relate_number":"GO-OPERATION"}`), ReconcileAttempts: 2, SendAttempts: 3, ResendAuthorizations: 1, LastError: "provider_unknown"}
				op, err := decodeOperation(&row)
				if err != nil {
					t.Fatal(err)
				}
				if op.Kind != kind.want || op.Status != status.want || op.ID != row.ID || op.OrderID != row.OrderID || op.ProviderKey != row.ProviderKey || op.AmountCents != row.AmountCents || op.Request.RelateNumber != "GO-OPERATION" || op.Reconciles != 2 || op.Sends != 3 || op.ResendAuthorizations != 1 || op.LastError != row.LastError {
					t.Errorf("decode lost durable facts: %+v", op)
				}
			})
		}
	}
}

// TestDecodeOperationUnknownVocabulary: decodeOperation refuses a kind or
// status the database does not use, naming the operation, and returns nothing
// the state machine could act on.
func TestDecodeOperationUnknownVocabulary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, status, want string
	}{
		{"document kind", "invoice", "pending", "unknown kind"},
		{"blank kind", "", "pending", "unknown kind"},
		{"document status", "issue", "issued", "unknown status"},
		{"blank status", "issue", "", "unknown status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row := db.InvoiceOperationRow{ID: uuid.New(), Kind: tc.kind, Status: tc.status, RequestPayload: []byte(`{}`)}
			op, err := decodeOperation(&row)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), row.ID.String()) {
				t.Errorf("decode error = %v, want %q naming operation %s", err, tc.want, row.ID)
			}
			if op.ID != uuid.Nil {
				t.Error("an unknown operation reached the state machine")
			}
		})
	}
}
