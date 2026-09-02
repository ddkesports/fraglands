package core

import "sync"

// Lobby is one Fraglands lobby with a fixed slot capacity. A lobby is not
// owned by anyone: no account holds possession of it, and membership is only
// ever a slot reservation held by a real account. Slots are never filled by
// synthetic members or bots.
type Lobby struct {
	// ID is the lobby identifier.
	ID string
	// Capacity is the fixed number of slots.
	Capacity int

	// slots maps slot index to the reserving account ID.
	slots map[int]string
	// byAccount maps account ID to its claimed slot index.
	byAccount map[string]int
	// mtx guards slots and byAccount.
	mtx sync.Mutex
}

// NewLobby constructs an empty lobby with a fixed slot capacity.
func NewLobby(id string, capacity int) (*Lobby, error) {
	if capacity <= 0 {
		return nil, ErrInvalidLobbyCapacity
	}
	return &Lobby{
		ID:        id,
		Capacity:  capacity,
		slots:     make(map[int]string),
		byAccount: make(map[string]int),
	}, nil
}

// Claim reserves the lowest free slot for the account. A repeated claim by
// the same account is idempotent and returns the slot it already holds. A
// claim against a full lobby is refused and reserves nothing.
func (l *Lobby) Claim(accountID string) (int, error) {
	if accountID == "" {
		return -1, ErrInvalidAccount
	}
	l.mtx.Lock()
	defer l.mtx.Unlock()

	if slot, ok := l.byAccount[accountID]; ok {
		return slot, nil
	}
	if len(l.slots) >= l.Capacity {
		return -1, ErrLobbyFull
	}
	for slot := 0; slot < l.Capacity; slot++ {
		if _, taken := l.slots[slot]; !taken {
			l.slots[slot] = accountID
			l.byAccount[accountID] = slot
			return slot, nil
		}
	}
	return -1, ErrLobbyFull
}

// Release frees the slot held by the account. Releasing an account with no
// reservation is refused and changes nothing.
func (l *Lobby) Release(accountID string) error {
	if accountID == "" {
		return ErrInvalidAccount
	}
	l.mtx.Lock()
	defer l.mtx.Unlock()

	slot, ok := l.byAccount[accountID]
	if !ok {
		return ErrNoSlotClaimed
	}
	delete(l.byAccount, accountID)
	delete(l.slots, slot)
	return nil
}

// Slot returns the slot index the account holds, and whether it holds one.
func (l *Lobby) Slot(accountID string) (int, bool) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	slot, ok := l.byAccount[accountID]
	return slot, ok
}

// Occupied returns the number of reserved slots.
func (l *Lobby) Occupied() int {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return len(l.slots)
}
