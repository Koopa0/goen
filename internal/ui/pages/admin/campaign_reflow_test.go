package admin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestCampaignEditorReflowsInBothLanguages(t *testing.T) {
	debuggingPort := os.Getenv("CDP_PORT")
	if debuggingPort == "" {
		t.Skip("set CDP_PORT to the debugging port of a running Chrome to run the campaign editor's native browser regression")
	}
	fixtures := t.TempDir()
	view := CampaignView{
		Slug: "layout-campaign",
		CampaignDetail: CampaignDetail{
			Title: "Layout campaign", StartsAtInput: "2026-10-01T09:00", EndsAtInput: "2026-10-31T23:59",
			Active: true, Running: true, Sellable: true,
		},
		Results:  results(5, 1, 2, 3, 4, 5, 3, 4, 5, 6, 7),
		Products: []CampaignProduct{{Slug: "pixelight", Name: "Pixelight"}},
	}
	for _, locale := range i18n.Locales() {
		if err := os.WriteFile(filepath.Join(fixtures, locale.Tag()+".html"), []byte(renderResults(t, locale, &view)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "scripts", "campaign-reflow.mjs"), root, fixtures, debuggingPort) //nolint:gosec // G204: fixed test runner; the port is the debugging port of the browser the caller started.
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("campaign editor reflow: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}
