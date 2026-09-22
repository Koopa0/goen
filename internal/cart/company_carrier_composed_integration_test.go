//go:build integration

package cart_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/invoice"
)

func companyCarrierCheckout(t *testing.T, checker *checkoutCarrier) carrierCheckout {
	t.Helper()
	checkout := carrierCheckoutFor(t, checker)
	checkout.form.Set("invoice_type", string(invoice.PreferenceCompany))
	checkout.form.Set("invoice_company_delivery", string(invoice.CompanyDeliveryMobile))
	checkout.form.Set("invoice_company_name", "Buyer Company")
	checkout.form.Set("invoice_tax_id", "04595252")
	return checkout
}

func TestCompanyMobileUsesEveryCarrierVerdict(t *testing.T) {
	for _, status := range []invoice.CarrierStatus{invoice.CarrierExists, invoice.CarrierMissing, invoice.CarrierUnknown} {
		t.Run(strconv.Itoa(int(status)), func(t *testing.T) {
			checker := &checkoutCarrier{status: status}
			checkout := companyCarrierCheckout(t, checker)
			w := checkout.post(t)
			if checker.calls != 1 || checker.barcode != "/ABC+123" {
				t.Fatal("company mobile bypassed the shared provider check")
			}
			if status == invoice.CarrierExists {
				if w.Code != http.StatusSeeOther {
					t.Fatalf("known Y = %d", w.Code)
				}
				assertCompanyCarrierSnapshot(t, w.Header().Get("Location"))
				if again := checkout.post(t); again.Code != http.StatusSeeOther || checker.calls != 1 {
					t.Fatal("company idempotent replay rechecked its carrier")
				}
				return
			}
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("unconfirmed company = %d", w.Code)
			}
			for _, want := range []string{"Buyer Company", "04595252", " /abc+123 ", `name="invoice_company_delivery" value="mobile" checked`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("refusal lost %q", want)
				}
			}
			checkout.assertUnplaced(t)
			checkout.form.Set("invoice_carrier_continue", "1")
			w = checkout.post(t)
			if status == invoice.CarrierMissing {
				if w.Code != http.StatusUnprocessableEntity {
					t.Fatal("continue bypassed company known N")
				}
				checkout.assertUnplaced(t)
			} else {
				if w.Code != http.StatusSeeOther || checker.calls != 2 {
					t.Fatal("company unknown needs a fresh checked explicit continuation")
				}
				assertCompanyCarrierSnapshot(t, w.Header().Get("Location"))
			}
		})
	}
}

func TestCompanyUnknownPlatformChoiceKeepsTheCompanyIdentity(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierUnknown}
	checkout := companyCarrierCheckout(t, checker)
	if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity {
		t.Fatal("unknown did not ask for a choice")
	}
	checkout.form.Set("update", "invoice_member")
	w := checkout.post(t)
	if w.Code != http.StatusOK || checker.calls != 1 {
		t.Fatal("platform chooser should only redisplay")
	}
	for _, want := range []string{`name="invoice_company_name"`, `value="Buyer Company"`, `value="04595252"`, `name="invoice_company_delivery" value="email" checked`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("platform choice lost %q", want)
		}
	}
	checkout.form.Del("update")
	checkout.form.Set("invoice_company_delivery", string(invoice.CompanyDeliveryEmail))
	w = checkout.post(t)
	if w.Code != http.StatusSeeOther || checker.calls != 1 {
		t.Fatal("company platform choice rechecked or refused")
	}
	number := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/orders/"), "/pay")
	var kind, name, tax, carrier string
	if err := pool.QueryRow(t.Context(), `SELECT ip.invoice_type,ip.customer_name,ip.tax_id,coalesce(ip.carrier_code,'') FROM invoice_preferences ip JOIN orders o ON o.id=ip.order_id WHERE o.order_number=$1`, number).Scan(&kind, &name, &tax, &carrier); err != nil {
		t.Fatal(err)
	}
	if kind != "company" || name != "Buyer Company" || tax != "04595252" || carrier != "" {
		t.Fatal("platform alternative discarded company identity or retained the unknown barcode")
	}
}

func TestCompanyContinuationCannotBypassNewMissingVerdictOrLimit(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierUnknown}
	checkout := companyCarrierCheckout(t, checker)
	checkout.post(t)
	checker.status = invoice.CarrierMissing
	checkout.form.Set("invoice_carrier_continue", "1")
	for range 2 {
		if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity {
			t.Fatal("known N bypassed")
		}
	}
	checker.status = invoice.CarrierUnknown
	if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity || checker.calls != 3 {
		t.Fatal("company continuation bypassed limiter")
	}
	checkout.assertUnplaced(t)
}

func TestCompanyCarrierCheckFollowsLocalIdentityAndFormatValidation(t *testing.T) {
	for _, field := range []string{"invoice_tax_id", "invoice_company_name", "invoice_carrier", "invoice_company_delivery"} {
		t.Run(field, func(t *testing.T) {
			checker := &checkoutCarrier{status: invoice.CarrierExists}
			checkout := companyCarrierCheckout(t, checker)
			checkout.form.Set(field, "invalid")
			if field == "invoice_company_name" {
				checkout.form.Set(field, "")
			}
			checkout.form.Set("invoice_carrier_continue", "1")
			if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity || checker.calls != 0 {
				t.Fatal("invalid company fields reached the provider or placed an order")
			}
			checkout.assertUnplaced(t)
		})
	}
}

func assertCompanyCarrierSnapshot(t *testing.T, location string) {
	t.Helper()
	number := strings.TrimSuffix(strings.TrimPrefix(location, "/orders/"), "/pay")
	var kind, name, tax, carrier string
	if err := pool.QueryRow(t.Context(), `SELECT ip.invoice_type,ip.customer_name,ip.tax_id,ip.carrier_code FROM invoice_preferences ip JOIN orders o ON o.id=ip.order_id WHERE o.order_number=$1`, number).Scan(&kind, &name, &tax, &carrier); err != nil {
		t.Fatal(err)
	}
	if kind != "company" || name != "Buyer Company" || tax != "04595252" || carrier != "/ABC+123" {
		t.Fatal("company carrier verdict lost the immutable buyer or delivery")
	}
}
