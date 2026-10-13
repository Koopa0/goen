package site

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPrivacyDisclosuresRenderOnce(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			doc := policies["privacy"].For(locale)
			var out strings.Builder
			if err := pages.Policy(layouts.Page{}, doc).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			seen := make(map[string]bool)
			for _, section := range doc.Sections {
				for _, paragraph := range section.Body {
					if seen[paragraph] {
						continue
					}
					seen[paragraph] = true
					if count := strings.Count(body, "<p>"+html.EscapeString(paragraph)+"</p>"); count != 1 {
						t.Errorf("privacy disclosure %q rendered %d times, want 1", paragraph, count)
					}
				}
			}
		})
	}
}

func TestPrivacyDisclosesCollectedAndRetainedData(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			doc := policies["privacy"].For(locale)
			var out strings.Builder
			if err := pages.Policy(layouts.Page{}, doc).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			disclosures := []string{"訂閱電子報時", "電子郵件、語言、確認與退訂狀態", "IP 位址", "瀏覽器與裝置資訊", "公司統一編號", "手機條碼", "捐贈碼", "顧客姓名", "綠界電子發票平台", "字型由 goen 自己提供", "不會連到 Google 的伺服器", "不可變更的發票快照會保留", "保固登錄與商品序號會保留", "未驗證信箱的訂閱不會隨刪帳移除", "退訂連結停止寄送"}
			if locale == i18n.En {
				disclosures = []string{"subscribe to the newsletter", "email, language, confirmation and unsubscribe status", "IP address", "browser and device details", "company tax ID", "mobile barcode", "donation code", "customer name", "ECPay", "goen serves the website fonts itself", "loading them sends nothing to Google", "immutable invoice snapshots remain", "Warranty registrations and product serial numbers remain", "Subscriptions for an unverified email are not removed", "unsubscribe link in a newsletter to stop delivery"}
			}
			for _, disclosure := range disclosures {
				if !strings.Contains(body, disclosure) {
					t.Errorf("privacy omits %q", disclosure)
				}
			}
			for _, host := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
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

func TestPrivacyThirdPartySectionDisclosesGoogleSignIn(t *testing.T) {
	for _, tc := range []struct {
		locale  i18n.Locale
		heading string
		want    string
	}{
		{i18n.ZhHant, "第三方處理", "選擇用 Google 登入時，由 Google 確認你的身分；我們會從 Google 取得你的 Google 帳號識別碼、電子郵件、電子郵件是否已驗證與姓名，用來建立或登入你的 goen 帳號。"},
		{i18n.En, "Third-party processing", "If you sign in with Google, Google confirms who you are; we receive your Google account identifier, email address, whether that email is verified, and your name, and use them to create or sign in to your goen account."},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			var found int
			for _, section := range policies["privacy"].For(tc.locale).Sections {
				if section.Heading != tc.heading {
					continue
				}
				for _, paragraph := range section.Body {
					if paragraph == tc.want {
						found++
					}
				}
			}
			if found != 1 {
				t.Errorf("%s discloses Google sign-in %d times, want once", tc.heading, found)
			}
		})
	}
}
