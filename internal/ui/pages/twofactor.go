package pages

type TwoFactorView struct {
	Enabled   bool
	Enrolled  bool
	Enrolling bool
	// Secret, URI and QRCode exist in exactly one response and are never re-rendered.
	Secret string
	URI    string
	QRCode string
	Notice string
}

func (v TwoFactorView) NeedsEnrolment() bool { return v.Enabled && !v.Enrolled && !v.Enrolling }

func (v TwoFactorView) CanVerify() bool { return v.Enabled && v.Enrolled && !v.Enrolling }

func (v TwoFactorView) SecretGroups() []string {
	const group = 4
	out := make([]string, 0, len(v.Secret)/group+1)
	for i := 0; i < len(v.Secret); i += group {
		end := min(i+group, len(v.Secret))
		out = append(out, v.Secret[i:end])
	}
	return out
}
