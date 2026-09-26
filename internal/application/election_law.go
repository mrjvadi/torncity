package application

import (
	"context"

	"github.com/mrjvadi/torncity/internal/domain/election"
)

// This file is the one place election law is read as a policy value: the
// lever code an elected office's law lives at, and the resolved document
// turned into the domain's election.Rules. Nothing else reads an
// election_law lever's value; OpenElection (handlers) calls this once, when
// an election opens, and freezes the result on the election row, so a later
// change to the law never reaches an election already under way — the real
// world's rule that a change to electoral law takes effect only from the
// next election (docs/adr/0015 section 2).

// ElectionLawLeverCode is the lever code an office's election law lives at:
// "<jurisdiction>.election_law.<office>". jurisdiction is the office's own
// level ("city", "country"), not the jurisdiction instance it is asked of.
func ElectionLawLeverCode(officeJurisdiction, officeCode string) string {
	return officeJurisdiction + ".election_law." + officeCode
}

// ElectionLaw is one office's election law, resolved: the domain rules
// election.CanStand and election.CanVote take, and the raw document (so a
// caller can also read EndorsementsRequired, TermLimitConsecutive and so on
// without recomputing them — election.RulesFromFields already carries every
// field, so Doc exists only for a caller that wants the field by name, e.g.
// a "can I stand" checklist).
type ElectionLaw struct {
	Rules election.Rules
	Doc   map[string]int64
	// Value is the resolved PolicyValue in full: Source, Pending, Acting —
	// everything a transparency screen shows about who may change the law
	// and what change is already announced.
	Value PolicyValue
}

// ResolveElectionLaw reads an elected office's election law in force now, in
// jurisdictionID. It refuses ErrUnknownLever when the office has none (a
// content bug: every office acquired by election must have exactly one,
// enforced at load) and ErrWrongJurisdiction when jurisdictionID is not of
// officeJurisdiction's level.
func ResolveElectionLaw(ctx context.Context, reader PolicyReader, officeJurisdiction, officeCode, jurisdictionID string,
) (ElectionLaw, error) {
	v, err := reader.Get(ctx, jurisdictionID, ElectionLawLeverCode(officeJurisdiction, officeCode))
	if err != nil {
		return ElectionLaw{}, err
	}
	return ElectionLaw{Rules: election.RulesFromFields(v.ElectionLaw), Doc: v.ElectionLaw, Value: v}, nil
}
