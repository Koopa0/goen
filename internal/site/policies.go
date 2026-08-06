package site

import "github.com/koopa0/goen/internal/ui/pages"

// policies is the static policy documents, keyed by their path segment.
//
// Prose in Go rather than in a database: unlike the FAQ, these change when a
// LAWYER changes them, not when support notices a question recurring. A deploy
// is the right amount of ceremony for that, and it puts the text under review
// alongside the code that has to honour it.
//
// Every clause here describes what the code actually does. Where a commercial
// decision has not been made — the return window, who pays return postage, the
// warranty term — the document says so rather than inventing a number the shop
// would then be held to. Those are the owner's to set.
var policies = map[string]pages.PolicyDoc{
	"returns": {
		Title:   "退換貨政策",
		Summary: "goen 的退貨條件、流程與退款方式。",
		Sections: []pages.PolicySection{
			{
				Heading: "可以退什麼",
				Body: []string{
					"只有「已出貨」的商品可以申請退貨,而且數量以實際出貨的數量為上限。這不是政策上的選擇,是系統本身的規則:尚未離開倉庫的商品沒有東西可以退。",
					"還沒出貨的訂單請直接聯絡我們取消,不需要走退貨流程。",
				},
			},
			{
				Heading: "怎麼申請",
				Body: []string{
					"在訂單頁點「申請退貨」,選擇要退回的商品與數量,填寫原因後送出。同一筆訂單一次只能有一件處理中的申請。",
					"我們收到申請後會審核並回覆結果,同意或不同意都會說明原因。",
				},
			},
			{
				Heading: "退款",
				Body: []string{
					"退貨經同意後,系統會立即向 Stripe 發出退款,金額依訂單本身的單價計算。實際入帳時間由發卡銀行決定,通常是數個工作天。",
					"退款一定會退回原付款方式,不會改用其他管道。",
				},
			},
			{
				Heading: "尚未確定的條款",
				Pending: true,
				Body: []string{
					"鑑賞期天數、退貨運費由誰負擔,以及包裝拆封後是否影響退貨,這些條款尚未確定。正式營運前會在本頁公告,在那之前請以聯絡我們取得的說明為準。",
				},
			},
		},
	},
	"payment": {
		Title:   "付款說明",
		Summary: "goen 接受的付款方式與安全性說明。",
		Sections: []pages.PolicySection{
			{
				Heading: "付款方式",
				Body: []string{
					"目前接受信用卡付款,由 Stripe 處理。付款頁面在 Stripe 的網域上,goen 的伺服器不會接觸、也不會儲存您的卡片資料。",
					"我們只會保留卡別與末四碼,用於在訂單頁辨識是哪一張卡付的款。",
				},
			},
			{
				Heading: "什麼時候扣款",
				Body: []string{
					"在 Stripe 頁面完成付款時就會扣款。goen 只在收到 Stripe 經過簽章驗證的通知後,才把訂單標記為已付款 —— 回到網站看到的頁面本身不代表付款成功。",
				},
			},
			{
				Heading: "庫存保留",
				Body: []string{
					"送出訂單時系統會保留庫存 30 分鐘。超過時間未完成付款,商品會回到架上,但訂單仍然存在,可以重新付款(若庫存還在)。",
				},
			},
		},
	},
	"warranty": {
		Title:   "保固說明",
		Summary: "goen 販售商品的保固方式。",
		Sections: []pages.PolicySection{
			{
				Heading: "保固範圍",
				Body: []string{
					"goen 販售的商品由原廠提供保固。各商品的保固內容寫在該商品頁面上,以商品頁的說明為準。",
				},
			},
			{
				Heading: "尚未確定的條款",
				Pending: true,
				Body: []string{
					"保固期限、送修流程,以及維修期間是否提供替代機,這些尚未確定。正式營運前會在本頁公告。",
				},
			},
		},
	},
	"privacy": {
		Title:   "隱私權政策",
		Summary: "goen 蒐集哪些資料、為什麼蒐集,以及您可以怎麼處理它。",
		Sections: []pages.PolicySection{
			{
				Heading: "我們蒐集什麼",
				Body: []string{
					"下單時:收件人姓名、電話、地址與 Email,用於出貨與聯絡。",
					"註冊時:Email 與密碼。密碼以 argon2id 雜湊儲存,任何人都無法從資料庫還原它,包含我們。",
					"付款時:卡片資料由 Stripe 處理,不經過 goen。我們只收到卡別與末四碼。",
				},
			},
			{
				Heading: "我們不做什麼",
				Body: []string{
					"不將您的個人資料出售或提供給第三方作行銷用途。",
					"不在網站上使用第三方追蹤或廣告 cookie。goen 使用的 cookie 只有購物車、登入狀態,以及訂單瀏覽權限這三種。",
				},
			},
			{
				Heading: "刪除您的資料",
				Body: []string{
					"在會員中心可以要求刪除帳號。系統會清除您的姓名、Email、電話、地址與訂單上的收件資訊。",
					"訂單本身的財務紀錄會保留,不含個人識別資訊 —— 這是會計與稅務要求,不是我們的選擇。已公開的商品評價也會保留,但不再與您的帳號關聯。",
				},
			},
		},
	},
	"terms": {
		Title:   "服務條款",
		Summary: "使用 goen 的基本約定。",
		Sections: []pages.PolicySection{
			{
				Heading: "訂單成立",
				Body: []string{
					"送出訂單即表示要約,我們確認庫存與付款後訂單成立。若商品在您付款前售罄,我們會取消訂單並全額退款。",
				},
			},
			{
				Heading: "價格與標示",
				Body: []string{
					"網站上顯示的價格為新台幣含稅價。若因系統錯誤導致標價明顯有誤,我們保留取消該筆訂單並退款的權利,並會主動聯絡您說明。",
				},
			},
			{
				Heading: "帳號",
				Body: []string{
					"請妥善保管您的密碼。變更密碼會同時登出其他所有裝置。",
				},
			},
			{
				Heading: "尚未確定的條款",
				Pending: true,
				Body: []string{
					"準據法、爭議解決方式與管轄法院尚未確定,正式營運前會在本頁補上。",
				},
			},
		},
	},
}
