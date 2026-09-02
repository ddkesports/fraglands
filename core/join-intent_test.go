package core

import (
	"testing"
)

func TestJoinIntentConsume(t *testing.T) {
	const (
		revID = "rev-1"
		gen   = uint64(3)
	)
	steam := SteamID(76561198000000001)
	j := NewJoinIntent("intent-1", "acct-1", steam, revID, gen)

	if err := j.Consume(revID, gen, steam); err != nil {
		t.Fatal(err.Error())
	}
	if !j.Used() {
		t.Fatal("expected intent consumed")
	}

	// One-use: second consume is refused.
	if err := j.Consume(revID, gen, steam); err != ErrIntentAlreadyUsed {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}
}

func TestJoinIntentFencing(t *testing.T) {
	steam := SteamID(76561198000000001)
	j := NewJoinIntent("intent-2", "acct-1", steam, "rev-1", 3)

	if err := j.Consume("rev-other", 3, steam); err != ErrRevisionMismatch {
		t.Fatalf("expected ErrRevisionMismatch, got %v", err)
	}
	if err := j.Consume("rev-1", 4, steam); err != ErrGenerationMismatch {
		t.Fatalf("expected ErrGenerationMismatch, got %v", err)
	}
	if err := j.Consume("rev-1", 3, SteamID(999)); err != ErrSteamIDAlreadyBound {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	// Refused consumes never burn the intent.
	if j.Used() {
		t.Fatal("refused consume must not mark used")
	}
	if err := j.Consume("rev-1", 3, steam); err != nil {
		t.Fatal(err.Error())
	}
}
