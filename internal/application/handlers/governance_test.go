package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The governance handler is exercised against an in-memory world whose
// values are decided by the real application.ResolvePolicy and changed by the
// real application.SetPolicy: the fakes store rows, they decide nothing.

const (
	govCountryID = "j-country"
	govCityID    = "j-ostmarch"
	govMayorTG   = 501
	govDeputyTG  = 502
	govOutsideTG = 503
)

type govWorld struct {
	levers   []application.LeverDefinition
	offices  []application.OfficeDefinition
	places   map[string]application.Jurisdiction
	seats    []application.Office
	settings []application.PolicySetting
	changes  []application.PolicyChangeRecord
	names    map[string]application.PlayerName
	now      func() time.Time
}

func newGovWorld(now func() time.Time) *govWorld {
	return &govWorld{
		now: now,
		levers: []application.LeverDefinition{
			{Code: "city.immigration_fee", Jurisdiction: "city", Type: "money", ValueKind: "scalar",
				Default: 0, Min: 0, Max: 100000, HeldBy: "mayor", DecisionRule: "single",
				ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
			{Code: "city.tax_rate", Jurisdiction: "city", Type: "bps", ValueKind: "scalar",
				Default: 450, Min: 0, Max: 2500, HeldBy: "mayor", DecisionRule: "single",
				ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
			{Code: "country.border_tariff", Jurisdiction: "country", Type: "bps", ValueKind: "scalar",
				Default: 0, Min: 0, Max: 2500, HeldBy: "president", DecisionRule: "single",
				ChangeCooldown: 168 * time.Hour, Notice: 72 * time.Hour},
		},
		offices: []application.OfficeDefinition{
			{Code: "city_council", Jurisdiction: "city", Seats: 5},
			{Code: "deputy_mayor", Jurisdiction: "city", Seats: 1},
			{Code: "mayor", Jurisdiction: "city", Seats: 1, Deputy: "deputy_mayor"},
			{Code: "president", Jurisdiction: "country", Seats: 1},
		},
		places: map[string]application.Jurisdiction{
			govCountryID: {ID: govCountryID, Kind: "country", Code: "default_country", Name: "The Commonwealth"},
			govCityID:    {ID: govCityID, Kind: "city", Code: "ostmarch", Name: "Ostmarch", ParentID: govCountryID},
		},
		seats: []application.Office{
			{ID: "seat-mayor", OfficeCode: "mayor", JurisdictionID: govCityID, Seat: 1},
			{ID: "seat-deputy", OfficeCode: "deputy_mayor", JurisdictionID: govCityID, Seat: 1},
			{ID: "seat-council-1", OfficeCode: "city_council", JurisdictionID: govCityID, Seat: 1},
			{ID: "seat-president", OfficeCode: "president", JurisdictionID: govCountryID, Seat: 1},
		},
		names: map[string]application.PlayerName{
			"p-mayor":  {PublicCode: "M4Y0R11", DisplayName: "Mara"},
			"p-deputy": {PublicCode: "D3PUTY1", DisplayName: "Dara"},
		},
	}
}

func (w *govWorld) seat(id, holder string) {
	for i := range w.seats {
		if w.seats[i].ID == id {
			w.seats[i].HolderPlayerID = holder
			w.seats[i].AcquiredBy = ""
			if holder != "" {
				w.seats[i].AcquiredBy = application.AcquiredByAppointment
			}
		}
	}
}

func (w *govWorld) lever(code string) (application.LeverDefinition, bool) {
	for _, l := range w.levers {
		if l.Code == code {
			return l, true
		}
	}
	return application.LeverDefinition{}, false
}

func (w *govWorld) inputs(jid, code string) (application.PolicyInputs, error) {
	var in application.PolicyInputs
	l, ok := w.lever(code)
	if !ok {
		return in, application.ErrUnknownLever
	}
	j, ok := w.places[jid]
	if !ok {
		return in, application.ErrJurisdictionNotFound
	}
	in.Lever, in.Jurisdiction = l, j
	deputy := map[string]string{}
	for _, o := range w.offices {
		deputy[o.Code] = o.Deputy
	}
	for c := l.HeldBy; c != ""; c = deputy[c] {
		link := application.OfficeLink{OfficeCode: c}
		for _, s := range w.seats {
			if s.OfficeCode == c && s.JurisdictionID == jid {
				link.Seats = append(link.Seats, s)
			}
		}
		in.Chain = append(in.Chain, link)
	}
	for _, s := range w.settings {
		if s.JurisdictionID == jid && s.LeverCode == code {
			in.Settings = append(in.Settings, s)
			if in.LastChangeAt == nil || s.SetAt.After(*in.LastChangeAt) {
				at := s.SetAt
				in.LastChangeAt = &at
			}
		}
	}
	return in, nil
}

// PolicyReader.
func (w *govWorld) Get(_ context.Context, jid, code string) (application.PolicyValue, error) {
	in, err := w.inputs(jid, code)
	if err != nil {
		return application.PolicyValue{}, err
	}
	if err := application.CheckLeverApplies(in); err != nil {
		return application.PolicyValue{}, err
	}
	return application.ResolvePolicy(in, w.now()), nil
}

// GovernanceDirectory.
func (w *govWorld) JurisdictionByCode(_ context.Context, kind, code string) (application.Jurisdiction, error) {
	for _, j := range w.places {
		if j.Kind == kind && j.Code == code {
			return j, nil
		}
	}
	return application.Jurisdiction{}, application.ErrJurisdictionNotFound
}

func (w *govWorld) Ancestry(_ context.Context, id string) ([]application.Jurisdiction, error) {
	var out []application.Jurisdiction
	for j, ok := w.places[id]; ok; j, ok = w.places[j.ParentID] {
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil, application.ErrJurisdictionNotFound
	}
	return out, nil
}

func (w *govWorld) Levers(context.Context) ([]application.LeverDefinition, error) {
	return w.levers, nil
}
func (w *govWorld) Offices(context.Context) ([]application.OfficeDefinition, error) {
	return w.offices, nil
}

func (w *govWorld) Seats(_ context.Context, ids []string) ([]application.Office, error) {
	var out []application.Office
	for _, s := range w.seats {
		for _, id := range ids {
			if s.JurisdictionID == id {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (w *govWorld) SeatsHeldBy(_ context.Context, playerID string) ([]application.Office, error) {
	var out []application.Office
	for _, s := range w.seats {
		if s.HolderPlayerID != "" && s.HolderPlayerID == playerID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (w *govWorld) LastPolicyChange(_ context.Context, jid, code string) (*time.Time, error) {
	in, err := w.inputs(jid, code)
	return in.LastChangeAt, err
}

func (w *govWorld) PolicyHistory(_ context.Context, ids []string, limit, offset int) ([]application.PolicyChangeRecord, int, error) {
	var all []application.PolicyChangeRecord
	for i := len(w.changes) - 1; i >= 0; i-- {
		for _, id := range ids {
			if w.changes[i].JurisdictionID == id {
				all = append(all, w.changes[i])
			}
		}
	}
	end := min(offset+limit, len(all))
	if offset > len(all) {
		offset = len(all)
	}
	return all[offset:end], len(all), nil
}

func (w *govWorld) PlayerNames(_ context.Context, ids []string) (map[string]application.PlayerName, error) {
	out := map[string]application.PlayerName{}
	for _, id := range ids {
		if n, ok := w.names[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

// GovernanceRepository, for SetPolicy.
func (w *govWorld) LockLever(context.Context, string, string) error { return nil }
func (w *govWorld) PolicyInputs(_ context.Context, jid, code string, _ time.Time) (application.PolicyInputs, error) {
	return w.inputs(jid, code)
}

func (w *govWorld) RecordPolicy(_ context.Context, c application.PolicyChange) (application.PolicyChange, error) {
	c.ID = "change-" + string(rune('a'+len(w.changes)))
	c.Setting.ID = "value-" + string(rune('a'+len(w.settings)))
	w.settings = append(w.settings, c.Setting)
	w.changes = append(w.changes, application.PolicyChangeRecord{
		ID: c.ID, JurisdictionID: c.Setting.JurisdictionID, LeverCode: c.Setting.LeverCode, OfficeCode: c.OfficeCode,
		SetByPlayerID: c.Setting.SetByPlayerID, OldValue: c.OldValue, NewValue: c.Setting.Value,
		SetAt: c.Setting.SetAt, EffectiveAt: c.Setting.EffectiveAt,
	})
	return c, nil
}

func (w *govWorld) Jurisdiction(_ context.Context, id string) (application.Jurisdiction, error) {
	return w.places[id], nil
}

func (w *govWorld) OfficeDefinitions(context.Context) ([]application.OfficeDefinition, error) {
	return w.offices, nil
}

func (w *govWorld) Seat(context.Context, string, string, int) (application.Office, error) {
	return application.Office{}, application.ErrOfficeNotFound
}
func (w *govWorld) AssignSeat(context.Context, application.Office) error { return nil }

// govTx is the shared fake transaction with this world behind Governance.
type govTx struct {
	*fakeTx
	world *govWorld
}

func (t govTx) Governance() application.GovernanceRepository { return t.world }

// govUOW rolls back the shared fakes AND the world's policy rows on failure.
type govUOW struct {
	tx    *fakeTx
	world *govWorld
}

func (u *govUOW) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	restore := u.tx.snapshot()
	settings, changes := len(u.world.settings), len(u.world.changes)
	if err := fn(ctx, govTx{fakeTx: u.tx, world: u.world}); err != nil {
		restore()
		u.world.settings, u.world.changes = u.world.settings[:settings], u.world.changes[:changes]
		return err
	}
	return nil
}

type govHarness struct {
	h     *GovernanceHandler
	uow   *govUOW
	world *govWorld
	clock time.Time
}

func newGovHarness(t *testing.T) *govHarness {
	t.Helper()
	g := &govHarness{clock: fixedNow}
	now := func() time.Time { return g.clock }
	g.world = newGovWorld(now)
	g.uow = &govUOW{tx: newFakeTx(), world: g.world}
	cityID := "city-ostmarch"
	for tg, id := range map[int64]string{govMayorTG: "p-mayor", govDeputyTG: "p-deputy", govOutsideTG: "p-out"} {
		g.uow.tx.players.byTelegramID[tg] = &application.Player{ID: id, TelegramUserID: tg, Language: "en", CityID: &cityID}
	}
	cities := &fakeCities{cities: []application.City{{ID: cityID, Code: "ostmarch", Name: "Ostmarch", JurisdictionID: govCityID}}}
	g.h = NewGovernanceHandler(g.uow, messages(t), cities, g.world, g.world,
		GovernanceSteps{FineDivisor: 100, CoarseDivisor: 10}, testPageSize, testIdempotencyTTL, now)
	return g
}

func govMeta(tg int64, requestID, command string) envelope.Metadata {
	m := meta("bot01", tg, requestID)
	m.Command = command
	m.Language = "en"
	return m
}

// govShow renders a handler's answer as text and its buttons, failing on an error.
func govShow(t *testing.T) func(*presenter.Response, error) string {
	t.Helper()
	return func(resp *presenter.Response, err error) string {
		t.Helper()
		return govRendered(t, resp, err)
	}
}

func govRendered(t *testing.T, resp *presenter.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	var b strings.Builder
	b.WriteString(resp.Text)
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, btn := range row {
				b.WriteString("\n[" + btn.Text + "|" + btn.CallbackData + "]")
			}
		}
	}
	return b.String()
}

func TestGovCityShowsOfficesPoliciesAndTheActingDeputy(t *testing.T) {
	g := newGovHarness(t)
	g.world.seat("seat-deputy", "p-deputy")
	ctx := context.Background()

	got := govShow(t)(g.h.City(ctx, govMeta(govOutsideTG, "req-1", "gov.city"), GovCityRequest{}))
	for _, want := range []string{"Ostmarch", "The Commonwealth", "Dara (D3PUTY1)", "Tax rate: 4.5% · default",
		"Mayor: vacant · acting: Deputy mayor Dara (D3PUTY1)", "President: vacant", "gov:history:ostmarch"} {
		if !strings.Contains(got, want) {
			t.Errorf("the city screen lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "gov:office") {
		t.Errorf("a player with no office is offered the office screen:\n%s", got)
	}
}

func TestGovOfficeListsWhatTheHolderCanChange(t *testing.T) {
	g := newGovHarness(t)
	g.world.seat("seat-mayor", "p-mayor")
	ctx := context.Background()

	got := govShow(t)(g.h.Office(ctx, govMeta(govMayorTG, "req-1", "gov.office")))
	for _, want := range []string{"Mayor of Ostmarch", "gov:lever:city.tax_rate:ostmarch", "gov:lever:city.immigration_fee:ostmarch"} {
		if !strings.Contains(got, want) {
			t.Errorf("the office screen lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "border_tariff") {
		t.Errorf("the mayor is offered the president's policy:\n%s", got)
	}

	// With the mayor gone, the deputy acts, and says so.
	g.world.seat("seat-mayor", "")
	g.world.seat("seat-deputy", "p-deputy")
	got = govShow(t)(g.h.Office(ctx, govMeta(govDeputyTG, "req-2", "gov.office")))
	if !strings.Contains(got, "acting for the vacant office of Mayor") || !strings.Contains(got, "gov:lever:city.tax_rate:ostmarch") {
		t.Errorf("the acting deputy's screen:\n%s", got)
	}

	got = govShow(t)(g.h.Office(ctx, govMeta(govOutsideTG, "req-3", "gov.office")))
	if strings.Contains(got, "gov:lever") {
		t.Errorf("a player with no office is offered a change:\n%s", got)
	}
}

func TestGovChangeFlowSetsThroughSetPolicyOnceAndEmitsTheEvent(t *testing.T) {
	g := newGovHarness(t)
	g.world.seat("seat-mayor", "p-mayor")
	ctx := context.Background()
	req := GovLeverRequest{Lever: "city.tax_rate", Place: "ostmarch", Value: "800"}

	got := govShow(t)(g.h.Lever(ctx, govMeta(govMayorTG, "req-1", "gov.lever"), req))
	for _, want := range []string{"New value: 8%", "gov:confirm:city.tax_rate:ostmarch:800", "gov:lever:city.tax_rate:ostmarch:825",
		"Allowed range: 0% to 25%"} {
		if !strings.Contains(got, want) {
			t.Errorf("the policy screen lacks %q:\n%s", want, got)
		}
	}

	got = govShow(t)(g.h.Confirm(ctx, govMeta(govMayorTG, "req-2", "gov.confirm"), req))
	if !strings.Contains(got, "from 4.5% to 8%") || !strings.Contains(got, "gov:set:city.tax_rate:ostmarch:800") {
		t.Errorf("the confirmation:\n%s", got)
	}
	if len(g.world.settings) != 0 {
		t.Fatal("the confirmation wrote a value")
	}

	set := govMeta(govMayorTG, "req-3", "gov.set")
	got = govShow(t)(g.h.Set(ctx, set, req))
	if !strings.Contains(got, "will change from 4.5% to 8% in 24h") {
		t.Errorf("the announcement:\n%s", got)
	}
	if len(g.world.settings) != 1 || g.world.settings[0].Value != 800 || !g.world.settings[0].EffectiveAt.Equal(fixedNow.Add(24*time.Hour)) {
		t.Fatalf("settings: %+v", g.world.settings)
	}
	if n := len(g.uow.tx.outbox.records); n != 1 || g.uow.tx.outbox.records[0].Subject != PolicyChangedSubject {
		t.Fatalf("outbox: %+v", g.uow.tx.outbox.records)
	}

	// A redelivery of the same press changes nothing and shows the policy.
	got = govShow(t)(g.h.Set(ctx, set, req))
	if len(g.world.settings) != 1 || len(g.uow.tx.outbox.records) != 1 {
		t.Errorf("a redelivery wrote again: %d values, %d events", len(g.world.settings), len(g.uow.tx.outbox.records))
	}
	if !strings.Contains(got, "Changes to 8% in 24h") {
		t.Errorf("the redelivery's screen:\n%s", got)
	}

	// The public history names the change and its author.
	got = govShow(t)(g.h.History(ctx, govMeta(govOutsideTG, "req-4", "gov.history"), GovHistoryRequest{City: "ostmarch"}))
	if !strings.Contains(got, "4.5% → 8%") || !strings.Contains(got, "Mara (M4Y0R11)") {
		t.Errorf("the history:\n%s", got)
	}
}

func TestGovRefusalsAreAnswersAndWriteNothing(t *testing.T) {
	g := newGovHarness(t)
	g.world.seat("seat-mayor", "p-mayor")
	ctx := context.Background()
	tax := func(v string) GovLeverRequest {
		return GovLeverRequest{Lever: "city.tax_rate", Place: "ostmarch", Value: v}
	}

	for _, tc := range []struct {
		name string
		tg   int64
		req  GovLeverRequest
		want string
	}{
		{"not the holder", govOutsideTG, tax("800"), "Only the Mayor can change this policy"},
		{"out of range", govMayorTG, tax("2600"), "outside the allowed range of 0% to 25%"},
		{"unknown lever", govMayorTG, GovLeverRequest{Lever: "city.nothing", Place: "ostmarch", Value: "1"}, "no longer exists"},
		{"unknown place", govMayorTG, GovLeverRequest{Lever: "city.tax_rate", Place: "atlantis", Value: "1"}, "could not be found"},
	} {
		got := govShow(t)(g.h.Set(ctx, govMeta(tc.tg, "req-"+tc.name, "gov.set"), tc.req))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %s", tc.name, got)
		}
		if len(g.world.settings) != 0 || len(g.uow.tx.outbox.records) != 0 {
			t.Fatalf("%s: a refusal wrote %d values, %d events", tc.name, len(g.world.settings), len(g.uow.tx.outbox.records))
		}
		if len(g.uow.tx.idem.seen) != 0 {
			t.Errorf("%s: a refusal kept its idempotency key", tc.name)
		}
	}

	// Inside the cooldown: the lever screen offers no change, the
	// confirmation and the change itself are refused with the wait.
	govShow(t)(g.h.Set(ctx, govMeta(govMayorTG, "req-first", "gov.set"), tax("800")))
	g.clock = fixedNow.Add(time.Hour)
	got := govShow(t)(g.h.Lever(ctx, govMeta(govMayorTG, "req-l", "gov.lever"), tax("")))
	if strings.Contains(got, "gov:confirm") || !strings.Contains(got, "again in 2d 23h") {
		t.Errorf("the policy screen in the cooldown:\n%s", got)
	}
	for _, run := range []func() (*presenter.Response, error){
		func() (*presenter.Response, error) {
			return g.h.Confirm(ctx, govMeta(govMayorTG, "req-c", "gov.confirm"), tax("900"))
		},
		func() (*presenter.Response, error) {
			return g.h.Set(ctx, govMeta(govMayorTG, "req-s", "gov.set"), tax("900"))
		},
	} {
		got := govShow(t)(run())
		if !strings.Contains(got, "changed recently. You can change it again in 2d 23h") {
			t.Errorf("a change inside the cooldown:\n%s", got)
		}
	}
	if len(g.world.settings) != 1 || len(g.uow.tx.outbox.records) != 1 {
		t.Errorf("the cooldown refusal wrote: %d values, %d events", len(g.world.settings), len(g.uow.tx.outbox.records))
	}
}

func TestGovCityWithNoCityAndAnUnknownCity(t *testing.T) {
	g := newGovHarness(t)
	g.uow.tx.players.byTelegramID[govOutsideTG].CityID = nil
	ctx := context.Background()
	got := govShow(t)(g.h.City(ctx, govMeta(govOutsideTG, "req-1", "gov.city"), GovCityRequest{}))
	if !strings.Contains(got, "not settled in a city") {
		t.Errorf("no city:\n%s", got)
	}
	if _, err := g.h.City(ctx, govMeta(govOutsideTG, "req-2", "gov.city"), GovCityRequest{City: "atlantis"}); err == nil {
		t.Error("an unknown city is not refused")
	}
}
