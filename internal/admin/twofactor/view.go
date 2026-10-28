package twofactor

import "github.com/koopa0/goen/internal/i18n"

type View struct {
	Enabled   bool
	Enrolled  bool
	Enrolling bool
	// Secret, URI and QRCode exist in exactly one response and are never re-rendered.
	Secret string
	URI    string
	QRCode string
	Notice string
	// Recovery is a notice that ends in a link to the staff page, so it cannot be one string.
	Recovery Recovery
}

// Recovery names the two situations in which only another administrator can
// clear the factor, at the staff page.
type Recovery int

const (
	RecoveryNone Recovery = iota
	RecoveryEnrolled
	RecoveryUnreadable
)

// Before and After are the words either side of the link to the staff page.
func (r Recovery) Before() i18n.Key {
	if r == RecoveryUnreadable {
		return i18n.KeyTOTPSecretUnreadableBefore
	}
	return i18n.KeyTOTPAlreadyEnrolledBefore
}

func (r Recovery) After() i18n.Key {
	if r == RecoveryUnreadable {
		return i18n.KeyTOTPSecretUnreadableAfter
	}
	return i18n.KeyTOTPAlreadyEnrolledAfter
}

func (v View) NeedsEnrolment() bool { return v.Enabled && !v.Enrolled && !v.Enrolling }

func (v View) CanVerify() bool { return v.Enabled && v.Enrolled && !v.Enrolling }

func (v View) SecretGroups() []string {
	const group = 4
	out := make([]string, 0, len(v.Secret)/group+1)
	for i := 0; i < len(v.Secret); i += group {
		end := min(i+group, len(v.Secret))
		out = append(out, v.Secret[i:end])
	}
	return out
}
