package site

import (
	"fmt"

	"github.com/koopa0/goen/internal/ui/pages"
)

// policies is the static policy documents, keyed by their path segment.
//
// Prose in Go rather than in a database: unlike the FAQ, these change when a
// LAWYER changes them, not when support notices a question recurring. A deploy
// is the right amount of ceremony for that, and it puts the text under review
// alongside the code that has to honour it.
//
// Every clause here describes what the code actually does, or what the law
// requires of it regardless. Where a COMMERCIAL decision has not been made — the
// warranty term, a goodwill return beyond the statutory window — the document
// says so rather than inventing a number the shop would then be held to.
//
// That distinction is load-bearing in BOTH directions, and only one of them had
// a guard. TestUndecidedTermsAreMarkedPending catches a gap set in the same
// typeface as a rule. The MIRROR of it shipped here: 鑑賞期天數, who pays return
// postage and whether opening the box matters were all filed under 尚未確定 —
// and all three are fixed by 消保法 §19, which §19 V makes unwaivable. They were
// never the owner's to set. A right dressed as a gap reads to the customer as
// "you may not have one", which is the more expensive of the two mistakes, and
// no test in this repository could see it. TestStatutoryTermsAreNotPending is
// the other direction.
var policies = map[string]pages.PolicyDoc{
	"returns": {
		Title:     "退換貨政策",
		TitleEn:   "Returns and exchanges",
		Summary:   "goen 的退貨條件、流程與退款方式。",
		SummaryEn: "What can be returned, how to ask, and how you get your money back.",
		Sections: []pages.PolicySection{
			{
				Heading:   "可以退什麼",
				HeadingEn: "What can be returned",
				Body: []string{
					"只有「已出貨」的商品可以申請退貨,而且數量以實際出貨的數量為上限。這不是政策上的選擇,是系統本身的規則:尚未離開倉庫的商品沒有東西可以退。",
					"還沒出貨的訂單請直接聯絡我們取消,不需要走退貨流程。",
				},
				BodyEn: []string{
					"Only goods that have SHIPPED can be returned, and never more than actually left the warehouse. That is not a policy choice: there is nothing to send back from a parcel that has not gone out.",
					"For an order that has not shipped, contact us to cancel it — there is no return to file.",
				},
			},
			{
				Heading:   "怎麼申請",
				HeadingEn: "How to ask",
				Body: []string{
					"在訂單頁點「申請退貨」,選擇要退回的商品與數量,填寫原因後送出。同一筆訂單一次只能有一件處理中的申請。",
					"我們收到申請後會審核並回覆結果,同意或不同意都會說明原因。",
				},
				BodyEn: []string{
					"On your order page choose \u0022Request a return\u0022, pick the items and quantities, and send it. A reason is optional. One order can have one open request at a time.",
					"We review it and reply either way, with the reason for the decision.",
				},
			},
			{
				Heading:   "退款",
				HeadingEn: "Refunds",
				Body: []string{
					"退貨經同意後,系統會立即向 Stripe 發出退款,金額依訂單本身的單價計算。實際入帳時間由發卡銀行決定,通常是數個工作天。",
					"退款一定會退回原付款方式,不會改用其他管道。",
				},
				BodyEn: []string{
					"Once a return is approved we ask Stripe to refund immediately. The amount comes from the order's own prices, less the share of any discount those goods carried. When it lands is your card issuer's decision, usually a few working days.",
					"A refund always goes back to the way you paid. We will not substitute another channel.",
				},
			},
			// 消保法 §19 I fixes seven days from RECEIPT, with no reason and no
			// cost to the consumer; §19 V voids any agreement to the contrary, so
			// none of the three clauses below is goen's to write differently.
			// 民法 §120 II is why day one is the day AFTER delivery, and §19 IV is
			// why posting on the last day is in time whenever it arrives.
			{
				Heading:   "鑑賞期",
				HeadingEn: "Your seven-day right to cancel",
				Body: []string{
					"您有七天的鑑賞期。這七天從收到商品的「隔天」開始算,期間內要解除契約不需要說明理由,也不需要負擔任何費用。",
					"只要在期限內把商品交寄出去、或把書面通知發出,契約就算解除 —— 我們哪一天收到不影響這件事。",
					"鑑賞期是讓您檢查商品的期間,和在店裡把商品拿起來看是同一回事。因為檢查的必要而造成的毀損或變更,不會讓這個權利消失。",
				},
				BodyEn: []string{
					"You have seven days to cancel. They start the day AFTER the goods reach you, you need give no reason, and it costs you nothing. This is Article 19 of Taiwan's Consumer Protection Act, and no agreement can shorten or waive it.",
					"Sending the goods back, or sending us written notice, inside those seven days is enough — the contract is cancelled at that moment, whatever day it reaches us.",
					"The seven days are for INSPECTING what you bought, exactly as you would pick it up in a shop. Damage or change caused by that inspection does not cost you the right.",
				},
			},
			// The only statutory hook for this is §19 I's 「不負擔任何費用」 — a
			// construction of those four characters, and the reading a consumer
			// authority takes. NOT §19-2, which is about the trader's duty to
			// collect and contains no cost-allocation sentence at all; and NOT
			// 消保法字第0960012078號函, which reasons from 施行細則 §19 and §20,
			// both deleted in the 104/12/31 amendment.
			{
				Heading:   "退貨運費",
				HeadingEn: "Who pays return postage",
				Body: []string{
					"鑑賞期內解除契約,您不需要負擔任何費用,退貨運費由 goen 負擔。",
				},
				BodyEn: []string{
					"We do. Cancelling inside the seven days costs you nothing at all, return postage included, and the delivery fee you originally paid comes back with the goods.",
				},
			},
			// 通訊交易解除權合理例外情事適用準則 §2 is a CLOSED list of seven, and
			// opened 3C hardware is on none of them. Its chapeau also conditions
			// every one of the seven on 「並經企業經營者告知消費者」 — an exception
			// is not self-executing, so a shop that sells boxed software and says
			// nothing owes the full seven days anyway.
			//
			// goen therefore claims no exception, and the copy says so rather than
			// promising a per-product marking that nothing in the code renders. If
			// the catalogue ever carries software or a hygiene item, this clause
			// changes together with the disclosure that makes it true — 消保法
			// §18 I 4 requires that separately.
			{
				Heading:   "拆封之後還能退嗎",
				HeadingEn: "Can I still return it once it is opened?",
				Body: []string{
					"可以。手機、耳機、傳輸線這類 3C 硬體拆封後仍在鑑賞期內 —— 鑑賞期本來就包含拆開來檢查。",
					"法律允許少數幾類商品排除鑑賞期,而且必須在購買前就明確告知才算數。goen 目前沒有任何商品排除鑑賞期,所以本店所有商品都適用完整的七天。",
				},
				BodyEn: []string{
					"Yes. A phone, a pair of headphones, a cable — opening 3C hardware keeps you inside the seven days, because inspecting it is what they are for.",
					"The law allows a few narrow categories to be excluded, and only where the seller says so plainly BEFORE you buy. goen excludes nothing, so every product here carries the full seven days.",
				},
			},
			{
				Heading:   "尚未確定的條款",
				HeadingEn: "Not yet decided",
				Pending:   true,
				Body: []string{
					"七天鑑賞期之外是否另外受理退貨(例如商品沒有問題,但您在第十天改變主意),這一項尚未確定。正式營運前會在本頁公告,在那之前請以聯絡我們取得的說明為準。",
				},
				BodyEn: []string{
					"Whether we accept returns BEYOND the statutory seven days — nothing wrong with the goods, but you changed your mind on day ten — is not decided. It will be published here before we trade; until then, ask us.",
				},
			},
		},
	},
	"payment": {
		Title:     "付款說明",
		TitleEn:   "Paying for your order",
		Summary:   "goen 接受的付款方式與安全性說明。",
		SummaryEn: "What we accept, and what happens to your card details.",
		Sections: []pages.PolicySection{
			{
				Heading:   "付款方式",
				HeadingEn: "How you can pay",
				Body: []string{
					"目前接受信用卡付款,由 Stripe 處理。付款頁面在 Stripe 的網域上,goen 的伺服器不會接觸、也不會儲存您的卡片資料。",
					"我們只會保留卡別與末四碼,用於在訂單頁辨識是哪一張卡付的款。",
				},
				BodyEn: []string{
					"Cards, handled by Stripe. The payment form is on Stripe's own domain — goen's servers never see your card details and never store them.",
					"We keep the card brand and the last four digits, so your order page can tell you which card paid.",
				},
			},
			{
				Heading:   "什麼時候扣款",
				HeadingEn: "When you are charged",
				Body: []string{
					"在 Stripe 頁面完成付款時就會扣款。goen 只在收到 Stripe 經過簽章驗證的通知後,才把訂單標記為已付款 —— 回到網站看到的頁面本身不代表付款成功。",
				},
				BodyEn: []string{
					"At the moment you finish on Stripe's page. goen marks an order paid only on a signature-verified notice from Stripe — the page you land back on is not itself proof that the money arrived.",
				},
			},
			{
				Heading:   "庫存保留",
				HeadingEn: "We hold the stock while you pay",
				Body: []string{
					// The number is INTERPOLATED, not typed. It used to be the
					// literal 30 here and `%s` fed by pages.HoldMinutesText() on
					// /shipping — one figure stated twice, with a test binding only
					// one of them, so this copy could say something the till did not
					// do and nothing would notice. It said exactly that the moment
					// cart.HoldTTL moved.
					fmt.Sprintf("送出訂單時系統會保留庫存 %s 分鐘。超過時間未完成付款,商品會回到架上,"+
						"但訂單仍然存在,可以重新付款(若庫存還在)。", pages.HoldMinutesText()),
				},
				BodyEn: []string{
					fmt.Sprintf("Placing an order reserves the stock for %s minutes. If the payment does not "+
						"arrive in that time the goods go back on the shelf, but the order itself stays — you "+
						"can pay again if it is still in stock.", pages.HoldMinutesText()),
				},
			},
		},
	},
	"warranty": {
		Title:     "保固說明",
		TitleEn:   "Warranty",
		Summary:   "goen 販售商品的保固方式。",
		SummaryEn: "How the goods we sell are covered.",
		Sections: []pages.PolicySection{
			{
				Heading:   "保固範圍",
				HeadingEn: "What is covered",
				Body: []string{
					"goen 販售的商品由原廠提供保固。各商品的保固內容寫在該商品頁面上,以商品頁的說明為準。",
				},
				BodyEn: []string{
					"What we sell is covered by the manufacturer. Each product page states its own term, and that page is what governs.",
				},
			},
			{
				Heading:   "尚未確定的條款",
				HeadingEn: "Not yet decided",
				Pending:   true,
				Body: []string{
					"保固期限、送修流程,以及維修期間是否提供替代機,這些尚未確定。正式營運前會在本頁公告。",
				},
				BodyEn: []string{
					"How repairs are sent in, and whether we lend you something while yours is away, is not decided. It will be published here before we trade.",
				},
			},
		},
	},
	"privacy": {
		Title:     "隱私權政策",
		TitleEn:   "Privacy",
		Summary:   "goen 蒐集哪些資料、為什麼蒐集,以及您可以怎麼處理它。",
		SummaryEn: "What we collect, why, and what you can do about it.",
		Sections: []pages.PolicySection{
			{
				Heading:   "我們蒐集什麼",
				HeadingEn: "What we collect",
				Body: []string{
					"下單時:收件人姓名、電話、地址與 Email,用於出貨與聯絡。",
					"註冊時:Email 與密碼。密碼以 argon2id 雜湊儲存,任何人都無法從資料庫還原它,包含我們。",
					"付款時:卡片資料由 Stripe 處理,不經過 goen。我們只收到卡別與末四碼。",
				},
				BodyEn: []string{
					"When you order: the recipient's name, phone, address and email — to ship to you and to reach you.",
					"When you register: your email and a password. The password is stored as an argon2id hash, which nobody can reverse out of the database, us included.",
					"When you pay: your card details go to Stripe and never through goen. We receive the card brand and the last four digits.",
				},
			},
			{
				Heading:   "我們不做什麼",
				HeadingEn: "What we do not do",
				Body: []string{
					"不將您的個人資料出售或提供給第三方作行銷用途。",
					// The list is enumerated because the sentence claims completeness
					// — 「只有…這幾種」 is falsifiable, and it was false: it named
					// three while the site set five. The language cookie is written by
					// the switch in the footer of every page, so the shortfall was
					// reachable by any visitor who changed language.
					// TestThePrivacyPolicyNamesEveryCookie is what keeps them equal.
					"不在網站上使用第三方追蹤或廣告 cookie。goen 使用的 cookie 只有這幾種:購物車、登入狀態、訂單瀏覽權限、您選擇的語言,以及您關閉過的網站公告。",
				},
				BodyEn: []string{
					"We do not sell your personal data, or hand it to anybody else for marketing.",
					"There is no third-party tracking or advertising cookie on this site. goen sets these kinds of cookie and no others: your cart, your sign-in, permission to view an order, the language you chose, and which site notice you have dismissed.",
				},
			},
			{
				Heading:   "刪除您的資料",
				HeadingEn: "Deleting your data",
				Body: []string{
					"在會員中心可以要求刪除帳號。系統會清除您的姓名、Email、電話、地址與訂單上的收件資訊。",
					"訂單本身的財務紀錄會保留,不含個人識別資訊 —— 這是會計與稅務要求,不是我們的選擇。已公開的商品評價也會保留,但不再與您的帳號關聯。",
				},
				BodyEn: []string{
					"You can ask for your account to be deleted from your account pages. That erases your name, email, phone, address and the delivery details on your orders.",
					"The orders' financial records stay, stripped of anything identifying you — that is an accounting and tax requirement, not our choice. Reviews you published stay too, no longer linked to your account.",
				},
			},
		},
	},
	"terms": {
		Title:     "服務條款",
		TitleEn:   "Terms of service",
		Summary:   "使用 goen 的基本約定。",
		SummaryEn: "The basic agreement for using goen.",
		Sections: []pages.PolicySection{
			{
				Heading:   "訂單成立",
				HeadingEn: "When an order is formed",
				Body: []string{
					"送出訂單即表示要約,我們確認庫存與付款後訂單成立。若商品在您付款前售罄,我們會取消訂單並全額退款。",
				},
				BodyEn: []string{
					"Placing an order is an offer; the contract forms when we have confirmed the stock and the payment. If something sells out before you pay, we cancel the order and refund it in full.",
				},
			},
			{
				Heading:   "價格與標示",
				HeadingEn: "Prices",
				Body: []string{
					"網站上顯示的價格為新台幣含稅價。若因系統錯誤導致標價明顯有誤,我們保留取消該筆訂單並退款的權利,並會主動聯絡您說明。",
				},
				BodyEn: []string{
					"Prices are in New Taiwan dollars and include tax. Where a system fault makes a price obviously wrong, we reserve the right to cancel that order and refund it, and we will contact you to explain rather than leave you to notice.",
				},
			},
			{
				Heading:   "帳號",
				HeadingEn: "Your account",
				Body: []string{
					"請妥善保管您的密碼。變更密碼會同時登出其他所有裝置。",
				},
				BodyEn: []string{
					"Keep your password to yourself. Changing it signs out every other device at the same time.",
				},
			},
			{
				Heading:   "尚未確定的條款",
				HeadingEn: "Not yet decided",
				Pending:   true,
				Body: []string{
					"準據法、爭議解決方式與管轄法院尚未確定,正式營運前會在本頁補上。",
				},
				BodyEn: []string{
					"Governing law, how disputes are handled and which court hears them are not decided. They will be added here before we trade.",
				},
			},
		},
	},
}
