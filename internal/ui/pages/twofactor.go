package pages

// TwoFactorView is the second-factor page.
type TwoFactorView struct {
	Enabled   bool
	Enrolled  bool
	Enrolling bool
	// Secret and URI exist in exactly one response and are never re-rendered.
	Secret string
	URI    string
	Notice string
}

// NeedsEnrolment reports whether the person must set 2FA up before verifying.
func (v TwoFactorView) NeedsEnrolment() bool { return v.Enabled && !v.Enrolled && !v.Enrolling }

// CanVerify reports whether a code can be submitted.
func (v TwoFactorView) CanVerify() bool { return v.Enabled && v.Enrolled && !v.Enrolling }

// SecretGroups breaks the secret into four-character blocks.
func (v TwoFactorView) SecretGroups() []string {
	const group = 4
	out := make([]string, 0, len(v.Secret)/group+1)
	for i := 0; i < len(v.Secret); i += group {
		end := min(i+group, len(v.Secret))
		out = append(out, v.Secret[i:end])
	}
	return out
}
