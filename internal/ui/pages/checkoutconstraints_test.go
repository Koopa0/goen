package pages

import "testing"

func TestCheckoutAddressConstraintsReachTheForm(t *testing.T) {
	controls := renderedAutofillControls(t)
	for id, pattern := range map[string]string{"postal_code": `\s*[0-9]{3,6}\s*`, "city": `\s*\S(?:.{0,18}\S)?\s*`, "district": `\s*\S(?:.{0,18}\S)?\s*`} {
		field := controls[id]
		if field["pattern"] != pattern {
			t.Errorf("%s pattern = %q, want %q", id, field["pattern"], pattern)
		}
		if field["aria-describedby"] != id+"-error" {
			t.Errorf("%s has no attached constraint feedback", id)
		}
	}
	// The server trims before it counts, so a maxlength would truncate a pasted
	// " 110234" into a different postcode, and UTF-16 units miscount city names.
	for _, id := range []string{"postal_code", "city", "district"} {
		if _, ok := controls[id]["maxlength"]; ok && controls[id]["maxlength"] != "" {
			t.Errorf("%s carries a maxlength the server does not have", id)
		}
	}
}
