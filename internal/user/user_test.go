package user_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/koopa0/goen/internal/user"
)

func TestSession(t *testing.T) {
	digest := []byte{0xde, 0xad, 0xbe, 0xef}
	for _, tt := range []struct {
		name   string
		ctx    func(context.Context) context.Context
		want   []byte
		wantOK bool
	}{
		{"round trip", func(ctx context.Context) context.Context { return user.WithSession(ctx, digest) }, digest, true},
		{"empty digest", func(ctx context.Context) context.Context { return user.WithSession(ctx, []byte{}) }, nil, false},
		{"nil digest", func(ctx context.Context) context.Context { return user.WithSession(ctx, nil) }, nil, false},
		{"no value", func(ctx context.Context) context.Context { return ctx }, nil, false},
		{"a user is not a session", func(ctx context.Context) context.Context {
			return user.NewContext(ctx, user.User{ID: "u1"})
		}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := user.Session(tt.ctx(t.Context()))
			if ok != tt.wantOK || !bytes.Equal(got, tt.want) {
				t.Errorf("Session() = %x, %v; want %x, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
