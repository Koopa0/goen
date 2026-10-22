package warranty

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestLine_CoverText: the warranty term states the parcel's expiry, in both
// locales: with the expiry, before delivery, as months, and with no warranty.
func TestLine_CoverText(t *testing.T) {
	t.Parallel()
	end := shoptime.Date{Year: 2028, Month: time.October, Day: 5}
	for _, tt := range []struct {
		name   string
		line   Line
		zh, en string
	}{
		{name: "delivered", line: Line{HasTerm: true, Months: 24, CoverEnds: &end}, zh: "保固 2 年，到 2028-10-05（從送達日起算）", en: "2-year warranty, until 2028-10-05 (from delivery)"},
		{name: "before delivery", line: Line{HasTerm: true, Months: 24}, zh: "保固 2 年，從送達日起算", en: "2-year warranty, from delivery"},
		{name: "month term", line: Line{HasTerm: true, Months: 18, CoverEnds: &end}, zh: "保固 18 個月，到 2028-10-05（從送達日起算）", en: "18-month warranty, until 2028-10-05 (from delivery)"},
		{name: "no warranty", line: Line{}},
	} {
		for _, locale := range []struct {
			locale i18n.Locale
			want   string
		}{{locale: i18n.ZhHant, want: tt.zh}, {locale: i18n.En, want: tt.en}} {
			t.Run(tt.name+"/"+locale.locale.Tag(), func(t *testing.T) {
				t.Parallel()
				if got := tt.line.CoverText(i18n.WithLocale(t.Context(), locale.locale)); got != locale.want {
					t.Errorf("CoverText() = %q, want %q", got, locale.want)
				}
			})
		}
	}
}

// TestWarrantyOrderConfirmationNextSteps: registration shows the unit and the
// remaining steps: the confirmation names the registered unit and its expiry,
// links on to all warranties, and the ready list has one registration link per
// item.
func TestWarrantyOrderConfirmationNextSteps(t *testing.T) {
	t.Parallel()
	end := shoptime.Date{Year: 2028, Month: time.October, Day: 5}
	for _, locale := range []struct {
		locale                                             i18n.Locale
		confirmation, done, all, serial, heading, register string
	}{
		{locale: i18n.ZhHant, confirmation: "已登錄：Pixelight，保固到 2028-10-05。", done: "這筆訂單的商品都已登錄保固。", all: "查看所有保固", serial: "序號 SN-SECOND", heading: "可以登錄", register: "登錄保固"},
		{locale: i18n.En, confirmation: "Registered: Pixelight, under warranty until 2028-10-05.", done: "Every item on this order has been registered for warranty.", all: "View all warranties", serial: "Serial SN-SECOND", heading: "Ready to register", register: "Register warranty"},
	} {
		t.Run(locale.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale.locale)
			body := renderWarrantyInLocale(t, ctx, WarrantyOrder(frame.Page{}, OrderView{
				Number: "GO-TEST", Lines: []Line{{ID: "phone", Name: "Pixelight", HasTerm: true, Months: 24, Delivered: 2, Registered: 2, CoverEnds: &end}},
				RegisteredUnit: &Warranty{Name: "Pixelight", Unit: 2, Serial: "SN-SECOND", ExpiresOn: end},
			}))
			for _, want := range []string{locale.confirmation, locale.done, locale.serial, `<a href="/account/warranty">` + locale.all + `</a>`, `role="status" id="warranty-registered"`} {
				if !strings.Contains(body, want) {
					t.Errorf("warranty confirmation lacks %q", want)
				}
			}
			if strings.Count(body, locale.done) != 1 || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyAllDone)) {
				t.Error("completed registration repeats a line-level completion message")
			}
			ready := renderWarrantyInLocale(t, ctx, WarrantyList(frame.Page{}, WarrantyListView{Ready: []WarrantyReady{
				{Number: "GO-FIRST", Line: Line{ID: "phone", Name: "Pixelight <Pro>", HasTerm: true, Months: 24, CoverEnds: &end}},
				{Number: "GO-SECOND", Line: Line{ID: "tablet", Name: "Tablet", HasTerm: true, Months: 24, CoverEnds: &end}},
			}}))
			for _, want := range []string{`id="warranty-ready-heading">` + locale.heading, `href="/account/warranty/GO-FIRST#serial-phone"`, `href="/account/warranty/GO-SECOND#serial-tablet"`, "Pixelight &lt;Pro&gt;", "2028-10-05", locale.register} {
				if !strings.Contains(ready, want) {
					t.Errorf("ready warranties lack %q", want)
				}
			}
			if strings.Count(ready, `#serial-`) != 2 {
				t.Error("ready list does not have one registration link per item")
			}
			empty := renderWarrantyInLocale(t, ctx, WarrantyList(frame.Page{}, WarrantyListView{}))
			if strings.Contains(empty, `id="warranty-ready-heading"`) {
				t.Error("empty ready list has a registration section")
			}
			noTerm := renderWarrantyInLocale(t, ctx, WarrantyOrder(frame.Page{}, OrderView{Number: "GO-NONE", Lines: []Line{{Name: "No cover"}}}))
			if strings.Count(noTerm, i18n.T(ctx, i18n.KeyWarrantyNoTerm)) != 1 {
				t.Error("no-warranty explanation is duplicated")
			}
		})
	}
}
