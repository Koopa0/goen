package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func TestPickingCopyDescribesEveryOrderStillOwingAParcel(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale           Locale
		scope, remaining string
	}{
		{locale: ZhHant, scope: "涵蓋所有尚有商品未出貨的訂單", remaining: "尚未出貨數量"},
		{locale: En, scope: "every order with items still to ship", remaining: "Not yet dispatched"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := WithLocale(t.Context(), tt.locale)
			if got := fmt.Sprintf(T(ctx, KeyAdminPickingScope), 50); !strings.Contains(got, tt.scope) {
				t.Errorf("picking scope=%q, want %q", got, tt.scope)
			}
			if got := T(ctx, KeyAdminPickingRemaining); got != tt.remaining {
				t.Errorf("remaining label=%q, want %q", got, tt.remaining)
			}
		})
	}
}
