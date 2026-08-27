package i18n

var (
	KeyLoyaltyPoints = key("account.points", Message{ZhHant: "會員點數", En: "Points"})

	KeyLoyaltyPointsHint = key("account.points.hint", Message{
		ZhHant: "每消費 NT$100 得 1 點,可換購物金",
		En:     "One point per NT$100 spent, redeemable for store credit",
	})

	KeyPointsTitle = key("points.title", Message{ZhHant: "會員點數", En: "Points"})

	KeyPointsSub = key("points.sub", Message{
		ZhHant: "每消費 NT$100 得 1 點,%s,可以兌換成商店額度在結帳時折抵。",
		En:     "One point per NT$100 spent. %s, redeemable as store credit at checkout.",
	})

	KeyPointsBalance = key("points.balance", Message{ZhHant: "目前點數", En: "Balance"})

	KeyPointsRedeemable = key("points.redeemable", Message{ZhHant: "可兌換", En: "Redeemable"})

	KeyPointsUsing = key("points.using", Message{ZhHant: "用 %s 點", En: "Using %s points"})

	KeyPointsHowMany = key("points.howmany", Message{
		ZhHant: "要兌換幾點?",
		En:     "How many points?",
	})

	KeyPointsRule = key("points.rule", Message{
		ZhHant: "最少 %s 點,而且要是 %s 的整數倍 —— 換不完的點數會留著。",
		En: "At least %s points, in whole multiples of %s. Whatever is left over stays " +
			"on your account.",
	})

	KeyPointsRedeem = key("points.redeem", Message{
		ZhHant: "兌換成商店額度",
		En:     "Redeem for store credit",
	})

	KeyPointsLedger = key("points.ledger", Message{ZhHant: "紀錄", En: "History"})

	KeyPointsEmpty = key("points.empty", Message{
		ZhHant: "還沒有任何點數紀錄。完成一筆訂單就會開始累積。",
		En:     "No points yet. They start accumulating with your first completed order.",
	})

	KeyPointsRate = key("points.rate", Message{ZhHant: "%s 點 = NT$1", En: "%s points = NT$1"})

	KeyPointsExpiring = key("points.expiring", Message{
		ZhHant: "%s 點會在 %s 到期",
		En:     "%s points expire on %s",
	})

	KeyPointsExpired = key("points.expired", Message{ZhHant: "已於 %s 到期", En: "expired %s"})

	KeyPointsExpiresOn = key("points.expireson", Message{ZhHant: "%s 到期", En: "expires %s"})

	KeyPointsFromOrder = key("points.reason.order", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyPointsEarned = key("points.reason.earned", Message{ZhHant: "購物回饋", En: "Earned on a purchase"})

	KeyPointsSpent = key("points.reason.redeem", Message{
		ZhHant: "兌換商店額度",
		En:     "Redeemed for store credit",
	})

	KeyPointsClawback = key("points.reason.clawback", Message{
		ZhHant: "退貨扣回",
		En:     "Reversed for a return",
	})

	KeyPointsClawbackDetail = key("points.reason.clawback.detail", Message{
		ZhHant: "應扣回 %s 點；實際扣回 %s 點；未扣回 %s 點",
		En:     "Requested %s points; reversed %s; shortfall %s",
	})

	KeyPointsRedeemed = key("points.notice.done", Message{
		ZhHant: "已經兌換成商店額度,結帳時會自動折抵。",
		En:     "Redeemed. The credit comes off your next order automatically.",
	})

	KeyPointsBadAmount = key("points.notice.amount", Message{
		ZhHant: "兌換的點數要是整數倍,而且不能低於最低門檻。",
		En:     "Redeem a whole multiple, and not less than the minimum.",
	})

	KeyPointsShort = key("points.notice.short", Message{
		ZhHant: "點數不夠 —— 可能剛剛有一筆到期了。",
		En:     "Not enough points — some may have just expired.",
	})
)
