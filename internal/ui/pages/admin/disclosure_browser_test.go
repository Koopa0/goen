package admin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestAdminContentDisclosuresWorkWithTheKeyboard(t *testing.T) {
	port := os.Getenv("CDP_PORT")
	if port == "" {
		t.Skip("set CDP_PORT to run the native browser disclosure regression")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(root, "assets")))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx := i18n.WithLocale(r.Context(), i18n.En)
		if r.URL.Query().Get("lang") == "zh-Hant" {
			ctx = i18n.WithLocale(r.Context(), i18n.ZhHant)
		}
		var component templ.Component
		switch r.URL.Path {
		case "/health":
			component = admin.Health(layouts.Page{}, &admin.WorkerHealthView{})
		case "/twofactor":
			component = pages.TwoFactor(layouts.Page{}, pages.TwoFactorView{
				Enabled: true, Enrolling: true, URI: "otpauth://totp/goen:disclosure-fixture",
			})
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if renderErr := component.Render(ctx, w); renderErr != nil {
			t.Errorf("render disclosure fixture: %v", renderErr)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	//nolint:gosec // G204: fixed regression script, test-owned server, caller's Chrome debugging port.
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "admin-disclosure-check.mjs"), server.URL, port)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("admin content disclosures: %v\n%s", err, output)
	}
	t.Log(string(output))
}
