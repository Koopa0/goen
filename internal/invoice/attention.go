package invoice

// The attention categories an invoice operation can enter. The issuer writes
// them to the operation's attention column; the back-office views read them.
const (
	// CategoryBuyerUnconfirmed is the attention an allowance enters once its
	// consent link lapsed with no agreement.
	CategoryBuyerUnconfirmed = "allowance_buyer_unconfirmed"

	// CategoryAmountStillHeld is the attention an allowance enters when ECPay
	// answers that an earlier request still holds the invoice's remaining
	// amount.
	CategoryAmountStillHeld = "allowance_amount_still_held"

	// CategoryMultipleCandidates is the attention an allowance enters when ECPay
	// lists more than one allowance that could be the one goen sent.
	CategoryMultipleCandidates = "allowance_multiple_unknown_candidates"

	// CategorySuccessMismatch is the attention an allowance enters when ECPay's
	// success reply names another document.
	CategorySuccessMismatch = "allowance_success_mismatch"
)
