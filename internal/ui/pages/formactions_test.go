package pages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

// These fixtures bind each method-only expression to its production receiver.
// Both arms of conditional actions matter; strings copied from routes would
// leave a broken method invisible to the route gate.
func methodFormActions(t *testing.T) map[string][]string {
	t.Helper()
	product := &pages.AdminProductView{Slug: "product"}
	newProduct := &pages.AdminProductView{IsNew: true}
	pdp := &pages.ProductView{Slug: "product"}
	ret := &pages.AdminReturn{ID: "return"}
	return map[string][]string{
		"layouts/banner.templ:templ.SafeURL(b.DismissAction())":         {(layouts.Banner{}).DismissAction()},
		"pages/newsletter.templ:templ.SafeURL(v.Action)":                newsletterFormActions(t),
		"pages/adminproduct.templ:templ.SafeURL(v.Action())":            {product.Action(), newProduct.Action()},
		"pages/adminproduct.templ:templ.SafeURL(v.ImageAction())":       {product.ImageAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.ImageRemoveAction())": {product.ImageRemoveAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.ReuseAction())":       {product.ReuseAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.OptionAction())":      {product.OptionAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.OptionValueAction())": {product.OptionValueAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.SpecAction())":        {product.SpecAction()},
		"pages/adminproduct.templ:templ.SafeURL(v.SpecRemoveAction())":  {product.SpecRemoveAction()},
		"pages/product.templ:templ.SafeURL(v.NotifyAction())":           {pdp.NotifyAction()},
		"pages/product.templ:templ.SafeURL(v.AskAction())":              {pdp.AskAction()},
		"pages/product.templ:templ.SafeURL(v.ReviewAction())":           {pdp.ReviewAction()},
		"pages/adminreturns.templ:templ.SafeURL(r.Action())":            {ret.Action()},
		"pages/adminreturns.templ:templ.SafeURL(r.AssessAction())":      {ret.AssessAction()},
		"pages/adminreturns.templ:templ.SafeURL(r.InspectAction())":     {ret.InspectAction()},
		"pages/adminreturns.templ:templ.SafeURL(r.CompleteAction())":    {ret.CompleteAction()},
		"pages/adminmessage.templ:templ.SafeURL(m.Action())":            {(pages.AdminMessage{}).Action(), (pages.AdminMessage{Handled: true}).Action()},
		"pages/adminreview.templ:templ.SafeURL(r.Action())":             {(pages.AdminReview{}).Action(), (pages.AdminReview{Hidden: true}).Action()},
		"pages/admincoupon.templ:templ.SafeURL(c.Action())":             {(pages.AdminCoupon{Code: "coupon"}).Action()},
		"pages/adminnewsletter.templ:templ.SafeURL(issue.SendAction())": {(pages.AdminNewsletterIssue{ID: "issue"}).SendAction()},
		"pages/admincampaign.templ:templ.SafeURL(c.ToggleAction())":     {(pages.AdminCampaign{Slug: "campaign"}).ToggleAction()},
		"pages/admincampaign.templ:templ.SafeURL(v.FeatureAction())":    {(pages.AdminCampaignView{Slug: "campaign"}).FeatureAction()},
		"pages/adminhome.templ:templ.SafeURL(slide.PromoteAction())":    {(pages.AdminHeroSlide{ID: "slide"}).PromoteAction()},
		"pages/adminhome.templ:templ.SafeURL(slide.ToggleAction())":     {(pages.AdminHeroSlide{ID: "slide"}).ToggleAction()},
		"pages/adminquestion.templ:templ.SafeURL(q.AnswerAction())":     {(pages.AdminQuestion{ID: "question"}).AnswerAction()},
		"pages/listing.templ:templ.SafeURL(v.FilterAction())":           {(pages.ListingView{Slug: "category"}).FilterAction()},
		"pages/warranty.templ:templ.SafeURL(v.Action())":                {(pages.WarrantyOrderView{Number: "order"}).Action()},
		"pages/pay.templ:templ.SafeURL(v.Action())":                     {(pages.PayView{Number: "order"}).Action()},
		"pages/returns.templ:templ.SafeURL(v.Action())":                 {(pages.ReturnsView{Number: "order"}).Action()},
	}
}

// Newsletter actions are fields supplied by its handler, so read that
// production assignment instead of duplicating the two URLs in fixtures.
func newsletterFormActions(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot(t), "internal", "newsletter", "handler.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || typ.Sel.Name != "NewsletterActionView" {
			return true
		}
		for _, element := range lit.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok || key.Name != "Action" {
				continue
			}
			value, ok := pair.Value.(*ast.BasicLit)
			if !ok || value.Kind != token.STRING {
				t.Error("newsletter Action needs a resolvable production value")
				continue
			}
			action, err := strconv.Unquote(value.Value)
			if err != nil {
				t.Fatal(err)
			}
			actions = append(actions, action)
		}
		return true
	})
	if len(actions) == 0 {
		t.Fatal("no newsletter form actions found")
	}
	return actions
}
