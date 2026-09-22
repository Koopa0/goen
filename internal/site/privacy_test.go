package site

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPrivacyDisclosesCollectedAndRetainedData(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			doc := policies["privacy"].For(locale)
			var out strings.Builder
			if err := pages.Policy(layouts.Page{}, doc).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			disclosures := []string{"訂閱電子報時", "Email、語言、確認與退訂狀態", "IP 位址", "User-Agent", "公司統一編號", "手機條碼", "顧客姓名", "綠界電子發票平台", "字型由 goen 本站提供", "不可變更的發票快照會保留", "保固登錄與商品序號會保留", "未驗證信箱的訂閱不會隨刪帳移除", "退訂連結停止寄送"}
			if locale == i18n.En {
				disclosures = []string{"subscribe to the newsletter", "email, language, confirmation and unsubscribe status", "IP address", "User-Agent", "company tax ID", "mobile barcode", "customer name", "ECPay", "goen serves the website fonts itself", "immutable invoice snapshots remain", "Warranty registrations and product serial numbers remain", "Subscriptions for an unverified email are not removed", "unsubscribe link in a newsletter to stop delivery"}
			}
			for _, disclosure := range disclosures {
				if !strings.Contains(body, disclosure) {
					t.Errorf("privacy omits %q", disclosure)
				}
			}
			for _, host := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
				if !strings.Contains(body, host) {
					t.Errorf("privacy omits the self-hosted font boundary %q", host)
				}
				if strings.Contains(body, `href="https://`+host) {
					t.Errorf("privacy claims self-hosted fonts but renders a request to %q", host)
				}
			}
			for _, misleading := range []string{"不含個人識別資訊", "stripped of anything identifying you"} {
				if strings.Contains(body, misleading) {
					t.Errorf("privacy overstates financial-data erasure: %q", misleading)
				}
			}
		})
	}
}
