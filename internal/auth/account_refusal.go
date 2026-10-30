package auth

// AccountForm names a form on the account page that auth answers, which is the
// section a refusal returns to.
type AccountForm string

const (
	EmailForm    AccountForm = "email"
	PasswordForm AccountForm = "password"
	EraseForm    AccountForm = "erase"
)

// AccountRefusal is a refused request from one of those forms, as the account
// page shows it: the section reopens at 422, says why, and puts back what was
// typed.
type AccountRefusal struct {
	Form AccountForm
	// Notice says why when no single control explains it. A refusal that names
	// controls leaves it empty.
	Notice string
	// Errors is the message under each refused control, by its form field name.
	Errors map[string]string
	// Values is what was typed into the controls worth putting back, by form
	// field name. A password is never in it.
	Values map[string]string
	// OpenReturn marks an erasure refused for a return still being processed.
	// The page words it with the order's number when the account has one such
	// return, and with Notice otherwise.
	OpenReturn bool
}
