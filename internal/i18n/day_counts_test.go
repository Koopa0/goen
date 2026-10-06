package i18n

import (
	"strings"
	"testing"
)

func TestTheReturnsDeskCountsDaysWithTian(t *testing.T) {
	for _, k := range []Key{
		KeyAdminReturnWindowGoodwill,
		KeyAdminRetLateHint,
		KeyAdminRetErrStatutoryReject,
		KeyAdminRetErrUnmetApprove,
	} {
		zh := messages[k].ZhHant
		if strings.Contains(zh, "8–14 日") || strings.Contains(zh, "14 日") {
			t.Errorf("%s counts days with 日: %q", k, zh)
		}
		if !strings.Contains(zh, "8–14 天") && !strings.Contains(zh, "14 天") {
			t.Errorf("%s has no day count in 天: %q", k, zh)
		}
	}
}

// 七日內 is the statute's wording (消保法 §19) and stays 日.
func TestTheStatutoryRejectionKeepsSevenDaysInTheStatutesWords(t *testing.T) {
	if zh := messages[KeyAdminRetErrStatutoryReject].ZhHant; !strings.Contains(zh, "七日內") {
		t.Errorf("the statutory sentence lost 七日內: %q", zh)
	}
}

func TestDepartmentsAreNamedGuanBie(t *testing.T) {
	for _, k := range []Key{KeyCategoryNav, KeySectionCategories} {
		if got := messages[k]; got.ZhHant != "館別" || got.En != "Departments" {
			t.Errorf("%s = %q / %q, want 館別 / Departments", k, got.ZhHant, got.En)
		}
	}
}
