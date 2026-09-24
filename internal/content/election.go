package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/election"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrInvalidElection means an office's election terms are unusable.
var ErrInvalidElection = errors.New("content: invalid election")

// ElectionDef is how one elected office is elected (governance.yml
// elections). Durations are REAL time, like an office's term: an election is
// a governance guarantee (docs/adr/0018-game-clock.md; see the election
// package).
type ElectionDef struct {
	// Office is the office code; it must be acquired by election.
	Office string `yaml:"office" json:"office"`
	// Candidacy and Voting are the two periods, as Go durations.
	Candidacy string `yaml:"candidacy" json:"candidacy"`
	Voting    string `yaml:"voting" json:"voting"`
	// MinLevel and MinResidency are what a candidate needs; CleanRecord
	// that they owe nothing to victims or the city and are not in jail.
	MinLevel     int    `yaml:"min_level" json:"min_level"`
	MinResidency string `yaml:"min_residency" json:"min_residency"`
	CleanRecord  bool   `yaml:"clean_record" json:"clean_record"`
	// VoterMinResidency is how long a resident must have lived there to
	// vote.
	VoterMinResidency string `yaml:"voter_min_residency" json:"voter_min_residency"`
	// Deposit, in minor units, is what standing costs; RefundShareBPS the
	// share of the votes cast that earns it back.
	Deposit        int64 `yaml:"deposit" json:"deposit"`
	RefundShareBPS int   `yaml:"refund_share_bps" json:"refund_share_bps"`
	// ReopenAfter is how long after a count that filled no seat the next
	// election opens.
	ReopenAfter string `yaml:"reopen_after" json:"reopen_after"`
}

// Rules converts the definition to the election rules.
func (d ElectionDef) Rules() (election.Rules, error) {
	var errs []error
	dur := func(field, raw string, required bool) time.Duration {
		if raw == "" && !required {
			return 0
		}
		v, err := parseLeverDuration(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %v", field, err))
		}
		return v
	}
	r := election.Rules{
		Candidacy:         dur("candidacy", d.Candidacy, true),
		Voting:            dur("voting", d.Voting, true),
		MinLevel:          d.MinLevel,
		MinResidency:      dur("min_residency", d.MinResidency, false),
		CleanRecord:       d.CleanRecord,
		Deposit:           money.FromMinor(d.Deposit),
		RefundShareBPS:    d.RefundShareBPS,
		VoterMinResidency: dur("voter_min_residency", d.VoterMinResidency, false),
		ReopenAfter:       dur("reopen_after", d.ReopenAfter, true),
	}
	if err := errors.Join(errs...); err != nil {
		return r, err
	}
	return r, r.Validate()
}

// validateElections checks every election against the offices.
func (p *Pack) validateElections(problems *[]error) {
	offices := make(map[string]OfficeDef, len(p.Offices))
	for _, o := range p.Offices {
		offices[o.Code] = o
	}
	seen := map[string]bool{}
	for _, e := range p.Elections {
		bad := func(format string, args ...any) {
			*problems = append(*problems, fmt.Errorf("%w: %s: %s", ErrInvalidElection, e.Office, fmt.Sprintf(format, args...)))
		}
		o, ok := offices[e.Office]
		switch {
		case !ok:
			bad("no such office")
			continue
		case o.AcquiredBy != AcquiredByElection:
			bad("the office is acquired by %s, not by election", o.AcquiredBy)
		case seen[e.Office]:
			bad("the office has two elections")
		}
		seen[e.Office] = true
		if _, err := e.Rules(); err != nil {
			bad("%v", err)
		}
		if e.Deposit < 0 || e.Deposit > 1_000_000_000 {
			bad("deposit %d is outside 0..1000000000", e.Deposit)
		}
	}
}

// buildElections indexes the elections by office.
func (s *Snapshot) buildElections(p *Pack) {
	s.elections = make(map[string]ElectionDef, len(p.Elections))
	for _, e := range p.Elections {
		s.elections[e.Office] = e
	}
}

// Election returns how an office is elected, and whether it is.
func (s *Snapshot) Election(office string) (ElectionDef, election.Rules, bool) {
	d, ok := s.elections[office]
	if !ok {
		return ElectionDef{}, election.Rules{}, false
	}
	r, err := d.Rules()
	if err != nil {
		return d, r, false
	}
	return d, r, true
}

// Elections lists the elected offices' election terms, in no set order.
func (s *Snapshot) Elections() []ElectionDef {
	out := make([]ElectionDef, 0, len(s.elections))
	for _, e := range s.elections {
		out = append(out, e)
	}
	return out
}
