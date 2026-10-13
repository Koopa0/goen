package site

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func policyText(locale i18n.Locale, key i18n.Key) string {
	return i18n.T(i18n.WithLocale(context.Background(), locale), key)
}

// policies is the static policy documents, keyed by their path segment. A
// section marked Pending renders as a visible gap, and a term the law already
// fixes is never the shop's to leave Pending.
var policies = map[string]pages.PolicyDoc{
	"returns": {
		Title:     "退貨政策",
		TitleEn:   "Returns",
		Summary:   "goen 的退貨條件、流程與退款方式。",
		SummaryEn: "What can be returned, how to ask, and how you get your money back.",
		Sections: []pages.PolicySection{
			{
				Heading:   "可以退什麼",
				HeadingEn: "What can be returned",
				Body: []string{
					"只有「已出貨」的商品可以申請退貨，而且數量以實際出貨的數量為上限。",
					"還沒出貨的訂單請直接聯絡我們取消，不需要走退貨流程。",
				},
				BodyEn: []string{
					"Only goods that have shipped can be returned, and never more than actually left the warehouse.",
					"For an order that has not shipped, contact us to cancel it — there is no return to file.",
				},
			},
			{
				Heading:   "怎麼申請",
				HeadingEn: "How to ask",
				Body: []string{
					// Consumer Protection Act §19 I needs no reason; the form
					// and return_requests_reason_bounded allow a blank. Chinese
					// that treats filling one as a step before submit tells
					// that reader a blank is refused.
					"在訂單頁點「申請退貨」，選擇要退回的商品與數量後送出。原因選填。同一筆訂單一次只能有一件處理中的申請。",
					// returns.Evaluate refuses a decline on any line filed
					// inside the statutory window, and a decline cannot be
					// saved without the reason the customer then sees.
					"在猶豫期內提出的申請，我們不會拒絕。超過猶豫期的申請，我們會依「七日之外」的條件確認後回覆；不同意時一定會說明原因。",
				},
				BodyEn: []string{
					"On your order page choose \u0022Request a return\u0022, pick the items and quantities, and send it. A reason is optional. One order can have one open request at a time.",
					"A request made within your seven-day right to cancel is never declined. After the seven days, we check it against the terms under \u0022Beyond the seven days\u0022 and reply; if we decline it, we always tell you why.",
				},
			},
			{
				Heading:   "退款",
				HeadingEn: "Refunds",
				// compensate_return_with_credit pays the store-credit half of a
				// return; naming only Stripe here would describe a different shop.
				Body: []string{
					"我們確認退貨後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，購物金退回餘額。金額依訂單本身的單價計算，並扣除這些商品分攤的折扣。卡款入帳時間由發卡銀行決定，通常是數個工作天；購物金退回後可立刻再用於結帳。",
					"退款依原路退回，不會改用其他管道。",
				},
				BodyEn: []string{
					"Once we confirm the return, we pay it back the way you paid: the card share is refunded through Stripe immediately, and store credit returns to your balance. The amount comes from the order's own prices, less the share of any discount those goods carried. When a card refund lands is your card issuer's decision, usually a few working days; credit is available again at once.",
					"A refund always follows the original payment: card through Stripe, store credit to your balance. We will not substitute another channel.",
				},
			},
			// Consumer Protection Act §19 I: seven days from receipt, no reason,
			// no cost; §19 V voids any agreement otherwise. Civil Code §120 II
			// excludes the day of receipt, and §19 IV fixes it on dispatch.
			{
				Heading:   "猶豫期",
				HeadingEn: "Your seven-day right to cancel",
				Body: []string{
					"你可以自收到商品的次日起七日內解除契約，不需要說明理由，也不需要負擔任何費用。這是消費者保護法第 19 條的規定，任何約定都不能縮短或排除。",
					"只要在期限內把商品交寄出去、或把書面通知發出，契約就算解除，我們哪一天收到都不影響。",
					"猶豫期是讓你檢查商品的期間，和在店裡把商品拿起來看是同一回事。因為檢查的必要而造成的毀損或變更，不會讓這個權利消失。",
				},
				BodyEn: []string{
					"You have seven days to cancel, counted from the day after you receive the goods. You need give no reason, and it costs you nothing. This is Article 19 of Taiwan's Consumer Protection Act, and no agreement can shorten or waive it.",
					"Sending the goods back, or sending us written notice, inside those seven days is enough — the contract is cancelled at that moment, whatever day it reaches us.",
					"The seven days are for inspecting what you bought, exactly as you would pick it up in a shop. Damage or change caused by that inspection does not cost you the right.",
				},
			},
			// The hook is Consumer Protection Act §19 I ("bears no cost"), not
			// §19-2, which allocates no costs at all.
			{
				Heading:   "退貨運費",
				HeadingEn: "Who pays return postage",
				Body: []string{
					"猶豫期內解除契約，你不需要負擔任何費用，退貨運費由 goen 負擔。整筆訂單都退回時，原本支付的運費也會退還給你。",
				},
				BodyEn: []string{
					"We do. Cancelling inside the seven days costs you nothing at all, return postage included. The delivery fee you originally paid is refunded once you have returned the whole order.",
				},
			},
			// The Regulations on Reasonable Exceptions to Rescission in Distance
			// Sales §2 are a closed list of seven, each conditioned on the trader
			// having disclosed it before the sale. goen claims none.
			{
				Heading:   "拆封之後還能退嗎",
				HeadingEn: "Can I still return it once it is opened?",
				Body: []string{
					"可以。無論是哪一項商品，拆開包裹檢查商品都不會讓七日的解除權結束，因為猶豫期本來就包含拆開來檢查。",
					"法律允許少數幾類商品排除猶豫期，而且必須在購買前就明確告知才算數。goen 目前沒有任何商品排除猶豫期，所以本店所有商品都適用完整的七日。",
				},
				BodyEn: []string{
					"Yes. For every product, opening the parcel to inspect the goods does not end your seven days, because inspecting them is what the seven days are for.",
					"The law allows a few narrow categories to be excluded, and only where the seller says so plainly before you buy. goen excludes nothing, so every product here carries the full seven days.",
				},
			},
			{
				Heading:   "七日之外",
				HeadingEn: "Beyond the seven days",
				Body: []string{
					"猶豫期之外，商品未使用、包裝與配件齊全的話，我們願意在送達後 14 天內受理退貨，運費由你負擔。",
					"這是 goen 自己的額外服務，不是法律規定的猶豫期。前面七日的權利不受這一條影響，也不會因為這一條變短。",
				},
				BodyEn: []string{
					"After the seven days, we will still take something back within 14 days of delivery if it is unused and complete with its box and accessories. You pay the postage.",
					"This is goen's own offer, not the statutory window. It adds to the seven days above and takes nothing away from them.",
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
				Heading:   "信用卡",
				HeadingEn: "Credit cards",
				Body: []string{
					"目前接受信用卡付款，由 Stripe 處理。付款頁面在 Stripe 的網域上，goen 的伺服器不會接觸、也不會儲存你的卡片資料。",
					"我們只會保留卡別與末四碼，用於在訂單頁辨識是哪一張卡付的款。",
				},
				BodyEn: []string{
					"Cards, handled by Stripe. The payment form is on Stripe's own domain — goen's servers never see your card details and never store them.",
					"We keep the card brand and the last four digits, so your order page can tell you which card paid.",
				},
			},
			{
				Heading:   "購物金",
				HeadingEn: "Store credit",
				Body:      []string{"登入後，帳號內可用的購物金會在結帳時自動折抵，不足的金額再以信用卡付款。"},
				BodyEn:    []string{"When you are signed in, your available store credit comes off the order automatically at checkout; any remaining amount is paid by card."},
			},
			{
				Heading:   "折扣碼",
				HeadingEn: "Discount codes",
				Body:      []string{"折扣碼是價格折抵，不是付款方式。在結帳頁輸入有效的折扣碼，購物金會從折抵後的金額再扣除，剩餘款項以信用卡付款。", "百分比折扣碼是照購物車內商品目前的售價小計計算，已在特價的商品也一併折抵，特價與折扣碼可以疊加。"},
				BodyEn: []string{
					"A discount code reduces the price; it is not a payment method. Enter a valid code at checkout; store credit then comes off the discounted total, and any remaining amount is paid by card.",
					"A percentage code is worked out on the cart subtotal at the items' current prices, so items already on sale are discounted too: a sale and a code stack.",
				},
			},
			{
				Heading:   "什麼時候扣款",
				HeadingEn: "When you are charged",
				Body: []string{
					"在 Stripe 頁面完成付款時就會扣款。訂單要等 Stripe 確認收款後才會顯示為已付款；付款後回到網站看到的頁面，還不代表付款成功。",
				},
				BodyEn: []string{
					"At the moment you finish on Stripe's page. Your order shows as paid only once Stripe confirms the payment — the page you land back on is not itself proof that the money arrived.",
				},
			},
			{
				Heading:   "庫存保留",
				HeadingEn: "We hold the stock while you pay",
				Body: []string{
					// Interpolated, never typed: a literal here is a second copy
					// of an enforced duration that no test binds.
					fmt.Sprintf(policyText(i18n.ZhHant, i18n.KeyPolicyPaymentHold),
						pages.HoldMinutesText(), pages.PayStartMinutesText()),
				},
				BodyEn: []string{
					fmt.Sprintf(policyText(i18n.En, i18n.KeyPolicyPaymentHold),
						pages.HoldMinutesText(), pages.PayStartMinutesText()),
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
					"商品頁面標示了保固的商品，由原廠依標示的期限提供保固；沒有標示保固的商品，除法律另有規定外，goen 不另外提供保固。各商品的保固內容寫在該商品頁面上，以商品頁的說明為準。",
				},
				BodyEn: []string{
					"A product whose page states a warranty is covered by the manufacturer's warranty for the term stated there. A product that states none carries no warranty from goen beyond what the law gives you. Each product page states its own term, and that page is what governs.",
				},
			},
			{
				Heading:   "保固期限",
				HeadingEn: "How long you are covered",
				Body: []string{
					"保固期限依商品而不同，長度寫在該商品的頁面上。期限從商品送達當日起算。",
					"沒有標示保固期限的商品，表示原廠沒有提供保固，這類商品無法登錄。",
				},
				BodyEn: []string{
					"The term depends on the product, and its length is stated on that product's own page. It runs from the day the goods reach you.",
					"A product with no term stated carries no manufacturer's warranty, and cannot be registered.",
				},
			},
			{
				Heading:   "怎麼送修",
				HeadingEn: "Sending something in",
				Body: []string{
					policyText(i18n.ZhHant, i18n.KeyPolicyWarrantyRegistration),
					"需要送修時請聯絡客服。宅配訂單由我們安排到府收件，超商取貨的訂單請由超商寄回；兩種訂單的收送費用都由 goen 負擔。",
					"維修期間不提供替代機。",
				},
				BodyEn: []string{
					policyText(i18n.En, i18n.KeyPolicyWarrantyRegistration),
					"When you need a repair, contact us. For a home-delivery order we arrange collection from your door, and a convenience-store pickup order is sent back from a convenience store; we pay the carriage both ways in either case.",
					"We do not lend a replacement while yours is away.",
				},
			},
		},
	},
	"privacy": {
		Title:     "隱私權政策",
		TitleEn:   "Privacy",
		Summary:   "goen 蒐集哪些資料、為什麼蒐集，以及你可以怎麼處理它。",
		SummaryEn: "What we collect, why, and what you can do about it.",
		Sections: []pages.PolicySection{
			{
				Heading:   "我們蒐集什麼",
				HeadingEn: "What we collect",
				Body: []string{
					"下單時：收件人姓名、電話、地址與電子郵件，用於出貨與聯絡。",
					"註冊時：電子郵件與密碼。密碼以無法還原的方式保存，任何人都無法從資料庫取回它，包含我們。",
					"付款時：卡片資料由 Stripe 處理，不經過 goen。我們只收到卡別與末四碼。",
					"訂閱電子報時：保存你的電子郵件、語言、確認與退訂狀態，用於寄送及停止電子報。",
					"登入時：工作階段保存 IP 位址，以及瀏覽器送出的瀏覽器與裝置資訊，登入狀態結束或帳號刪除後一併移除。",
					"開立發票時：保存顧客姓名、電子郵件，以及你選擇提供的公司統一編號、手機條碼或捐贈碼（愛心碼），用於開立發票與後續折讓。",
					"登錄保固時：保存商品序號與保固登錄資料，用於識別送修商品及保固期限。",
				},
				BodyEn: []string{
					"When you order: the recipient's name, phone, address and email — to ship to you and to reach you.",
					"When you register: your email and a password. The password is stored in a form that cannot be reversed, so nobody can recover it from the database, us included.",
					"When you pay: your card details go to Stripe and never through goen. We receive the card brand and the last four digits.",
					"When you subscribe to the newsletter: we keep your email, language, confirmation and unsubscribe status to send or stop the newsletter.",
					"When you sign in: the session stores your IP address and the browser and device details your browser sends, and is removed when the session expires or the account is deleted.",
					"When we issue an invoice: we keep your customer name and email, and the company tax ID, mobile barcode or donation code you choose to provide, for invoicing and subsequent allowances.",
					"When you register a warranty: we keep the product serial number and warranty registration to identify the unit and its coverage period.",
				},
			},
			{
				Heading:   "第三方處理",
				HeadingEn: "Third-party processing",
				Body: []string{
					"付款由 Stripe 處理；開立發票與折讓所需的資料會提供給綠界電子發票平台。",
					"選擇用 Google 登入時，由 Google 確認你的身分；我們會從 Google 取得你的 Google 帳號識別碼、電子郵件、電子郵件是否已驗證與姓名，用來建立或登入你的 goen 帳號。",
					"網站字型由 goen 自己提供，載入字型不會連到 Google 的伺服器。",
				},
				BodyEn: []string{
					"Stripe processes payments; information needed for invoices and allowances is sent to ECPay's e-invoice platform.",
					"If you sign in with Google, Google confirms who you are; we receive your Google account identifier, email address, whether that email is verified, and your name, and use them to create or sign in to your goen account.",
					"goen serves the website fonts itself; loading them sends nothing to Google's servers.",
				},
			},
			{
				Heading:   "我們不做什麼",
				HeadingEn: "What we do not do",
				Body: []string{
					"不將你的個人資料出售或提供給第三方作行銷用途。",
					// The cookie list claims completeness, in both locales.
					"不在網站上使用第三方追蹤或廣告 cookie。goen 使用的 cookie 只有這幾種：購物車、登入狀態、訂單瀏覽權限、你選擇的語言、你關閉過的網站公告、挑選超商取貨門市時暫存的選擇、重設密碼後或刪除帳號前重新登入時用於預填電子郵件地址的資料（最多保留兩分鐘，開啟登入頁後即清除），以及用 Google 登入時暫存幾分鐘的驗證資料。",
				},
				BodyEn: []string{
					"We do not sell your personal data, or hand it to anybody else for marketing.",
					"There is no third-party tracking or advertising cookie on this site. goen sets these kinds of cookie and no others: your cart, your sign-in, permission to view an order, the language you chose, which site notice you have dismissed, what you chose while picking a convenience store to collect from, the email address used to prefill sign-in after a password reset or before account-deletion reauthentication (kept for up to two minutes and cleared when you open the sign-in page), and — for a few minutes while you sign in with Google — what that sign-in belongs to.",
				},
			},
			{
				Heading:   "刪除你的資料",
				HeadingEn: "Deleting your data",
				Body: []string{
					"在會員中心可以要求刪除帳號。系統會清除帳號中的姓名、電子郵件、電話、地址與訂單上的收件資訊；下列保留資料不在清除範圍內。",
					"訂單財務紀錄及不可變更的發票快照會保留，包括顧客姓名、電子郵件、公司統一編號、手機條碼與捐贈碼。尚待處理或確認結果的發票作業也會保留所需資料，直到完成確認。",
					"保固登錄與商品序號會保留，但不再連結到已刪除的帳號。已公開的商品評價也會保留，但不再與你的帳號關聯。",
					"只有已驗證帳號目前電子郵件的所有權，刪帳才會移除同信箱的電子報訂閱。未驗證信箱的訂閱不會隨刪帳移除；請使用電子報中的退訂連結停止寄送。",
				},
				BodyEn: []string{
					"You can ask for your account to be deleted from your account pages. That erases the name, email, phone and address in your account and the delivery details on your orders, except for the retained data described below.",
					"Order financial records and immutable invoice snapshots remain, including the customer name, email, company tax ID, mobile barcode and donation code. Invoice operations awaiting processing or reconciliation also keep the data they need until their outcome is settled.",
					"Warranty registrations and product serial numbers remain, no longer linked to the deleted account. Published reviews also remain without their account link.",
					"Deleting an account removes newsletter subscriptions for its current email only if ownership of that email has been verified. Subscriptions for an unverified email are not removed by account deletion; use the unsubscribe link in a newsletter to stop delivery.",
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
					"送出訂單即表示要約，我們確認庫存與付款後訂單成立。若商品在你付款前售罄，我們會取消訂單並全額退款。",
				},
				BodyEn: []string{
					"Placing an order is an offer; the contract forms when we have confirmed the stock and the payment. If something sells out before you pay, we cancel the order and refund it in full.",
				},
			},
			{
				Heading:   "價格與標示",
				HeadingEn: "Prices",
				Body: []string{
					"網站上顯示的價格為新台幣含稅價。若因系統錯誤導致標價明顯有誤，我們保留取消該筆訂單並退款的權利，並會主動聯絡你說明。",
				},
				BodyEn: []string{
					"Prices are in New Taiwan dollars and include tax. Where a system fault makes a price obviously wrong, we reserve the right to cancel that order and refund it, and we will contact you to explain rather than leave you to notice.",
				},
			},
			{
				Heading:   "點數與購物金",
				HeadingEn: "Points and store credit",
				Body: []string{
					"會員點數自取得起一年到期。用點數兌換成的購物金不會到期。",
				},
				BodyEn: []string{
					"Points expire one year after you earn them. Store credit you redeem from points does not expire.",
				},
			},
			{
				Heading:   "帳號",
				HeadingEn: "Your account",
				Body: []string{
					"請妥善保管你的密碼。變更密碼會同時登出其他所有裝置。",
				},
				BodyEn: []string{
					"Keep your password to yourself. Changing it signs out every other device at the same time.",
				},
			},
			// Consumer Protection Act §47 and Code of Civil Procedure §12 already
			// let a consumer sue where they live, so this clause can only add a
			// court, never take one away.
			{
				Heading:   "準據法與管轄",
				HeadingEn: "Governing law",
				Body: []string{
					"本條款以中華民國法律為準據法。",
					"有爭議時請先聯絡我們，大多數問題不需要走到法院。若確實需要訴訟，以臺灣臺北地方法院為第一審管轄法院，但這不影響消費者依消費者保護法向自己住所地法院起訴的權利。",
				},
				BodyEn: []string{
					"These terms are governed by the law of the Republic of China (Taiwan).",
					"If something goes wrong, contact us first — most things do not need a court. If a suit is necessary, the Taiwan Taipei District Court is the court of first instance, and this does not affect a consumer's right to sue where they live instead.",
				},
			},
		},
	},
}
