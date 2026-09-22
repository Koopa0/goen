//go:build integration

package twofactor_test

import (
	"bytes"
	"encoding/base64"
	"html"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"rsc.io/qr"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/web"
)

func TestEnrolmentQRMatchesTheOneTimeURI(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			s := twofactor.NewStore(pool, testKey)
			userID, email := staff(t)
			h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
			ctx := account.WithUser(i18n.WithLocale(t.Context(), locale), account.User{ID: userID, Email: email, Role: "admin"})
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/verify/enrol", strings.NewReader(""))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			web.Compress(http.HandlerFunc(h.Enrol)).ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "" {
				t.Fatalf("enrolment status/encoding = %d/%q", w.Code, w.Header().Get("Content-Encoding"))
			}
			body := w.Body.String()
			image := regexp.MustCompile(`<img class="goen-twofa__qr" src="data:image/png;base64,([^"]+)"`).FindStringSubmatch(body)
			uri := regexp.MustCompile(`<code class="goen-twofa__uri">([^<]+)</code>`).FindStringSubmatch(body)
			if len(image) != 2 || len(uri) != 2 {
				t.Fatal("enrolment must contain both an inline QR image and the manual URI")
			}
			data, err := base64.StdEncoding.DecodeString(image[1])
			if err != nil {
				t.Fatal(err)
			}
			if _, err = png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatalf("inline image is not a PNG: %v", err)
			}
			want, err := qr.Encode(html.UnescapeString(uri[1]), qr.M)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, want.PNG()) {
				t.Error("QR image does not encode the displayed provisioning URI")
			}
			if !strings.Contains(body, `class="goen-twofa__secret"`) || !strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyTwoFAQRCode))) {
				t.Error("enrolment lacks its manual secret or localized QR description")
			}
			next := httptest.NewRecorder()
			h.Challenge(next, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/verify", http.NoBody))
			if next.Code != http.StatusOK {
				t.Fatalf("subsequent challenge status = %d", next.Code)
			}
			for _, secret := range []string{"data:image/png;base64,", "otpauth://", `class="goen-twofa__secret"`} {
				if strings.Contains(next.Body.String(), secret) {
					t.Errorf("subsequent challenge repeats enrolment material: %s", secret)
				}
			}
		})
	}
}
