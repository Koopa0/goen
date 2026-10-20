//go:build integration

package db_test

import "testing"

// TestOnlySignInWritesKnownDevices holds known_devices to the storefront's
// sign-in. store issues a row with its account and expiry, counts its misses
// and deletes it, and can neither extend a row's life, move it to another
// account, nor issue one already counted down. The back office and reporting
// cannot read a row, let alone write one: a digest beside its account is half of
// what a stolen cookie needs, and a row is a budget exemption for that account.
func TestOnlySignInWritesKnownDevices(t *testing.T) {
	const device = `known_devices (digest, user_id, expires_at)
		VALUES (sha256('boundary-device'::bytea), ` + ming + `, now() + interval '1 day')`
	const issued = `INSERT INTO ` + device + `;`

	expectAccepted(t, "store issues a device", `SET LOCAL ROLE store; `+issued)
	expectAccepted(t, "store counts a miss and forgets the device", issued+`
		SET LOCAL ROLE store;
		UPDATE known_devices SET misses = misses + 1 WHERE digest = sha256('boundary-device'::bytea);
		SELECT digest FROM known_devices WHERE user_id = `+ming+`;
		DELETE FROM known_devices WHERE digest = sha256('boundary-device'::bytea);`)

	for _, tc := range []struct{ what, stmt string }{
		{"store extends a device's life", issued + `SET LOCAL ROLE store;
			UPDATE known_devices SET expires_at = now() + interval '10 years'`},
		{"store moves a device to another account", issued + `SET LOCAL ROLE store;
			UPDATE known_devices SET user_id = ` + hua},
		{"store issues a device with misses already spent", `SET LOCAL ROLE store;
			INSERT INTO known_devices (digest, user_id, misses, expires_at)
			VALUES (sha256('boundary-device'::bytea), ` + ming + `, 0, now() + interval '1 day')`},
		{"admin reads devices", issued + `SET LOCAL ROLE admin; SELECT digest FROM known_devices`},
		{"admin issues a device", `SET LOCAL ROLE admin; ` + issued},
		{"admin forgets a device", issued + `SET LOCAL ROLE admin; DELETE FROM known_devices`},
		{"reporting reads devices", issued + `SET LOCAL ROLE reporting; SELECT digest FROM known_devices`},
	} {
		expectRefusal(t, tc.what, tc.stmt, "42501", "")
	}
}

// TestPromotionForgetsTheBrowsersAnAccountRemembered: a browser remembered
// before the account was given the back office was vouched for by somebody the
// shop had not yet decided to trust, as its sessions were.
func TestPromotionForgetsTheBrowsersAnAccountRemembered(t *testing.T) {
	expectAccepted(t, "promoting ming forgets ming's browsers", `
		INSERT INTO known_devices (digest, user_id, expires_at)
		VALUES (sha256('promoted-device'::bytea), `+ming+`, now() + interval '1 day');
		SET LOCAL ROLE admin;
		SELECT upsert_staff('ming@example.com', '', 'staff');
		RESET ROLE;
		DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM known_devices WHERE digest = sha256('promoted-device'::bytea)) THEN
				RAISE EXCEPTION 'the promoted account still remembers a browser';
			END IF;
		END $$;`)
}
