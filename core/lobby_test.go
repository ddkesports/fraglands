package core

import "testing"

func TestLobbyClaimAssignsLowestFreeSlot(t *testing.T) {
	l, err := NewLobby("lobby-1", 4)
	if err != nil {
		t.Fatal(err.Error())
	}
	for want := 0; want < 3; want++ {
		slot, err := l.Claim("acct-" + string(rune('a'+want)))
		if err != nil {
			t.Fatal(err.Error())
		}
		if slot != want {
			t.Fatalf("expected slot %d, got %d", want, slot)
		}
	}
	if l.Occupied() != 3 {
		t.Fatalf("expected 3 occupied, got %d", l.Occupied())
	}
}

func TestLobbyClaimIdempotentPerAccount(t *testing.T) {
	l, _ := NewLobby("lobby-1", 4)
	first, err := l.Claim("acct-a")
	if err != nil {
		t.Fatal(err.Error())
	}
	second, err := l.Claim("acct-a")
	if err != nil {
		t.Fatal(err.Error())
	}
	if first != second {
		t.Fatalf("expected idempotent claim, got %d then %d", first, second)
	}
	if l.Occupied() != 1 {
		t.Fatalf("expected 1 occupied after re-claim, got %d", l.Occupied())
	}
}

func TestLobbyFullRefusesWithoutReserving(t *testing.T) {
	l, _ := NewLobby("lobby-1", 2)
	if _, err := l.Claim("acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := l.Claim("acct-b"); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := l.Claim("acct-c"); err != ErrLobbyFull {
		t.Fatalf("expected ErrLobbyFull, got %v", err)
	}
	if l.Occupied() != 2 {
		t.Fatalf("refused claim must not reserve: got %d occupied", l.Occupied())
	}
}

func TestLobbyReleaseFreesSlotForReuse(t *testing.T) {
	l, _ := NewLobby("lobby-1", 2)
	if _, err := l.Claim("acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := l.Claim("acct-b"); err != nil {
		t.Fatal(err.Error())
	}
	if err := l.Release("acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	slot, err := l.Claim("acct-c")
	if err != nil {
		t.Fatal(err.Error())
	}
	if slot != 0 {
		t.Fatalf("expected freed slot 0 to be reused, got %d", slot)
	}
	if _, err := l.Claim("acct-d"); err != ErrLobbyFull {
		t.Fatalf("expected ErrLobbyFull after refill, got %v", err)
	}
}

func TestLobbyReleaseWithoutClaimRefused(t *testing.T) {
	l, _ := NewLobby("lobby-1", 2)
	if err := l.Release("acct-a"); err != ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
	if l.Occupied() != 0 {
		t.Fatalf("refused release must not change state: got %d", l.Occupied())
	}
}

func TestLobbyRejectsInvalidCapacityAndAccount(t *testing.T) {
	if _, err := NewLobby("lobby-1", 0); err != ErrInvalidLobbyCapacity {
		t.Fatalf("expected ErrInvalidLobbyCapacity, got %v", err)
	}
	l, _ := NewLobby("lobby-1", 2)
	if _, err := l.Claim(""); err != ErrInvalidAccount {
		t.Fatalf("expected ErrInvalidAccount, got %v", err)
	}
	if err := l.Release(""); err != ErrInvalidAccount {
		t.Fatalf("expected ErrInvalidAccount, got %v", err)
	}
}

func TestLobbyNoPossessionNoBots(t *testing.T) {
	// A lobby has no owner and slots are only ever held by real accounts:
	// there is no possession transfer and no synthetic member to fill slots.
	l, _ := NewLobby("lobby-1", 2)
	if _, err := l.Claim("acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if _, ok := l.Slot("acct-a"); !ok {
		t.Fatal("claimed account must hold a slot")
	}
	if _, ok := l.Slot("acct-unknown"); ok {
		t.Fatal("unknown account must not hold a slot")
	}
	// Slots only empty by explicit release; nothing auto-fills them.
	if err := l.Release("acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if l.Occupied() != 0 {
		t.Fatalf("released slot must stay empty, got %d occupied", l.Occupied())
	}
}
