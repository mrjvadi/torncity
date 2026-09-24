package application

import (
	"context"
	stderrors "errors"
	"testing"
	"time"
)

// The governance sentinels join the distinguishability checks in
// errors_test.go: each refusal must be told apart from every other.
func init() {
	namedSentinels["ErrUnknownLever"] = ErrUnknownLever
	namedSentinels["ErrJurisdictionNotFound"] = ErrJurisdictionNotFound
	namedSentinels["ErrWrongJurisdiction"] = ErrWrongJurisdiction
	namedSentinels["ErrLeverKindUnsupported"] = ErrLeverKindUnsupported
	namedSentinels["ErrPolicyRequiresVote"] = ErrPolicyRequiresVote
	namedSentinels["ErrNotOfficeHolder"] = ErrNotOfficeHolder
	namedSentinels["ErrPolicyOutOfBounds"] = ErrPolicyOutOfBounds
	namedSentinels["ErrPolicyCooldown"] = ErrPolicyCooldown
	namedSentinels["ErrOfficeNotFound"] = ErrOfficeNotFound
	namedSentinels["ErrOfficeOccupied"] = ErrOfficeOccupied
	namedSentinels["ErrOfficeVacant"] = ErrOfficeVacant
	namedSentinels["ErrAlreadyHoldsSeat"] = ErrAlreadyHoldsSeat
	namedSentinels["ErrIncompatibleOffices"] = ErrIncompatibleOffices
}

// ---------------------------------------------------------------------------
// A world of two cities in one country, in memory.
// ---------------------------------------------------------------------------

const (
	cityA   = "00000000-0000-4000-8000-00000000000a"
	cityB   = "00000000-0000-4000-8000-00000000000b"
	country = "00000000-0000-4000-8000-0000000000c0"

	mayorA  = "player-mayor-a"
	mayorB  = "player-mayor-b"
	deputyA = "player-deputy-a"
	someone = "player-nobody"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func taxLever() LeverDefinition {
	return LeverDefinition{
		Code: "city.tax_rate", Jurisdiction: "city", Type: "bps", ValueKind: ValueKindScalar,
		Default: 450, Min: 0, Max: 2500, HeldBy: "mayor", DecisionRule: DecisionSingle,
		ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour, CityDefault: "tax_rate_bps",
	}
}

// fakeGov is GovernanceRepository in memory. Its PolicyInputs selects the
// settings the way the SQL does — the latest in effect and every pending one —
// so the tests exercise ResolvePolicy on the same inputs production gives it.
type fakeGov struct {
	levers        map[string]LeverDefinition
	jurisdictions map[string]Jurisdiction
	cityDefaults  map[string]int64
	offices       []OfficeDefinition
	seats         []Office
	settings      []PolicySetting

	recorded []PolicyChange
	assigned []Office
	locks    int
}

func newFakeGov() *fakeGov {
	g := &fakeGov{
		levers: map[string]LeverDefinition{"city.tax_rate": taxLever()},
		jurisdictions: map[string]Jurisdiction{
			cityA:   {ID: cityA, Kind: "city", Code: "alpha", ParentID: country},
			cityB:   {ID: cityB, Kind: "city", Code: "bravo", ParentID: country},
			country: {ID: country, Kind: "country", Code: "home", ParentID: WorldJurisdictionID},
		},
		cityDefaults: map[string]int64{cityA: 120, cityB: 1450},
		offices: []OfficeDefinition{
			{Code: "mayor", Jurisdiction: "city", Seats: 1, Deputy: "deputy_mayor", IncompatibleWith: []string{"city_council"}},
			{Code: "deputy_mayor", Jurisdiction: "city", Seats: 1},
			{Code: "city_council", Jurisdiction: "city", Seats: 2},
			{Code: "president", Jurisdiction: "country", Seats: 1, Term: 1344 * time.Hour},
		},
	}
	for _, j := range []string{cityA, cityB} {
		g.seats = append(g.seats,
			Office{ID: "mayor@" + j, OfficeCode: "mayor", JurisdictionID: j, Seat: 1},
			Office{ID: "deputy@" + j, OfficeCode: "deputy_mayor", JurisdictionID: j, Seat: 1},
			Office{ID: "council1@" + j, OfficeCode: "city_council", JurisdictionID: j, Seat: 1},
			Office{ID: "council2@" + j, OfficeCode: "city_council", JurisdictionID: j, Seat: 2},
		)
	}
	g.seats = append(g.seats, Office{ID: "president@" + country, OfficeCode: "president", JurisdictionID: country, Seat: 1})
	return g
}

// seat puts a holder in a seat directly, for arranging a test.
func (g *fakeGov) seat(id, holder string) {
	for i := range g.seats {
		if g.seats[i].ID == id {
			g.seats[i].HolderPlayerID = holder
			g.seats[i].AcquiredBy = AcquiredByAppointment
			return
		}
	}
	panic("no seat " + id)
}

func (g *fakeGov) LockLever(context.Context, string, string) error { g.locks++; return nil }

func (g *fakeGov) PolicyInputs(_ context.Context, jid, code string, now time.Time) (PolicyInputs, error) {
	var in PolicyInputs
	l, ok := g.levers[code]
	if !ok {
		return in, ErrUnknownLever
	}
	j, ok := g.jurisdictions[jid]
	if !ok {
		return in, ErrJurisdictionNotFound
	}
	in.Lever, in.Jurisdiction = l, j
	if d, ok := g.cityDefaults[jid]; ok && l.CityDefault != "" {
		in.CityDefault = &d
	}

	deputy := map[string]string{}
	for _, o := range g.offices {
		deputy[o.Code] = o.Deputy
	}
	seen := map[string]bool{}
	for code := l.HeldBy; code != "" && !seen[code]; code = deputy[code] {
		seen[code] = true
		link := OfficeLink{OfficeCode: code}
		for _, s := range g.seats {
			if s.OfficeCode == code && s.JurisdictionID == jid {
				link.Seats = append(link.Seats, s)
			}
		}
		in.Chain = append(in.Chain, link)
	}

	var latest *PolicySetting
	for i := range g.settings {
		s := g.settings[i]
		if s.JurisdictionID != jid || s.LeverCode != code {
			continue
		}
		if in.LastChangeAt == nil || s.SetAt.After(*in.LastChangeAt) {
			at := s.SetAt
			in.LastChangeAt = &at
		}
		if s.EffectiveAt.After(now) {
			in.Settings = append(in.Settings, s)
		} else if latest == nil || laterSetting(s, *latest) {
			latest = &s
		}
	}
	if latest != nil {
		in.Settings = append(in.Settings, *latest)
	}
	return in, nil
}

func (g *fakeGov) RecordPolicy(_ context.Context, c PolicyChange) (PolicyChange, error) {
	c.ID = "change-" + time.Now().String()
	c.Setting.ID = "value-" + c.Setting.SetAt.String()
	g.recorded = append(g.recorded, c)
	g.settings = append(g.settings, c.Setting)
	return c, nil
}

func (g *fakeGov) Jurisdiction(_ context.Context, id string) (Jurisdiction, error) {
	j, ok := g.jurisdictions[id]
	if !ok {
		return j, ErrJurisdictionNotFound
	}
	return j, nil
}

func (g *fakeGov) OfficeDefinitions(context.Context) ([]OfficeDefinition, error) {
	return g.offices, nil
}

func (g *fakeGov) Seat(_ context.Context, code, jid string, seat int) (Office, error) {
	for _, s := range g.seats {
		if s.OfficeCode == code && s.JurisdictionID == jid && s.Seat == seat {
			return s, nil
		}
	}
	return Office{}, ErrOfficeNotFound
}

func (g *fakeGov) SeatsHeldBy(_ context.Context, player string) ([]Office, error) {
	var out []Office
	for _, s := range g.seats {
		if s.HolderPlayerID == player {
			out = append(out, s)
		}
	}
	return out, nil
}

func (g *fakeGov) ActingChain(_ context.Context, officeCode, jid string) ([]OfficeLink, error) {
	deputy := map[string]string{}
	for _, o := range g.offices {
		deputy[o.Code] = o.Deputy
	}
	var chain []OfficeLink
	seen := map[string]bool{}
	for code := officeCode; code != "" && !seen[code]; code = deputy[code] {
		seen[code] = true
		link := OfficeLink{OfficeCode: code}
		for _, s := range g.seats {
			if s.OfficeCode == code && s.JurisdictionID == jid {
				link.Seats = append(link.Seats, s)
			}
		}
		chain = append(chain, link)
	}
	return chain, nil
}

func (g *fakeGov) AssignSeat(_ context.Context, o Office) error {
	for i := range g.seats {
		if g.seats[i].ID == o.ID {
			g.seats[i] = o
			g.assigned = append(g.assigned, o)
			return nil
		}
	}
	return ErrOfficeNotFound
}

// govTx is a Tx whose only reachable repository is the governance one. The
// embedded nil interface makes any other use panic, loudly.
type govTx struct {
	Tx
	gov *fakeGov
}

func (t govTx) Governance() GovernanceRepository { return t.gov }

func inputs(t *testing.T, g *fakeGov, jid string, now time.Time) PolicyInputs {
	t.Helper()
	in, err := g.PolicyInputs(context.Background(), jid, "city.tax_rate", now)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func setting(value int64, setAt time.Time, notice time.Duration) PolicySetting {
	return PolicySetting{
		ID: "v" + setAt.Format(time.RFC3339), JurisdictionID: cityA, LeverCode: "city.tax_rate", Value: value,
		SetByPlayerID: mayorA, OfficeID: "mayor@" + cityA, SetAt: setAt, EffectiveAt: setAt.Add(notice),
	}
}

// ---------------------------------------------------------------------------
// ResolvePolicy: the one precedence function.
// ---------------------------------------------------------------------------

func TestResolveDefaultsWhenNothingIsSet(t *testing.T) {
	g := newFakeGov()

	v := ResolvePolicy(inputs(t, g, cityA, t0), t0)
	if v.Value != 120 || v.Source != PolicyFromDefault || v.InForce != nil {
		t.Errorf("alpha = %d from %s, want its own default 120", v.Value, v.Source)
	}

	// Without a per-city source the lever's own default applies.
	l := taxLever()
	l.CityDefault = ""
	g.levers["city.tax_rate"] = l
	if v := ResolvePolicy(inputs(t, g, cityA, t0), t0); v.Value != 450 {
		t.Errorf("without a city default = %d, want the lever default 450", v.Value)
	}
}

// Invariant 5: a vacant office — here every seat of the chain — yields the
// default, never an error, and names nobody as acting.
func TestResolveVacantOfficeYieldsTheDefault(t *testing.T) {
	g := newFakeGov()
	v := ResolvePolicy(inputs(t, g, cityB, t0), t0)
	if v.Value != 1450 || v.Source != PolicyFromDefault {
		t.Errorf("vacant bravo = %d from %s, want default 1450", v.Value, v.Source)
	}
	if v.Acting != nil {
		t.Errorf("a vacant chain names %+v as acting", v.Acting)
	}
	// And through the reader contract: no error either.
	if _, err := (fakeReader{g}).Get(context.Background(), cityB, "city.tax_rate"); err != nil {
		t.Errorf("reading a lever of a vacant office failed: %v", err)
	}
}

func TestResolveNotYetEffectiveReturnsTheDefault(t *testing.T) {
	g := newFakeGov()
	g.settings = []PolicySetting{setting(800, t0, 24*time.Hour)}

	v := ResolvePolicy(inputs(t, g, cityA, t0.Add(23*time.Hour)), t0.Add(23*time.Hour))
	if v.Value != 120 || v.Source != PolicyFromDefault {
		t.Errorf("inside the notice = %d from %s, want the default 120", v.Value, v.Source)
	}
	if v.Pending == nil || v.Pending.Value != 800 {
		t.Errorf("the announced change is not reported as pending: %+v", v.Pending)
	}
}

func TestResolveEffectiveValueWins(t *testing.T) {
	g := newFakeGov()
	g.settings = []PolicySetting{setting(800, t0, 24*time.Hour)}

	at := t0.Add(24 * time.Hour) // exactly when notice ends
	v := ResolvePolicy(inputs(t, g, cityA, at), at)
	if v.Value != 800 || v.Source != PolicyFromOffice || v.InForce == nil {
		t.Errorf("after the notice = %d from %s, want 800 from the office", v.Value, v.Source)
	}
	if v.Pending != nil {
		t.Errorf("an applied change is still pending: %+v", v.Pending)
	}
}

func TestResolveLatestOfSeveralWins(t *testing.T) {
	g := newFakeGov()
	g.settings = []PolicySetting{
		setting(800, t0, 24*time.Hour),
		setting(900, t0.Add(100*time.Hour), 24*time.Hour),
		setting(300, t0.Add(200*time.Hour), 24*time.Hour),
	}
	at := t0.Add(150 * time.Hour)
	v := ResolvePolicy(inputs(t, g, cityA, at), at)
	if v.Value != 900 {
		t.Errorf("at +150h = %d, want 900 (the latest in effect)", v.Value)
	}
	at = t0.Add(300 * time.Hour)
	if v := ResolvePolicy(inputs(t, g, cityA, at), at); v.Value != 300 {
		t.Errorf("at +300h = %d, want 300", v.Value)
	}

	// Two settings taking effect at the same instant: the later announcement
	// wins, whatever order they arrive in.
	a, b := setting(700, t0, 48*time.Hour), setting(650, t0.Add(24*time.Hour), 24*time.Hour)
	for _, order := range [][]PolicySetting{{a, b}, {b, a}} {
		in := PolicyInputs{Lever: taxLever(), Settings: order}
		if v := ResolvePolicy(in, t0.Add(72*time.Hour)); v.Value != 650 {
			t.Errorf("tie at the same effective instant = %d, want 650 (announced later)", v.Value)
		}
	}
}

// A value set under wider bounds never escapes the operator's current ones.
func TestResolveClampsIntoCurrentBounds(t *testing.T) {
	in := PolicyInputs{Lever: taxLever(), Settings: []PolicySetting{setting(2400, t0, 0)}}
	in.Lever.Max = 2000
	v := ResolvePolicy(in, t0.Add(time.Hour))
	if v.Value != 2000 || !v.Clamped {
		t.Errorf("= %d clamped=%v, want 2000 clamped", v.Value, v.Clamped)
	}
}

// Setting outlives setter: vacating the seat changes who may act, not the law.
func TestResolveValueOutlivesItsSetter(t *testing.T) {
	g := newFakeGov()
	g.settings = []PolicySetting{setting(800, t0, 0)}
	v := ResolvePolicy(inputs(t, g, cityA, t0.Add(time.Hour)), t0.Add(time.Hour))
	if v.Value != 800 || v.Acting != nil {
		t.Errorf("with the mayor gone = %d acting %+v, want 800 and nobody acting", v.Value, v.Acting)
	}
}

func TestResolveActingChain(t *testing.T) {
	g := newFakeGov()

	g.seat("deputy@"+cityA, deputyA)
	v := ResolvePolicy(inputs(t, g, cityA, t0), t0)
	if v.Acting == nil || v.Acting.OfficeCode != "deputy_mayor" || !v.Acting.Deputy {
		t.Fatalf("with only a deputy, acting = %+v, want the deputy mayor acting", v.Acting)
	}

	g.seat("mayor@"+cityA, mayorA)
	v = ResolvePolicy(inputs(t, g, cityA, t0), t0)
	if v.Acting == nil || v.Acting.OfficeCode != "mayor" || v.Acting.Deputy ||
		v.Acting.Holders[0].HolderPlayerID != mayorA {
		t.Errorf("with a mayor, acting = %+v, want the mayor", v.Acting)
	}
}

// fakeReader is PolicyReader over the fake, the way the postgres reader is
// over SQL: inputs, the level check, the precedence function.
type fakeReader struct{ g *fakeGov }

func (r fakeReader) Get(ctx context.Context, jid, lever string) (PolicyValue, error) {
	in, err := r.g.PolicyInputs(ctx, jid, lever, t0)
	if err != nil {
		return PolicyValue{}, err
	}
	if err := CheckLeverApplies(in); err != nil {
		return PolicyValue{}, err
	}
	return ResolvePolicy(in, t0), nil
}

// ---------------------------------------------------------------------------
// SetPolicy.
// ---------------------------------------------------------------------------

func TestSetPolicyRecordsAnAnnouncedChange(t *testing.T) {
	g := newFakeGov()
	g.seat("mayor@"+cityA, mayorA)

	c, err := SetPolicy(context.Background(), govTx{gov: g}, mayorA, cityA, "city.tax_rate", 800, t0)
	if err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if g.locks != 1 {
		t.Errorf("the lever was locked %d times, want 1", g.locks)
	}
	s := c.Setting
	if s.Value != 800 || s.SetByPlayerID != mayorA || s.OfficeID != "mayor@"+cityA || c.OfficeCode != "mayor" {
		t.Errorf("recorded %+v", c)
	}
	if !s.SetAt.Equal(t0) || !s.EffectiveAt.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("set %v effective %v, want now and now + notice", s.SetAt, s.EffectiveAt)
	}
	if c.OldValue != 120 {
		t.Errorf("old value %d, want the default in force, 120", c.OldValue)
	}
}

// While the mayor is vacant the deputy acts; once there is a mayor, only the
// mayor may.
func TestSetPolicyByTheActingDeputy(t *testing.T) {
	g := newFakeGov()
	g.seat("deputy@"+cityA, deputyA)
	if _, err := SetPolicy(context.Background(), govTx{gov: g}, deputyA, cityA, "city.tax_rate", 500, t0); err != nil {
		t.Fatalf("the acting deputy was refused: %v", err)
	}
	if got := g.recorded[0]; got.OfficeCode != "deputy_mayor" {
		t.Errorf("recorded as %s, want deputy_mayor", got.OfficeCode)
	}

	g.seat("mayor@"+cityA, mayorA)
	_, err := SetPolicy(context.Background(), govTx{gov: g}, deputyA, cityA, "city.tax_rate", 600, t0.Add(100*time.Hour))
	if !stderrors.Is(err, ErrNotOfficeHolder) {
		t.Errorf("a deputy with a mayor in office: %v, want ErrNotOfficeHolder", err)
	}
}

// Every refusal writes nothing and names itself.
func TestSetPolicyRefusalsWriteNothing(t *testing.T) {
	later := t0.Add(10 * time.Hour)
	tests := []struct {
		name    string
		arrange func(g *fakeGov)
		player  string
		place   string
		lever   string
		value   int64
		want    error
	}{
		{"not the holder", nil, someone, cityA, "city.tax_rate", 800, ErrNotOfficeHolder},
		{"holder of another city", func(g *fakeGov) { g.seat("mayor@"+cityB, mayorB) },
			mayorB, cityA, "city.tax_rate", 800, ErrNotOfficeHolder},
		{"a city lever asked of a country", nil, mayorA, country, "city.tax_rate", 800, ErrWrongJurisdiction},
		{"above max", nil, mayorA, cityA, "city.tax_rate", 2501, ErrPolicyOutOfBounds},
		{"below min", nil, mayorA, cityA, "city.tax_rate", -1, ErrPolicyOutOfBounds},
		{"within the cooldown", func(g *fakeGov) { g.settings = []PolicySetting{setting(700, t0, 24*time.Hour)} },
			mayorA, cityA, "city.tax_rate", 800, ErrPolicyCooldown},
		{"a lever decided by a vote", func(g *fakeGov) {
			l := taxLever()
			l.Code, l.HeldBy, l.DecisionRule, l.CityDefault = "city.bylaw", "city_council", "majority", ""
			g.levers[l.Code] = l
		}, mayorA, cityA, "city.bylaw", 1, ErrPolicyRequiresVote},
		{"a structured lever", func(g *fakeGov) {
			l := taxLever()
			l.Code, l.Type, l.ValueKind = "city.item_legality", "map", "structured"
			g.levers[l.Code] = l
		}, mayorA, cityA, "city.item_legality", 1, ErrLeverKindUnsupported},
		{"an unknown lever", nil, mayorA, cityA, "city.curfew", 1, ErrUnknownLever},
		{"an unknown place", nil, mayorA, "00000000-0000-4000-8000-000000000999", "city.tax_rate", 800, ErrJurisdictionNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGov()
			g.seat("mayor@"+cityA, mayorA)
			if tc.arrange != nil {
				tc.arrange(g)
			}
			before := len(g.settings)
			_, err := SetPolicy(context.Background(), govTx{gov: g}, tc.player, tc.place, tc.lever, tc.value, later)
			if !stderrors.Is(err, tc.want) {
				t.Fatalf("SetPolicy = %v, want %v", err, tc.want)
			}
			if len(g.recorded) != 0 || len(g.settings) != before {
				t.Errorf("a refused change wrote %d change(s)", len(g.recorded))
			}
		})
	}
}

// The cooldown runs from the last announcement, and lifts exactly at its end.
func TestSetPolicyCooldownLifts(t *testing.T) {
	g := newFakeGov()
	g.seat("mayor@"+cityA, mayorA)
	tx := govTx{gov: g}
	if _, err := SetPolicy(context.Background(), tx, mayorA, cityA, "city.tax_rate", 700, t0); err != nil {
		t.Fatal(err)
	}
	_, err := SetPolicy(context.Background(), tx, mayorA, cityA, "city.tax_rate", 800, t0.Add(72*time.Hour-time.Second))
	if !stderrors.Is(err, ErrPolicyCooldown) {
		t.Fatalf("one second early: %v, want ErrPolicyCooldown", err)
	}
	c, err := SetPolicy(context.Background(), tx, mayorA, cityA, "city.tax_rate", 800, t0.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("at the end of the cooldown: %v", err)
	}
	if c.OldValue != 700 {
		t.Errorf("old value %d, want 700 (in force since its notice passed)", c.OldValue)
	}
}

// ---------------------------------------------------------------------------
// Appointment and vacancy.
// ---------------------------------------------------------------------------

func TestAppointAndVacate(t *testing.T) {
	g := newFakeGov()
	tx := govTx{gov: g}

	before, after, err := AppointToOffice(context.Background(), tx, "president", country, 1, mayorA, t0)
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}
	if !before.Vacant() || after.HolderPlayerID != mayorA || after.AcquiredBy != AcquiredByAppointment {
		t.Errorf("appointed %+v", after)
	}
	if after.TermEndsAt == nil || !after.TermEndsAt.Equal(t0.Add(1344*time.Hour)) {
		t.Errorf("term ends %v, want now + the office's term", after.TermEndsAt)
	}

	// One player may hold several offices when nothing forbids it.
	if _, _, err := AppointToOffice(context.Background(), tx, "mayor", cityA, 1, mayorA, t0); err != nil {
		t.Errorf("a president could not also be a mayor: %v", err)
	}

	_, vacated, err := VacateOffice(context.Background(), tx, "mayor", cityA, 1, t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("vacate: %v", err)
	}
	if !vacated.Vacant() || vacated.AcquiredBy != "" || vacated.TermEndsAt != nil {
		t.Errorf("vacated %+v", vacated)
	}
	if _, _, err := VacateOffice(context.Background(), tx, "mayor", cityA, 1, t0); !stderrors.Is(err, ErrOfficeVacant) {
		t.Errorf("vacating a vacant seat: %v, want ErrOfficeVacant", err)
	}
}

func TestAppointRefusals(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(g *fakeGov)
		office  string
		place   string
		seat    int
		player  string
		want    error
	}{
		{"unknown office", nil, "sheriff", cityA, 1, mayorA, ErrOfficeNotFound},
		{"no such seat", nil, "mayor", cityA, 2, mayorA, ErrOfficeNotFound},
		{"a city office in a country", nil, "mayor", country, 1, mayorA, ErrWrongJurisdiction},
		{"seat held by somebody else", func(g *fakeGov) { g.seat("mayor@"+cityA, mayorB) },
			"mayor", cityA, 1, mayorA, ErrOfficeOccupied},
		{"seat already held by the player", func(g *fakeGov) { g.seat("mayor@"+cityA, mayorA) },
			"mayor", cityA, 1, mayorA, ErrAlreadyHoldsSeat},
		{"another seat of the same body", func(g *fakeGov) { g.seat("council1@"+cityA, mayorA) },
			"city_council", cityA, 2, mayorA, ErrAlreadyHoldsSeat},
		// mayor declares city_council incompatible; the relation binds both ways.
		{"incompatible, declared on the office sought", func(g *fakeGov) { g.seat("council1@"+cityB, mayorA) },
			"mayor", cityA, 1, mayorA, ErrIncompatibleOffices},
		{"incompatible, declared on the office held", func(g *fakeGov) { g.seat("mayor@"+cityB, mayorA) },
			"city_council", cityA, 1, mayorA, ErrIncompatibleOffices},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGov()
			if tc.arrange != nil {
				tc.arrange(g)
			}
			_, _, err := AppointToOffice(context.Background(), govTx{gov: g}, tc.office, tc.place, tc.seat, tc.player, t0)
			if !stderrors.Is(err, tc.want) {
				t.Fatalf("AppointToOffice = %v, want %v", err, tc.want)
			}
			if len(g.assigned) != 0 {
				t.Errorf("a refused appointment wrote %+v", g.assigned)
			}
		})
	}
}

func TestIncompatibleIsSymmetric(t *testing.T) {
	a := OfficeDefinition{Code: "judge", IncompatibleWith: []string{"mayor"}}
	b := OfficeDefinition{Code: "mayor"}
	c := OfficeDefinition{Code: "treasurer"}
	if !incompatible(a, b) || !incompatible(b, a) {
		t.Error("incompatibility declared on one side does not bind both")
	}
	if incompatible(a, c) || incompatible(b, c) {
		t.Error("offices nobody declared incompatible are")
	}
}

// TestAuthorizeWalksTheDeputyChain: an action is the office's holder's, or
// — while the office is vacant — the acting deputy's; nobody else's.
func TestAuthorizeWalksTheDeputyChain(t *testing.T) {
	g := newFakeGov()
	tx := govTx{gov: g}
	ctx := context.Background()
	if _, err := Authorize(ctx, tx, cityA, "mayor", mayorA); !stderrors.Is(err, ErrNotOfficeHolder) {
		t.Errorf("a vacant office and deputy: %v, want ErrNotOfficeHolder", err)
	}
	g.seat("deputy@"+cityA, mayorB)
	seat, err := Authorize(ctx, tx, cityA, "mayor", mayorB)
	if err != nil || seat.OfficeCode != "deputy_mayor" {
		t.Errorf("the deputy acting for a vacant mayor: %+v, %v", seat, err)
	}
	g.seat("mayor@"+cityA, mayorA)
	if _, err := Authorize(ctx, tx, cityA, "mayor", mayorB); !stderrors.Is(err, ErrNotOfficeHolder) {
		t.Errorf("the deputy once the mayor is seated: %v, want ErrNotOfficeHolder", err)
	}
	if seat, err := Authorize(ctx, tx, cityA, "mayor", mayorA); err != nil || seat.OfficeCode != "mayor" {
		t.Errorf("the mayor: %+v, %v", seat, err)
	}
	if _, err := Authorize(ctx, tx, cityB, "mayor", mayorA); !stderrors.Is(err, ErrNotOfficeHolder) {
		t.Errorf("another city's mayor: %v, want ErrNotOfficeHolder", err)
	}
}
