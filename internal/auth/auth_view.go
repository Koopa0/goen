package auth

import (
	"context"
	"fmt"
	"net/url"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type SignInRecovery uint8

const (
	SignInNoRecovery SignInRecovery = iota
	SignInResetPassword
	SignInRegister
)

func (r SignInRecovery) Href() string {
	switch r {
	case SignInNoRecovery:
		return ""
	case SignInResetPassword:
		return "/forgot"
	case SignInRegister:
		return "/register"
	default:
		panic(fmt.Sprintf("sign-in: unknown recovery %d", r))
	}
}

func (r SignInRecovery) Label(ctx context.Context) string {
	switch r {
	case SignInNoRecovery:
		return ""
	case SignInResetPassword:
		return i18n.T(ctx, i18n.KeyForgotPassword)
	case SignInRegister:
		return i18n.T(ctx, i18n.KeyRegister)
	default:
		panic(fmt.Sprintf("sign-in: unknown recovery %d", r))
	}
}

type AuthView struct {
	ReturnMessage string
	HideRegister  bool
	PasswordFocus bool
	Recovery      SignInRecovery

	Email        string
	Name         string
	Next         string
	Errors       map[string]string
	Notice       string
	GoogleSignIn bool
	OffersResend bool
	// DemoEmail and DemoPassword are the account every visitor of a public demonstration shares; empty is none.
	DemoEmail    string
	DemoPassword string
}

func (v AuthView) OffersDemoAccount() bool { return v.DemoEmail != "" }

type RegisterCompleteView struct {
	Token string
	Next  string
	Error string
}

func (v RegisterCompleteView) HasError() bool { return v.Error != "" }

func RegisterCompleteMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyRegisterCompleteTitle)}
}

func (v AuthView) authPageLink(path string) string {
	if v.Next == "" || v.Next == "/account" {
		return path
	}
	return path + "?next=" + url.QueryEscape(v.Next)
}

func (v AuthView) GoogleLink() string {
	if v.Next == "" {
		return "/auth/google"
	}
	return "/auth/google?next=" + url.QueryEscape(v.Next)
}

func SignInMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeySignIn)}
}

func RegisterMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyRegister)}
}

func (v AuthView) PasswordAttrs(attrs templ.Attributes) templ.Attributes {
	if !v.HasErr("password") {
		attrs["aria-describedby"] = "password-hint"
	}
	return attrs
}

func (v AuthView) Err(field string) string { return v.Errors[field] }

func (v AuthView) HasErr(field string) bool { return v.Errors[field] != "" }

func (v AuthView) AnyErrors() bool { return len(v.Errors) > 0 }

func (v AuthView) HasNotice() bool { return v.Notice != "" }
