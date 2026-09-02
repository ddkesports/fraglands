package core

import (
	"testing"
)

func TestAccountBindSteamID(t *testing.T) {
	a := &Account{ID: "acct-1"}
	if err := a.BindSteamID(76561198000000001); err != nil {
		t.Fatal(err.Error())
	}
	if a.SteamID != 76561198000000001 {
		t.Fatal("expected steam id to bind")
	}

	// The same binding is idempotent.
	if err := a.BindSteamID(76561198000000001); err != nil {
		t.Fatal(err.Error())
	}

	// A different identity never moves the binding.
	if err := a.BindSteamID(76561198000000002); err != ErrSteamIDAlreadyBound {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	if a.SteamID != 76561198000000001 {
		t.Fatal("binding moved between accounts")
	}

	// Zero identity is invalid.
	b := &Account{ID: "acct-2"}
	if err := b.BindSteamID(0); err != ErrInvalidSteamID {
		t.Fatalf("expected ErrInvalidSteamID, got %v", err)
	}
}
