package i18n

// Messages more than one surface needs.
//
// A key lives here when a SECOND caller wants it, never in anticipation of one.
// "Email 格式看起來不正確" was written three times in three packages before this
// file existed, and the three had already drifted apart by a full stop.

var (
	// Field validation. Every one of these is a message beside a control, which
	// is chrome by any reading — and the server's copy of it is the authority,
	// because `required` and `type=email` are the first line and never the only
	// one.
	KeyEmailRequired = key("field.email.required", Message{
		ZhHant: "請填寫 Email",
		En:     "Enter an email address",
	})
	KeyEmailMalformed = key("field.email.malformed", Message{
		ZhHant: "Email 格式看起來不正確",
		En:     "That does not look like an email address",
	})
	// %d is the limit. Bounded in RUNES rather than bytes, so the number means
	// the same thing to a Chinese-speaking customer as to an English-speaking
	// one — a byte limit gives the first a third of the room.
	KeyEmailTooLong = key("field.email.toolong", Message{
		ZhHant: "Email 請控制在 %d 個字元以內",
		En:     "Keep the email address under %d characters",
	})

	// What a browser is told when its form did not parse. Not a page: by the time
	// this fires goen does not know enough to render one.
	KeyFormUnreadable = key("form.unreadable", Message{
		ZhHant: "表單無法解析",
		En:     "That form could not be read",
	})

	// The generic failure. Two strings rather than one, because a heading and the
	// sentence under it are read at different speeds.
	KeyTryAgainTitle = key("error.retry.title", Message{
		ZhHant: "系統暫時無法處理",
		En:     "Something went wrong",
	})
	KeyTryAgainBody = key("error.retry.body", Message{
		ZhHant: "請稍後再試一次。",
		En:     "Please try again shortly.",
	})
)
