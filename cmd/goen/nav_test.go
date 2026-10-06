package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

type dealsFake struct {
	has bool
	err error
}

func (f dealsFake) HasListedCampaigns(context.Context) (bool, error) { return f.has, f.err }

func TestTheHeaderOffersDealsWhenCampaignsAreListed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		deals dealsFake
		want  bool
	}{
		{name: "campaigns listed", deals: dealsFake{has: true}, want: true},
		{name: "none listed", deals: dealsFake{}, want: false},
		{name: "the read fails", deals: dealsFake{err: errors.New("down")}, want: true},
	}
	for _, tt := range tests {
		got := layouts.HasDeals(withNav(t.Context(), nil, dealsOffered(t.Context(), tt.deals, slog.New(slog.DiscardHandler))))
		if got != tt.want {
			t.Errorf("%s: HasDeals = %v, want %v", tt.name, got, tt.want)
		}
	}
}
