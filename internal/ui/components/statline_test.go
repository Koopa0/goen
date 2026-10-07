package components

import (
	"strings"
	"testing"
)

func renderStatLine(t *testing.T, stats []Stat, variant StatLineVariant) string {
	t.Helper()
	var b strings.Builder
	if err := StatLine(stats, variant).Render(t.Context(), &b); err != nil {
		t.Fatalf("StatLine: %v", err)
	}
	return b.String()
}

func TestStatLinePrintsOnlyStatsWithAValue(t *testing.T) {
	t.Parallel()
	one := Stat{Label: "庫存保留", Value: StatCount(60, "分鐘")}
	absent := Stat{Label: "免運門檻"}

	if got := renderStatLine(t, []Stat{absent, absent}, StatLineWide); got != "" {
		t.Errorf("StatLine with every value absent = %q, want nothing", got)
	}
	got := renderStatLine(t, []Stat{one, absent}, StatLineWide)
	if n := strings.Count(got, "<dt>"); n != 1 || strings.Contains(got, "免運門檻") {
		t.Errorf("StatLine with one of two values absent printed %d items:\n%s", n, got)
	}
	four := []Stat{one, one, one, one}
	if n := strings.Count(renderStatLine(t, four, StatLinePlain), "<dt>"); n != 4 {
		t.Errorf("StatLine of four stats printed %d items, want 4", n)
	}
}

func TestStatLineRefusesMoreThanFourStats(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("StatLine of five stats did not panic")
		}
	}()
	s := Stat{Label: "a", Value: StatCount(1, "x")}
	renderStatLine(t, []Stat{s, s, s, s, s}, StatLinePlain)
}

func TestStatLineJoinsANumberToItsUnitWithANoBreakSpace(t *testing.T) {
	t.Parallel()
	got := renderStatLine(t, []Stat{{Label: "庫存保留", Value: StatCount(60, "分鐘")}}, StatLinePlain)
	if want := "60 <small>分鐘</small>"; !strings.Contains(got, want) {
		t.Errorf("StatLine count = %q, want it to contain %q", got, want)
	}
}

func TestStatLineMoneyLeadsWithItsCurrency(t *testing.T) {
	t.Parallel()
	got := renderStatLine(t, []Stat{{Label: "免運門檻", Value: StatMoney(300000)}}, StatLinePlain)
	if want := `<small class="ui-statline__pre">NT$</small>3,000`; !strings.Contains(got, want) {
		t.Errorf("StatLine money = %q, want it to contain %q", got, want)
	}
	if StatMoney(-1).present() {
		t.Error("a negative amount is a stat value, want absent")
	}
}

func TestStatLineVariantsAndNoStyleAttribute(t *testing.T) {
	t.Parallel()
	s := []Stat{{Label: "a", Value: StatCount(1, "x"), Note: "n"}}
	for variant, class := range map[StatLineVariant]string{
		StatLinePlain: `<dl class="ui-statline">`,
		StatLineWide:  `<dl class="ui-statline ui-statline--wide">`,
		StatLinePairs: `<dl class="ui-statline ui-statline--pairs">`,
	} {
		got := renderStatLine(t, s, variant)
		if !strings.Contains(got, class) {
			t.Errorf("StatLine(%q) = %q, want %q", variant, got, class)
		}
		if strings.Contains(got, "style=") {
			t.Errorf("StatLine(%q) carries a style attribute: %s", variant, got)
		}
		if !strings.Contains(got, `<span class="ui-statline__note">n</span>`) {
			t.Errorf("StatLine(%q) lacks its note: %s", variant, got)
		}
	}
}

func renderGlance(t *testing.T, stats []LinkedStat) string {
	t.Helper()
	var b strings.Builder
	if err := GlanceStatLine(stats).Render(t.Context(), &b); err != nil {
		t.Fatalf("GlanceStatLine: %v", err)
	}
	return b.String()
}

func TestGlanceStatLineLinksTheLabelNotTheFigure(t *testing.T) {
	t.Parallel()
	got := renderGlance(t, []LinkedStat{
		{Stat: Stat{Label: "待處理", Value: StatNumber(3)}, Href: "/admin/orders?status=pending"},
		{Stat: Stat{Label: "上架商品", Value: StatNumber(0)}, Href: "/admin/products"},
	})
	for _, want := range []string{
		`<dl class="ui-statline ui-statline--glance">`,
		`<dt><a href="/admin/orders?status=pending">待處理</a></dt><dd>3`,
		`<dt><a href="/admin/products">上架商品</a></dt><dd>0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("GlanceStatLine = %s\nwant it to contain %q", got, want)
		}
	}
}

func TestGlanceStatLineRequiresAnHref(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("GlanceStatLine with a stat that has no Href did not panic")
		}
	}()
	renderGlance(t, []LinkedStat{{Stat: Stat{Label: "待處理", Value: StatNumber(3)}}})
}

func TestStatNumberPrintsABareCount(t *testing.T) {
	t.Parallel()
	got := renderStatLine(t, []Stat{{Label: "訂單數", Value: StatNumber(0)}}, StatLinePlain)
	if want := "<dd>0"; !strings.Contains(got, want) {
		t.Errorf("StatLine with StatNumber(0) = %s\nwant it to contain %q", got, want)
	}
	if got := renderStatLine(t, []Stat{{Label: "訂單數", Value: StatNumber(-1)}}, StatLinePlain); got != "" {
		t.Errorf("StatLine with StatNumber(-1) = %q, want nothing", got)
	}
}
