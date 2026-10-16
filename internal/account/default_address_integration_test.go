//go:build integration

package account_test

import (
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/account"
)

func TestTheDefaultAddressCanBeMoved(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "movedefault@example.com")

	home := addAddress(t, s, u.ID, "家", true)
	work := addAddress(t, s, u.ID, "公司", false)

	if err := s.MakeDefaultAddress(ctx, u.ID, work); err != nil {
		t.Fatalf("move the default: %v", err)
	}

	got := defaultAddressID(t, u.ID)
	if got != work {
		t.Errorf("the default is %s, want the work address %s", got, work)
	}
	// Exactly one: setting before clearing trips addresses_one_default_per_user,
	// and clearing without setting leaves none.
	if n := countDefaults(t, u.ID); n != 1 {
		t.Errorf("the account has %d default addresses, want 1", n)
	}

	// And back again, so this is about moving rather than about the first move.
	if err := s.MakeDefaultAddress(ctx, u.ID, home); err != nil {
		t.Fatalf("move it back: %v", err)
	}
	if got := defaultAddressID(t, u.ID); got != home {
		t.Errorf("the default is %s, want the home address %s", got, home)
	}
}

func TestMovingTheDefaultToSomebodyElsesAddressChangesNothing(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

	mine := register(t, s, "mineaddr@example.com")
	theirs := register(t, s, "theirsaddr@example.com")
	myHome := addAddress(t, s, mine.ID, "家", true)
	theirHome := addAddress(t, s, theirs.ID, "他家", true)

	if err := s.MakeDefaultAddress(ctx, mine.ID, theirHome); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("setting a stranger's address as my default answered %v, want ErrNotFound", err)
	}
	// Mine is untouched: without the rollback the clear would have left me none.
	if got := defaultAddressID(t, mine.ID); got != myHome {
		t.Errorf("my default became %s, want %s", got, myHome)
	}
	if got := defaultAddressID(t, theirs.ID); got != theirHome {
		t.Errorf("their default became %s, want %s", got, theirHome)
	}
}
