package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// The flow snapshots drive the real handlers, end to end, over the in-memory
// fakes, through the main things a player does — home, travel, bank, work,
// study, city hall, friends, settings — and write what each step shows to
// testdata/snapshots/<language>/<flow>.txt. A screen can only render what its
// handler gives it: these files catch a value a handler forgot to fill, which
// the screen snapshots, fed by hand, never could.
//
//	go test ./internal/application/handlers -run Snapshot -update
//
// Each step presses the button the previous screen offered where there is
// one, so the addresses on the buttons are exercised too. Every step is
// linted like the screen snapshots (screentest.Problems).

const flowSnapshotDir = "testdata/snapshots"

// Sample players. Their record ids are real-looking UUIDs, so a screen that
// prints one is caught by the lint.
const (
	flowMeTG     = 9001
	flowFriendTG = 9002
	flowMeID     = "5f1d7c2e-8a41-4c6b-9e3d-2b7a1f0c9d11"
	flowFriendID = "7a2e9b4c-1d53-4f8a-a6e2-3c9b8d7e6f22"
	flowMeCode   = "K7Q2M9A"
	flowFriendC  = "B3C4D5F"
)

var flowNames = map[string][2]string{
	"fa": {"سارا", "کاوه"},
	"en": {"Sara", "Kaveh"},
}

// The flows' world: four shipped cities, three of them joined by road and
// two by air, each city its own jurisdiction under the one country.
var flowCities = []application.City{
	{ID: "city-ostmarch", Code: "ostmarch", Name: "Ostmarch", JurisdictionID: govCityID, CostOfLiving: 900, Population: 9},
	{ID: "city-brennhaven", Code: "brennhaven", Name: "Brennhaven", JurisdictionID: "j-brennhaven", CostOfLiving: 1200, Population: 5},
	{ID: "city-fenwick", Code: "fenwick_span", Name: "Fenwick Span", JurisdictionID: "j-fenwick", CostOfLiving: 700, Population: 3},
	{ID: "city-vantor", Code: "vantor_reach", Name: "Vantor Reach", JurisdictionID: "j-vantor", CostOfLiving: 600, Population: 2},
}

func flowTransport(t *testing.T) *fakeNetwork {
	t.Helper()
	bus, train, flight := testModes()
	roads, err := world.NewRoutes([]world.Edge{
		{From: "ostmarch", To: "brennhaven", Distance: 320},
		{From: "ostmarch", To: "fenwick_span", Distance: 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	air, err := world.NewRoutes([]world.Edge{{From: "ostmarch", To: "brennhaven", Distance: 320}})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeNetwork{modes: []travel.Mode{bus, train, flight},
		routes: map[string]world.Routes{"bus": roads, "train": roads, "flight": air}}
}

func flowRoutes(t *testing.T) world.Routes {
	t.Helper()
	return flowTransport(t).routes["bus"]
}

// flowTx is the shared fake transaction with work, study and governance.
type flowTx struct {
	*fakeTx
	w *flowWorld
}

func (t flowTx) Employment() application.EmploymentRepository { return t.w.jobs }
func (t flowTx) Education() application.EducationRepository   { return t.w.edu }
func (t flowTx) Crime() application.CrimeRepository           { return noCrime{} }
func (t flowTx) Governance() application.GovernanceRepository { return t.w.gov }

type flowWorld struct {
	tx   *fakeTx
	jobs *fakeEmployment
	edu  *fakeEducation
	gov  *govWorld
}

func (w *flowWorld) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	restore := []func(){w.tx.snapshot(), w.jobs.snapshot(), w.edu.snapshot()}
	settings, changes := len(w.gov.settings), len(w.gov.changes)
	if err := fn(ctx, flowTx{fakeTx: w.tx, w: w}); err != nil {
		for _, r := range restore {
			r()
		}
		w.gov.settings, w.gov.changes = w.gov.settings[:settings], w.gov.changes[:changes]
		return err
	}
	return nil
}

// flowLevers are the policies the flows read, with the defaults that ship.
var flowLevers = []application.LeverDefinition{
	{Code: "city.minimum_wage", Jurisdiction: "city", Type: "money", ValueKind: "scalar", Default: 100, Min: 0, Max: 10000,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: "city.income_tax", Jurisdiction: "city", Type: "bps", ValueKind: "scalar", Default: 500, Min: 0, Max: 5000,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: "city.shift_window_hours", Jurisdiction: "city", Type: "int", ValueKind: "scalar", Default: 24, Min: 6, Max: 72,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: "city.shifts_before_fatigue", Jurisdiction: "city", Type: "int", ValueKind: "scalar", Default: 4, Min: 1, Max: 12,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: application.LeverBankWithdrawalFee, Jurisdiction: "city", Type: "bps", ValueKind: "scalar", Default: 150, Min: 0, Max: 1000,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: application.LeverCardTransferFee, Jurisdiction: "city", Type: "bps", ValueKind: "scalar", Default: 100, Min: 0, Max: 1000,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
	{Code: TransitFareLever, Jurisdiction: "city", Type: "bps", ValueKind: "scalar", Default: 10000, Min: 0, Max: 20000,
		HeldBy: "mayor", DecisionRule: "single", ChangeCooldown: 72 * time.Hour, Notice: 24 * time.Hour},
}

// flowGame is every handler over one world, for one language.
type flowGame struct {
	t     *testing.T
	lang  string
	now   time.Time
	w     *flowWorld
	msgs  *i18n.Catalog
	seq   int
	snap  *content.Snapshot
	names [2]string

	profile   *ProfileHandler
	travel    *TravelHandler
	worldMap  *MapHandler
	skills    *SkillsHandler
	social    *SocialHandler
	settings  *SettingsHandler
	bank      *BankHandler
	jobs      *JobsHandler
	education *EducationHandler
	gov       *GovernanceHandler
	places    *PlacesHandler
}

func newFlowGame(t *testing.T, lang string) *flowGame {
	t.Helper()
	g := &flowGame{t: t, lang: lang, now: fixedNow, msgs: messages(t), names: flowNames[lang]}
	clock := func() time.Time { return g.now }

	gov := newGovWorld(clock)
	gov.levers = append(gov.levers, flowLevers...)
	for _, c := range flowCities[1:] {
		gov.places[c.JurisdictionID] = application.Jurisdiction{ID: c.JurisdictionID, Kind: "city", Code: c.Code, Name: c.Name, ParentID: govCountryID}
	}
	gov.names[flowMeID] = application.PlayerName{PublicCode: flowMeCode, DisplayName: g.names[0]}
	gov.seat("seat-mayor", flowMeID)

	g.w = &flowWorld{
		tx:   newFakeTx(),
		jobs: &fakeEmployment{current: map[string]application.Employment{}, residence: map[string]string{}},
		edu:  &fakeEducation{active: map[string]application.Enrollment{}, certs: map[string][]application.Certification{}},
		gov:  gov,
	}
	cities := &fakeCities{cities: flowCities}
	g.snap = shippedSnapshot(t)
	source := fixedContent{g.snap}
	ids := &flowIDs{}

	ostmarch := "city-ostmarch"
	for _, p := range []*application.Player{
		{ID: flowMeID, TelegramUserID: flowMeTG, DisplayName: g.names[0], PublicCode: flowMeCode, Username: "sara",
			Language: lang, CityID: &ostmarch, Status: "active", CreatedAt: fixedNow},
		{ID: flowFriendID, TelegramUserID: flowFriendTG, DisplayName: g.names[1], PublicCode: flowFriendC, Username: "kaveh",
			Language: lang, CityID: &ostmarch, Status: "active", CreatedAt: fixedNow},
	} {
		g.w.tx.players.byTelegramID[p.TelegramUserID] = p
		g.w.jobs.residence[p.ID] = ostmarch
	}
	// The fake ledger opens system_source only; fees are burned into
	// system_sink, which the real ledger always has.
	g.w.tx.ledger.accounts[application.SystemSinkAccountID] = &application.Account{
		ID: application.SystemSinkAccountID, Kind: application.AccountSystemSink}
	g.w.tx.ledger.give(t, application.AccountPlayerCash, flowMeID, 5000)
	g.w.tx.ledger.give(t, application.AccountPlayerCash, flowFriendID, 5000)

	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	search := bankSearch{players: g.w.tx.players}

	g.profile = NewProfileHandler(g.w, ids, g.msgs, cities, testDefaultLanguage, testIdempotencyTTL, clock).WithWork(source, gov)
	g.travel = NewTravelHandler(g.w, ids, g.msgs, cities, flowTransport(t), gov, int(workScale), testArrivalXP, testIdempotencyTTL, clock).
		WithPlaces(source)
	g.places = NewPlacesHandler(g.w, ids, g.msgs, source, cities, workScale, testIdempotencyTTL, clock)
	g.worldMap = NewMapHandler(g.w, g.msgs, cities, g.w.tx.travels, flowRoutes(t), DefaultPageSize, clock)
	g.skills = NewSkillsHandler(g.w, g.msgs, g.w.tx.skills, clock)
	g.social = NewSocialHandler(g.w, ids, g.msgs, search, DefaultPageSize, testIdempotencyTTL, clock)
	g.settings = NewSettingsHandler(g.w, g.msgs, g.msgs, testIdempotencyTTL)
	g.bank = NewBankHandler(g.w, ids, g.msgs, cities, gov, search, limits, testIdempotencyTTL, clock)
	g.jobs = NewJobsHandler(g.w, ids, g.msgs, source, cities, gov, workScale, DefaultPageSize, testIdempotencyTTL, clock)
	g.education = NewEducationHandler(g.w, ids, g.msgs, source, cities, workScale, DefaultPageSize, testIdempotencyTTL, clock)
	g.gov = NewGovernanceHandler(g.w, g.msgs, cities, gov, gov, GovernanceSteps{FineDivisor: 100, CoarseDivisor: 10},
		DefaultPageSize, testIdempotencyTTL, clock)
	return g
}

// flowIDs hands out short, readable ids: they appear in button addresses
// (a one-time token on a bank button), never on screen.
type flowIDs struct{ n int }

func (f *flowIDs) NewID() string {
	f.n++
	return "id" + strings.Repeat("x", f.n%3) + string(rune('a'+f.n%26)) + string(rune('a'+f.n/26%26))
}

// typed is a command the player typed.
func (g *flowGame) typed(tg int64, command string) envelope.Metadata {
	g.seq++
	id := "req-" + strings.ReplaceAll(command, ".", "-") + "-" + string(rune('a'+g.seq%26)) + string(rune('a'+g.seq/26))
	m := meta("bot01", tg, id)
	m.Command = command
	m.Language = g.lang
	m.IdempotencyKey = "update-" + id
	return m
}

// press is a button press on the message the flow is looking at.
func (g *flowGame) press(tg int64, command string) envelope.Metadata {
	return pressed(g.typed(tg, command), 42)
}

// flowBook is one flow's golden file.
type flowBook struct {
	g    *flowGame
	book *screentest.Book
	last *presenter.Response
}

func (g *flowGame) book() *flowBook {
	return &flowBook{g: g, book: screentest.NewBook(g.lang, g.names[0], g.names[1],
		g.msgs.T(g.lang, "language.en", nil), g.msgs.T(g.lang, "language.fa", nil))}
}

// step records what one action showed; call it as b.step(title)(handler
// call). A handler error is what the game service turns into screens.Error,
// so that is what is recorded — and the flow fails, because a step is an
// action that should work.
func (b *flowBook) step(title string) func(*presenter.Response, error) *presenter.Response {
	return b.record(title, false)
}

// refused is step for an action the game must refuse: its error is the
// answer, rendered as the player sees it.
func (b *flowBook) refused(title string) func(*presenter.Response, error) *presenter.Response {
	return b.record(title, true)
}

func (b *flowBook) record(title string, wantError bool) func(*presenter.Response, error) *presenter.Response {
	return func(resp *presenter.Response, err error) *presenter.Response {
		b.g.t.Helper()
		if err != nil {
			resp = screens.Error(screens.Context{Msgs: b.g.msgs, Lang: b.g.lang}, err)
			if !wantError {
				b.g.t.Errorf("[%s] %s: handler error: %v", b.g.lang, title, err)
			}
		}
		if resp == nil {
			b.book.AddText(title, "(nothing is shown)")
			return nil
		}
		b.book.Add(title, resp)
		b.last = resp
		return resp
	}
}

// button finds the address of the button on resp whose address starts with
// prefix, so a flow presses what the screen offered.
func (b *flowBook) button(resp *presenter.Response, prefix string) []string {
	b.g.t.Helper()
	if resp != nil && resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, btn := range row {
				if strings.HasPrefix(btn.CallbackData, prefix) {
					return strings.Split(btn.CallbackData, ":")
				}
			}
		}
	}
	// The flow carries on, so the golden file still shows where it went.
	b.g.t.Errorf("[%s] no button %q on:\n%s", b.g.lang, prefix, screentest.Transcript(resp))
	return make([]string, 8)
}

func (b *flowBook) check(name string) {
	b.g.t.Helper()
	b.book.Check(b.g.t, flowSnapshotDir, name)
}

func TestFlowSnapshots(t *testing.T) {
	for _, lang := range messages(t).Languages() {
		if _, ok := flowNames[lang]; !ok {
			t.Fatalf("no sample names for the %s locale", lang)
		}
		for name, flow := range map[string]func(*flowGame) *flowBook{
			"flow_home":       homeFlow,
			"flow_travel":     travelFlow,
			"flow_bank":       bankFlow,
			"flow_jobs":       jobsFlow,
			"flow_education":  educationFlow,
			"flow_governance": governanceFlow,
			"flow_social":     socialFlow,
			"flow_places":     placesFlow,
		} {
			t.Run(lang+"/"+name, func(t *testing.T) {
				flow(newFlowGame(t, lang)).check(name)
			})
		}
	}
}

func homeFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	b.step("/start")(g.profile.Handle(ctx, g.typed(flowMeTG, "player.profile.get")))
	b.step("Skills")(g.skills.List(ctx, g.press(flowMeTG, "skills.list")))
	b.step("Settings")(g.settings.Show(ctx, g.press(flowMeTG, "player.settings")))
	b.step("Refresh the profile")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	return b
}

func travelFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	b.step("Map")(g.places.Map(ctx, g.press(flowMeTG, "map.list")))
	b.step("Other cities")(g.worldMap.List(ctx, g.press(flowMeTG, "map.cities"), PageRequest{Page: "1"}))
	opts := b.step("Choose Brennhaven")(g.travel.Options(ctx, g.press(flowMeTG, "travel.options"), TravelOptionsRequest{City: "brennhaven"}))
	train := b.button(opts, screens.AddrTravelStart+":brennhaven:train:")
	// The train leaves from the station, not the city centre.
	away := b.step("Take the train from the city centre")(g.travel.Start(ctx, g.press(flowMeTG, "travel.start"),
		StartTravelRequest{City: train[2], Mode: train[3], Max: train[4]}))
	walk(g, b, away, "Walk to the station")
	checkout := b.step("Take the train")(g.travel.Start(ctx, g.press(flowMeTG, "travel.start"),
		StartTravelRequest{City: train[2], Mode: train[3], Max: train[4]}))
	pay := b.button(checkout, screens.AddrTravelStart+":brennhaven:train:")
	b.step("Pay the fare in cash")(g.travel.Start(ctx, g.press(flowMeTG, "travel.start"),
		StartTravelRequest{City: pay[2], Mode: pay[3], Max: pay[4], Method: pay[5]}))
	g.now = g.now.Add(time.Minute)
	b.step("My journey")(g.travel.Status(ctx, g.press(flowMeTG, "travel.status")))
	b.step("Profile on the road")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	b.step("Map on the road")(g.worldMap.List(ctx, g.press(flowMeTG, "map.list"), PageRequest{Page: "1"}))
	b.step("Bank on the road")(g.bank.Show(ctx, g.press(flowMeTG, "bank.show")))

	active := g.w.tx.travels.active[flowMeID]
	g.now = active.ArrivesAt.Add(time.Second)
	sched := scheduled("travel.arrive", "dispatch-1")
	sched.Language = g.lang
	b.step("Arrival (scheduler)")(g.travel.Complete(ctx, sched, arrival(flowMeID, active.ID)))
	// The travel repository moves the player in the same statement; the fake
	// only records it.
	moved := g.w.tx.travels.moved[flowMeID]
	g.w.tx.players.byTelegramID[flowMeTG].CityID = &moved
	b.step("Profile in Brennhaven")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	b.step("Map of Brennhaven, off the train")(g.places.Map(ctx, g.press(flowMeTG, "map.list")))

	// A trip the player cannot pay for.
	opts = b.step("Back to Ostmarch")(g.travel.Options(ctx, g.press(flowMeTG, "travel.options"), TravelOptionsRequest{City: "ostmarch"}))
	flight := b.button(opts, screens.AddrTravelStart+":ostmarch:flight:")
	cash := g.w.tx.ledger.balance(application.AccountPlayerCash, flowMeID)
	moveCash(g, flowMeID, cash-10)
	b.step("Fly with too little money")(g.travel.Start(ctx, g.press(flowMeTG, "travel.start"),
		StartTravelRequest{City: flight[2], Mode: flight[3], Max: flight[4]}))
	b.step("Pay the flight in cash anyway")(g.travel.Start(ctx, g.press(flowMeTG, "travel.start"),
		StartTravelRequest{City: flight[2], Mode: flight[3], Max: flight[4], Method: "cash"}))
	b.refused("Same city")(g.travel.Options(ctx, g.press(flowMeTG, "travel.options"), TravelOptionsRequest{City: "brennhaven"}))
	b.refused("Unreachable city")(g.travel.Options(ctx, g.press(flowMeTG, "travel.options"), TravelOptionsRequest{City: "vantor_reach"}))
	return b
}

// moveCash takes amount out of a player's cash, as spending it elsewhere would.
func moveCash(g *flowGame, playerID string, amount int64) {
	g.t.Helper()
	if amount <= 0 {
		return
	}
	g.w.tx.ledger.give(g.t, application.AccountPlayerCash, playerID, -amount)
}

func bankFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	show := b.step("/bank")(g.bank.Show(ctx, g.typed(flowMeTG, "bank.show")))
	dep := b.button(show, screens.AddrDeposit+":")
	show = b.step("Deposit")(g.bank.Deposit(ctx, g.press(flowMeTG, "bank.deposit"), BankAmountRequest{Amount: dep[2], Nonce: dep[3]}))
	wd := b.button(show, screens.AddrWithdraw+":")
	b.step("Withdraw")(g.bank.Withdraw(ctx, g.press(flowMeTG, "bank.withdraw"), BankAmountRequest{Amount: wd[2], Nonce: wd[3]}))
	b.refused("/bank deposit 999999 (too much)")(g.bank.Deposit(ctx, g.typed(flowMeTG, "bank.deposit"), BankAmountRequest{Amount: "999999"}))
	b.refused("/bank deposit abc")(g.bank.Deposit(ctx, g.typed(flowMeTG, "bank.deposit"), BankAmountRequest{Amount: "abc"}))
	b.step("/pay")(g.bank.Pay(ctx, g.typed(flowMeTG, "bank.pay"), PayRequest{}))
	pay := b.step("/pay " + flowFriendC)(g.bank.Pay(ctx, g.typed(flowMeTG, "bank.pay"), PayRequest{To: flowFriendC}))
	card := b.button(pay, screens.AddrPay+":"+flowFriendC+":")
	confirm := b.step("Choose an amount")(g.bank.Pay(ctx, g.press(flowMeTG, "bank.pay"),
		PayRequest{To: card[2], Amount: card[3], Method: card[4]}))
	send := b.button(confirm, screens.AddrPaySend+":")
	b.step("Confirm")(g.bank.PaySend(ctx, g.press(flowMeTG, "bank.pay.send"),
		PayRequest{To: send[2], Amount: send[3], Method: send[4], Nonce: send[5]}))
	b.step("/pay " + flowFriendC + " 900000 card (too much)")(g.bank.Pay(ctx, g.typed(flowMeTG, "bank.pay"),
		PayRequest{To: flowFriendC, Amount: "900000", Method: "card"}))
	b.refused("/pay " + flowMeCode + " (yourself)")(g.bank.Pay(ctx, g.typed(flowMeTG, "bank.pay"), PayRequest{To: flowMeCode}))
	b.refused("/pay ZZZZZZZ (nobody)")(g.bank.Pay(ctx, g.typed(flowMeTG, "bank.pay"), PayRequest{To: "ZZZZZZZ"}))
	return b
}

func jobsFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	b.step("/job (no job yet)")(g.jobs.Status(ctx, g.typed(flowMeTG, "job.status")))
	list := b.step("Job openings")(g.jobs.List(ctx, g.press(flowMeTG, "job.list"), PageRequest{Page: "1"}))
	view := b.button(list, screens.AddrJobView+":retail")
	detail := b.step("Retail")(g.jobs.View(ctx, g.press(flowMeTG, "job.view"), JobRequest{Role: view[2]}))
	apply := b.button(detail, screens.AddrJobApply+":")
	b.step("Apply")(g.jobs.Apply(ctx, g.press(flowMeTG, "job.apply"), JobRequest{Role: apply[2]}))
	b.step("Profile with a job")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	b.step("My job")(g.jobs.Status(ctx, g.press(flowMeTG, "job.status")))
	b.step("Start a shift")(g.jobs.Work(ctx, g.press(flowMeTG, "job.work")))
	g.now = g.now.Add(time.Minute)
	b.step("Profile during the shift")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	b.step("My job during the shift")(g.jobs.Status(ctx, g.press(flowMeTG, "job.status")))
	b.step("Start another shift while at work")(g.jobs.Work(ctx, g.press(flowMeTG, "job.work")))
	b.step("Shift finished (scheduler)")(finishShift(g))
	b.step("My job after the shift")(g.jobs.Status(ctx, g.press(flowMeTG, "job.status")))
	b.step("Ask for a promotion too early")(g.jobs.Promote(ctx, g.press(flowMeTG, "job.promote")))
	b.step("Openings while employed")(g.jobs.List(ctx, g.press(flowMeTG, "job.list"), PageRequest{Page: "1"}))
	b.step("A job needing more")(g.jobs.View(ctx, g.press(flowMeTG, "job.view"), JobRequest{Role: "technology"}))
	b.step("Apply while employed")(g.jobs.Apply(ctx, g.press(flowMeTG, "job.apply"), JobRequest{Role: "logistics"}))
	b.step("Quit")(g.jobs.Quit(ctx, g.press(flowMeTG, "job.quit"), QuitRequest{}))
	b.step("Quit, confirmed")(g.jobs.Quit(ctx, g.press(flowMeTG, "job.quit"), QuitRequest{Confirm: screens.QuitConfirmation}))
	b.step("Work with no job")(g.jobs.Work(ctx, g.press(flowMeTG, "job.work")))
	b.step("Apply for what you do not qualify for")(g.jobs.Apply(ctx, g.press(flowMeTG, "job.apply"), JobRequest{Role: "technology"}))
	return b
}

// finishShift ends the shift in progress the way the scheduler does, with
// the clock at its end.
func finishShift(g *flowGame) (*presenter.Response, error) {
	g.t.Helper()
	s, ok := g.w.jobs.working[flowMeID]
	if !ok {
		g.t.Fatal("no shift in progress to finish")
	}
	if g.now.Before(s.EndsAt) {
		g.now = s.EndsAt
	}
	m := g.typed(0, "job.finish_shift")
	m.TelegramUserID = 0
	return g.jobs.FinishShift(context.Background(), m, FinishShiftRequest{
		ActorID: flowMeID, ReferenceType: "shift_sessions", ReferenceID: s.ID,
	})
}

func educationFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	list := b.step("/study")(g.education.List(ctx, g.typed(flowMeTG, "education.list"), PageRequest{}))
	view := b.button(list, screens.AddrCourseView+":first_aid")
	detail := b.step("First Aid")(g.education.View(ctx, g.press(flowMeTG, "education.view"), CourseRequest{Course: view[2]}))
	enrol := b.button(detail, screens.AddrCourseEnrol+":")
	away := b.step("Enrol from the city centre")(g.education.Enroll(ctx, g.press(flowMeTG, "education.enroll"),
		CourseRequest{Course: enrol[2], Method: enrol[3]}))
	walk(g, b, away, "Walk to the training centre")
	b.step("Enrol")(g.education.Enroll(ctx, g.press(flowMeTG, "education.enroll"), CourseRequest{Course: enrol[2], Method: enrol[3]}))
	g.now = g.now.Add(30 * time.Minute)
	b.step("Studying")(g.education.List(ctx, g.press(flowMeTG, "education.list"), PageRequest{Page: "1"}))
	b.step("Profile while studying")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))
	b.step("Enrol in a second course")(g.education.Enroll(ctx, g.press(flowMeTG, "education.enroll"), CourseRequest{Course: "bookkeeping", Method: "cash"}))
	b.step("A course taught elsewhere")(g.education.View(ctx, g.press(flowMeTG, "education.view"), CourseRequest{Course: "nursing"}))

	enrolment := g.w.edu.active[flowMeID]
	g.now = enrolment.CompletesAt.Add(time.Second)
	sched := g.typed(0, "education.complete")
	sched.TelegramUserID = 0
	b.step("Course finished (scheduler)")(g.education.Complete(ctx, sched,
		CompleteCourseRequest{ActorID: flowMeID, ReferenceID: enrolment.ID, ReferenceType: "enrollments"}))
	b.step("Study after the course")(g.education.List(ctx, g.press(flowMeTG, "education.list"), PageRequest{Page: "1"}))
	b.step("Profile after the course")(g.profile.Handle(ctx, g.press(flowMeTG, "player.profile.get")))

	moveCash(g, flowMeID, g.w.tx.ledger.balance(application.AccountPlayerCash, flowMeID)-100)
	b.step("A course price with too little cash")(g.education.View(ctx, g.press(flowMeTG, "education.view"), CourseRequest{Course: "bookkeeping"}))
	b.step("Enrol without the fee")(g.education.Enroll(ctx, g.press(flowMeTG, "education.enroll"), CourseRequest{Course: "bookkeeping", Method: "cash"}))
	b.step("A course that does not exist")(g.education.View(ctx, g.press(flowMeTG, "education.view"), CourseRequest{Course: "alchemy"}))
	return b
}

// walk presses the walk a NotHere screen offers and lets the scheduler end it.
func walk(g *flowGame, b *flowBook, from *presenter.Response, title string) {
	ctx := context.Background()
	to := b.button(from, screens.AddrPlaceGo+":")
	b.step(title)(g.places.Go(ctx, g.press(flowMeTG, "place.go"), PlaceRequest{Place: to[2]}))
	arrive(g, flowMeID)
}

// arrive ends a player's walk as the scheduler would.
func arrive(g *flowGame, playerID string) {
	g.t.Helper()
	m, ok := g.w.tx.places.moving[playerID]
	if !ok {
		g.t.Errorf("[%s] %s is not walking", g.lang, playerID)
		return
	}
	g.now = m.ArrivesAt.Add(time.Second)
	sched := g.typed(0, "place.arrive")
	sched.TelegramUserID = 0
	if _, err := g.places.Arrive(context.Background(), sched, PlaceScheduledRequest{ActorID: playerID, ReferenceID: m.ID}); err != nil {
		g.t.Errorf("[%s] arrival: %v", g.lang, err)
	}
}

// placesFlow walks around the player's own city.
func placesFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	g.w.tx.places.at[flowFriendID] = "bazaar"
	city := b.step("/map")(g.places.Map(ctx, g.typed(flowMeTG, "map.list")))
	bazaar := b.button(city, screens.AddrPlaceGo+":bazaar")
	b.step("Walk to the bazaar")(g.places.Go(ctx, g.press(flowMeTG, "place.go"), PlaceRequest{Place: bazaar[2]}))
	g.now = g.now.Add(5 * time.Second)
	b.step("The map on the way")(g.places.Map(ctx, g.press(flowMeTG, "map.list")))
	b.step("Walk somewhere else on the way")(g.places.Go(ctx, g.press(flowMeTG, "place.go"), PlaceRequest{Place: "park"}))
	arrive(g, flowMeID)
	b.step("At the bazaar")(g.places.Map(ctx, g.press(flowMeTG, "map.list")))
	b.step("Walk to where you stand")(g.places.Go(ctx, g.press(flowMeTG, "place.go"), PlaceRequest{Place: "bazaar"}))
	b.refused("A place this city does not have")(g.places.Go(ctx, g.press(flowMeTG, "place.go"), PlaceRequest{Place: "airport"}))
	return b
}

func governanceFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	b.step("/city")(g.gov.City(ctx, g.typed(flowMeTG, "gov.city"), GovCityRequest{}))
	office := b.step("My office")(g.gov.Office(ctx, g.press(flowMeTG, "gov.office")))
	lever := b.button(office, screens.AddrGovLever+":city.tax_rate:")
	edit := b.step("Tax rate")(g.gov.Lever(ctx, g.press(flowMeTG, "gov.lever"), GovLeverRequest{Lever: lever[2], Place: lever[3]}))
	up := b.button(edit, screens.AddrGovLever+":city.tax_rate:ostmarch:")
	edit = b.step("Lower it")(g.gov.Lever(ctx, g.press(flowMeTG, "gov.lever"), GovLeverRequest{Lever: up[2], Place: up[3], Value: up[4]}))
	review := b.button(edit, screens.AddrGovConfirm+":")
	confirm := b.step("Review")(g.gov.Confirm(ctx, g.press(flowMeTG, "gov.confirm"), GovLeverRequest{Lever: review[2], Place: review[3], Value: review[4]}))
	set := b.button(confirm, screens.AddrGovSet+":")
	b.step("Confirm")(g.gov.Set(ctx, g.press(flowMeTG, "gov.set"), GovLeverRequest{Lever: set[2], Place: set[3], Value: set[4]}))
	b.step("Change it again at once")(g.gov.Lever(ctx, g.press(flowMeTG, "gov.lever"), GovLeverRequest{Lever: set[2], Place: set[3]}))
	b.step("City hall after the change")(g.gov.City(ctx, g.press(flowMeTG, "gov.city"), GovCityRequest{City: "ostmarch"}))
	b.step("Policy history")(g.gov.History(ctx, g.press(flowMeTG, "gov.history"), GovHistoryRequest{City: "ostmarch"}))
	b.step("A friend's office (none)")(g.gov.Office(ctx, g.typed(flowFriendTG, "gov.office")))
	b.step("A friend tries to change the tax")(g.gov.Set(ctx, g.press(flowFriendTG, "gov.set"),
		GovLeverRequest{Lever: "city.tax_rate", Place: "ostmarch", Value: "900"}))
	return b
}

func socialFlow(g *flowGame) *flowBook {
	ctx := context.Background()
	b := g.book()
	b.step("/social")(g.social.FriendList(ctx, g.typed(flowMeTG, "social.friend.list"), PageRequest{}))
	b.step("/find")(g.social.Search(ctx, g.typed(flowMeTG, "social.search"), SearchRequest{}))
	found := b.step("/find " + flowFriendC)(g.social.Search(ctx, g.typed(flowMeTG, "social.search"), SearchRequest{Query: flowFriendC}))
	add := b.button(found, screens.AddrFriendAdd+":")
	b.step("Add friend")(g.social.FriendAdd(ctx, g.press(flowMeTG, "social.friend.add"), FriendRequest{Player: add[2]}))
	// The request shows on both lists: sent, waiting for an answer, on the
	// requester's; received, with the accept button, on the other's.
	b.step("My list: the request I sent")(g.social.FriendList(ctx, g.typed(flowMeTG, "social.friend.list"), PageRequest{}))
	list := b.step("The friend's list")(g.social.FriendList(ctx, g.typed(flowFriendTG, "social.friend.list"), PageRequest{}))
	accept := b.button(list, screens.AddrFriendAccept+":")
	b.step("The friend accepts")(g.social.FriendAccept(ctx, g.press(flowFriendTG, "social.friend.accept"), FriendRequest{Player: accept[2]}))
	b.step("Friends")(g.social.FriendList(ctx, g.press(flowMeTG, "social.friend.list"), PageRequest{}))
	b.step("/find " + flowMeCode + " (yourself)")(g.social.Search(ctx, g.typed(flowMeTG, "social.search"), SearchRequest{Query: flowMeCode}))
	b.step("/find ZZZZZZZ")(g.social.Search(ctx, g.typed(flowMeTG, "social.search"), SearchRequest{Query: "ZZZZZZZ"}))
	b.refused("Add them again")(g.social.FriendAdd(ctx, g.press(flowMeTG, "social.friend.add"), FriendRequest{Player: add[2]}))
	return b
}
