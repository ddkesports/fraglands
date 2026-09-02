package orchestrator

import (
	"errors"

	"github.com/paralin/fraglands/core"
)

// ErrAmbiguousAdmission is returned when several accounts were admitted on
// one process generation against one revision. The decoded terminal summary
// carries no account, so the attempt cannot be attributed to one private
// result: the required provenance is not representable and the summary is
// refused whole.
var ErrAmbiguousAdmission = errors.New("orchestrator: multiple accounts admitted on this process generation")

// admission records that one account consumed a join intent on one server
// process generation against one revision. Result acceptance is fenced to
// admitted accounts.
type admission struct {
	accountID         string
	revisionID        string
	processGeneration uint64
}

// admissionKey fences one admission per account per process generation.
type admissionKey struct {
	accountID         string
	processGeneration uint64
}

// AdmittedAccountFor returns the one account admitted on the server process
// generation against the revision. It is the authoritative attribution seam
// of the result path: the decoded terminal summary carries no account, so
// the account of a result comes only from admission state, never from a
// caller.
//
// Refusals are typed: no admission on the generation at all returns
// ErrUnadmittedAccount; admissions exist on the generation but none against
// this revision returns core.ErrRevisionMismatch; several accounts admitted
// against this revision return ErrAmbiguousAdmission.
func (o *Orchestrator) AdmittedAccountFor(processGeneration uint64, revisionID string) (string, error) {
	var (
		accountID   string
		matches     int
		admittedAny bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		for key, adm := range o.admissions {
			if key.processGeneration != processGeneration {
				continue
			}
			admittedAny = true
			if adm.revisionID != revisionID {
				continue
			}
			accountID = key.accountID
			matches++
		}
	})
	switch {
	case matches == 1:
		return accountID, nil
	case matches == 0 && admittedAny:
		return "", core.ErrRevisionMismatch
	case matches == 0:
		return "", ErrUnadmittedAccount
	default:
		return "", ErrAmbiguousAdmission
	}
}

// AdmittedRevisionFor reports whether one account was admitted on one
// process generation, and with which revision. It is the account-scoped
// admission read used by composition: the caller already knows the account,
// so attribution stays bound to that account instead of inferring it.
func (o *Orchestrator) AdmittedRevisionFor(accountID string, processGeneration uint64) (string, bool) {
	var (
		revisionID string
		found      bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		adm, ok := o.admissions[admissionKey{accountID: accountID, processGeneration: processGeneration}]
		if !ok {
			return
		}
		revisionID = adm.revisionID
		found = true
	})
	return revisionID, found
}
