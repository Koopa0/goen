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
		ctx    context.Context
		want   []byte
		wantOK bool
	}{
		{"round trip", user.WithSession(context.Background(), digest), digest, true},
		{"empty digest", user.WithSession(context.Background(), []byte{}), nil, false},
		{"nil digest", user.WithSession(context.Background(), nil), nil, false},
		{"no value", context.Background(), nil, false},
		{"a user is not a session", user.NewContext(context.Background(), user.User{ID: "u1"}), nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := user.Session(tt.ctx)
			if ok != tt.wantOK || !bytes.Equal(got, tt.want) {
				t.Errorf("Session() = %x, %v; want %x, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
