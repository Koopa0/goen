// Package newsletter records email subscriptions taken from the site footer.
package newsletter

import (
	"fmt"

	"github.com/koopa0/goen/internal/email"
)

// Validate returns the message to show for addr, or "" when it is acceptable.
// A single string rather than a map: the form has one field.
func Validate(addr string) string {
	switch {
	case addr == "":
		return "請填寫 Email"
	case len(addr) > email.Max:
		return fmt.Sprintf("Email 請控制在 %d 個字元以內", email.Max)
	case !email.Valid(addr):
		return "Email 格式看起來不正確"
	}
	return ""
}
