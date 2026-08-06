package pages

import "strconv"

// AdminQuestion is one customer question as the back office sees it.
type AdminQuestion struct {
	ID          string
	Body        string
	Asker       string
	ProductSlug string
	ProductName string
	Asked       string
	Answers     int64
	// AnsweredByShop is the only thing that decides urgency. Three customer
	// replies and no official one is still an unanswered question — the person
	// deciding came for the shop's answer.
	AnsweredByShop bool
}

// Waiting reports whether the shop still owes an answer.
func (q AdminQuestion) Waiting() bool { return !q.AnsweredByShop }

// State is the word a staff member scans for.
func (q AdminQuestion) State() string {
	switch {
	case q.AnsweredByShop:
		return "已回覆"
	case q.Answers > 0:
		return "只有顧客回覆"
	default:
		return "待回覆"
	}
}

// AnswersText is how many replies it has.
func (q AdminQuestion) AnswersText() string { return strconv.FormatInt(q.Answers, 10) }

// Who is the person who asked.
func (q AdminQuestion) Who() string {
	if q.Asker == "" {
		return "已刪除的帳號"
	}
	return q.Asker
}

// ProductHref is the product's page on the storefront, so a staff member can
// see what the customer was looking at before answering.
func (q AdminQuestion) ProductHref() string { return "/p/" + q.ProductSlug }

// AnswerAction is where the reply form posts.
func (q AdminQuestion) AnswerAction() string { return "/admin/questions/" + q.ID }

// AdminQuestionsView is the queue.
type AdminQuestionsView struct {
	Rows   []AdminQuestion
	Notice string
}

// Empty reports whether nobody has asked anything.
func (v AdminQuestionsView) Empty() bool { return len(v.Rows) == 0 }

// Waiting is how many still need the shop.
func (v AdminQuestionsView) Waiting() int {
	n := 0
	for _, q := range v.Rows {
		if q.Waiting() {
			n++
		}
	}
	return n
}

// WaitingText is that count.
func (v AdminQuestionsView) WaitingText() string { return strconv.Itoa(v.Waiting()) }
