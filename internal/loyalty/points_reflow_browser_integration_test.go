//go:build integration

package loyalty

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// TestPointsRedemptionButtonReflows: the points redemption button reflows at
// double text size.
func TestPointsRedemptionButtonReflows(t *testing.T) {
	chrome := uitest.LocalChrome(t)
	fixtures := make(map[string]string)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		var rendered bytes.Buffer
		view := PointsView{Redeemable: 200, Minimum: 100, PerCredit: 10}
		ctx := i18n.WithLocale(t.Context(), locale)
		if renderErr := Points(frame.Page{}, view).Render(ctx, &rendered); renderErr != nil {
			t.Fatal(renderErr)
		}
		fixtures[locale.Tag()] = rendered.String()
	}
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "scripts/points-reflow-check.mjs")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(command.Environ(), "CHROME="+chrome)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("points redemption reflow: %v", err)
	}
}
