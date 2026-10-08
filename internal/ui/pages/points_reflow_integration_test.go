//go:build integration

package pages

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPointsRedemptionButtonReflowsAtDoubleText(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome and Node")
	}
	chrome, err := exec.Command("../../../scripts/resolve-chrome.sh").Output()
	if err != nil {
		t.Skip("Chrome unavailable")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	fixtures := make(map[string]string)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		var rendered bytes.Buffer
		view := PointsView{Redeemable: 200, Minimum: 100, PerCredit: 10}
		ctx := i18n.WithLocale(t.Context(), locale)
		if err := Points(layouts.Page{}, view).Render(ctx, &rendered); err != nil {
			t.Fatal(err)
		}
		fixtures[locale.Tag()] = rendered.String()
	}
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "scripts/points-reflow-check.mjs")
	command.Dir = filepath.Join("..", "..", "..")
	command.Env = append(command.Environ(), "CHROME="+string(bytes.TrimSpace(chrome)))
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("points redemption reflow: %v", err)
	}
}
