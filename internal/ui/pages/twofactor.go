package pages

// TwoFactorView is the second-factor page.
type TwoFactorView struct {
	// Enabled is whether this deployment can store a secret at all.
	Enabled bool
	// Enrolled is whether this person has a confirmed credential.
	Enrolled bool
	// Enrolling is whether a secret has just been generated and is being shown.
	Enrolling bool
	// Secret and URI exist in exactly one response and are never re-rendered: a
	// page that could redisplay the secret is one a stolen session could read.
	Secret string
	URI    string
	Notice string
}

// NeedsEnrolment reports whether the person must set 2FA up before they can
// verify.
func (v TwoFactorView) NeedsEnrolment() bool { return v.Enabled && !v.Enrolled && !v.Enrolling }

// CanVerify reports whether a code can be submitted.
func (v TwoFactorView) CanVerify() bool { return v.Enabled && v.Enrolled && !v.Enrolling }

// SecretGroups breaks the secret into four-character blocks: a mistyped secret
// is an authenticator generating codes goen rejects, with no clue why.
func (v TwoFactorView) SecretGroups() []string {
	const group = 4
	out := make([]string, 0, len(v.Secret)/group+1)
	for i := 0; i < len(v.Secret); i += group {
		end := min(i+group, len(v.Secret))
		out = append(out, v.Secret[i:end])
	}
	return out
}
