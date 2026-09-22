package pages

import "testing"

func TestCheckoutAddressConstraintsReachTheForm(t *testing.T) {
	controls := renderedAutofillControls(t)
	for id, pattern := range map[string]string{"postal_code": "[0-9]{3,6}", "city": ".{1,20}", "district": ".{1,20}"} {
		field := controls[id]
		if field["pattern"] != pattern {
			t.Errorf("%s pattern = %q, want %q", id, field["pattern"], pattern)
		}
		if field["aria-describedby"] != id+"-error" {
			t.Errorf("%s has no attached constraint feedback", id)
		}
	}
	if controls["postal_code"]["maxlength"] != "6" {
		t.Error("postal code has no six-digit input bound")
	}
	for _, id := range []string{"city", "district"} {
		if controls[id]["maxlength"] != "" {
			t.Errorf("%s uses UTF-16 maxlength for a Unicode-character limit", id)
		}
	}
}
