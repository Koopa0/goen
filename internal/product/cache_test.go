package product

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestDisabledCacheReadsThroughFill(t *testing.T) {
	cache, err := OpenPresentationCache("", DefaultCacheConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cache.Close()
	if cache.Enabled() {
		t.Fatal("empty address should disable cache")
	}
	want := Presentation{ProductID: uuid.New(), Name: "probe"}
	got, err := cache.Get(t.Context(), want.ProductID, 1, "zh-Hant",
		func(context.Context) (Presentation, error) { return want, nil })
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != want.Name {
		t.Fatalf("got %q, want %q", got.Name, want.Name)
	}
}
