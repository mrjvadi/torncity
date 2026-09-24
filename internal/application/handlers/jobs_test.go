package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The shared fake transaction reaches no study repository; the work tests
// below wrap it with one that does. nil makes an accidental use by any other
// test fail loudly. Its job repository is an empty one: a departure asks it
// whether the player is at work (no job, no shift), and anything that would
// write through it panics on its nil maps.
func (t *fakeTx) Employment() application.EmploymentRepository { return &fakeEmployment{} }
func (t *fakeTx) Education() application.EducationRepository   { return nil }
func (t *fakeTx) Crime() application.CrimeRepository           { return noCrime{} }

// --- fakes -------------------------------------------------------------

type fakeEmployment struct {
	current   map[string]application.Employment
	ended     []application.Employment
	shifts    []application.WorkShift
	residence map[string]string
	// working holds each player's shift in progress; sessions every shift
	// ever started, by id.
	working  map[string]application.ShiftSession
	sessions map[string]application.ShiftSession
}

func (f *fakeEmployment) snapshot() func() {
	current := make(map[string]application.Employment, len(f.current))
	for k, v := range f.current {
		current[k] = v
	}
	residence := make(map[string]string, len(f.residence))
	for k, v := range f.residence {
		residence[k] = v
	}
	ended := append([]application.Employment(nil), f.ended...)
	shifts := append([]application.WorkShift(nil), f.shifts...)
	working := make(map[string]application.ShiftSession, len(f.working))
	for k, v := range f.working {
		working[k] = v
	}
	sessions := make(map[string]application.ShiftSession, len(f.sessions))
	for k, v := range f.sessions {
		sessions[k] = v
	}
	return func() {
		f.current, f.ended, f.shifts, f.residence = current, ended, shifts, residence
		f.working, f.sessions = working, sessions
	}
}

func (f *fakeEmployment) ActiveShift(_ context.Context, playerID string) (*application.ShiftSession, error) {
	s, ok := f.working[playerID]
	if !ok {
		return nil, application.ErrNoShiftInProgress
	}
	return &s, nil
}

func (f *fakeEmployment) StartShift(_ context.Context, s application.ShiftSession) error {
	if f.working == nil {
		f.working = map[string]application.ShiftSession{}
	}
	if f.sessions == nil {
		f.sessions = map[string]application.ShiftSession{}
	}
	if _, ok := f.working[s.PlayerID]; ok {
		return application.ErrShiftInProgress
	}
	s.Status = application.ShiftWorking
	f.working[s.PlayerID] = s
	f.sessions[s.ID] = s
	return nil
}

func (f *fakeEmployment) EndShift(_ context.Context, id, status string, at time.Time) error {
	for k, s := range f.working {
		if s.ID == id {
			delete(f.working, k)
			s.Status, s.CompletedAt = status, &at
			f.sessions[id] = s
			return nil
		}
	}
	return application.ErrNoShiftInProgress
}

func (f *fakeEmployment) Current(_ context.Context, playerID string) (*application.Employment, error) {
	e, ok := f.current[playerID]
	if !ok {
		return nil, application.ErrNotEmployed
	}
	e.RecentShifts = append([]time.Time(nil), e.RecentShifts...)
	return &e, nil
}

func (f *fakeEmployment) Hire(_ context.Context, e application.Employment) error {
	if _, ok := f.current[e.PlayerID]; ok {
		return application.ErrAlreadyEmployed
	}
	f.current[e.PlayerID] = e
	return nil
}

func (f *fakeEmployment) Save(_ context.Context, e application.Employment) error {
	cur, ok := f.current[e.PlayerID]
	if !ok || cur.ID != e.ID {
		return application.ErrNotEmployed
	}
	f.current[e.PlayerID] = e
	return nil
}

func (f *fakeEmployment) End(_ context.Context, id, _ string, _ time.Time) error {
	for k, e := range f.current {
		if e.ID == id {
			delete(f.current, k)
			f.ended = append(f.ended, e)
			return nil
		}
	}
	return application.ErrNotEmployed
}

func (f *fakeEmployment) RecordShift(_ context.Context, s application.WorkShift) error {
	for _, have := range f.shifts {
		if have.ID == s.ID {
			// work_shifts.id is the primary key.
			return stderrors.New("fake: duplicate payroll row " + s.ID)
		}
	}
	f.shifts = append(f.shifts, s)
	return nil
}

func (f *fakeEmployment) ResidenceCityID(_ context.Context, playerID string) (string, error) {
	return f.residence[playerID], nil
}

type fakeEducation struct {
	active    map[string]application.Enrollment
	completed []application.Enrollment
	certs     map[string][]application.Certification
}

func (f *fakeEducation) snapshot() func() {
	active := make(map[string]application.Enrollment, len(f.active))
	for k, v := range f.active {
		active[k] = v
	}
	certs := make(map[string][]application.Certification, len(f.certs))
	for k, v := range f.certs {
		certs[k] = append([]application.Certification(nil), v...)
	}
	completed := append([]application.Enrollment(nil), f.completed...)
	return func() { f.active, f.certs, f.completed = active, certs, completed }
}

func (f *fakeEducation) Active(_ context.Context, playerID string) (*application.Enrollment, error) {
	e, ok := f.active[playerID]
	if !ok {
		return nil, application.ErrNoActiveEnrollment
	}
	return &e, nil
}

func (f *fakeEducation) SeatsTaken(_ context.Context, course string) (int, error) {
	n := 0
	for _, e := range f.active {
		if e.CourseCode == course {
			n++
		}
	}
	return n, nil
}

func (f *fakeEducation) Enroll(_ context.Context, e application.Enrollment) error {
	if _, ok := f.active[e.PlayerID]; ok {
		return application.ErrAlreadyEnrolled
	}
	f.active[e.PlayerID] = e
	return nil
}

func (f *fakeEducation) Complete(_ context.Context, id string, at time.Time) error {
	for k, e := range f.active {
		if e.ID == id {
			delete(f.active, k)
			e.Status = application.EnrollmentCompleted
			e.CompletedAt = &at
			f.completed = append(f.completed, e)
			return nil
		}
	}
	return application.ErrNoActiveEnrollment
}

func (f *fakeEducation) Certify(_ context.Context, playerID, course, _ string, at time.Time) (bool, error) {
	for _, c := range f.certs[playerID] {
		if c.CourseCode == course {
			return false, nil
		}
	}
	f.certs[playerID] = append(f.certs[playerID], application.Certification{CourseCode: course, IssuedAt: at})
	return true, nil
}

func (f *fakeEducation) Certifications(_ context.Context, playerID string) ([]application.Certification, error) {
	return append([]application.Certification(nil), f.certs[playerID]...), nil
}

func (f *fakeEducation) Pause(_ context.Context, playerID string, at time.Time) (bool, error) {
	e, ok := f.active[playerID]
	if !ok || e.PausedAt != nil {
		return false, nil
	}
	e.PausedAt = &at
	f.active[playerID] = e
	return true, nil
}

func (f *fakeEducation) Resume(_ context.Context, id string, startedAt, completesAt time.Time, actionID string) (bool, error) {
	for k, e := range f.active {
		if e.ID == id && e.PausedAt != nil {
			e.StartedAt, e.CompletesAt, e.GameActionID, e.PausedAt = startedAt, completesAt, actionID, nil
			f.active[k] = e
			return true, nil
		}
	}
	return false, nil
}

// fakeWorkLedger keeps balances by account and refuses what the real ledger
// refuses: an unbalanced transaction, an unknown reason, an overdraft.
type fakeWorkLedger struct {
	balances map[string]int64
	posts    []application.LedgerTransaction
}

func (f *fakeWorkLedger) snapshot() func() {
	balances := make(map[string]int64, len(f.balances))
	for k, v := range f.balances {
		balances[k] = v
	}
	posts := append([]application.LedgerTransaction(nil), f.posts...)
	return func() { f.balances, f.posts = balances, posts }
}

func accountID(kind application.AccountKind, owner string) string { return string(kind) + ":" + owner }

func (f *fakeWorkLedger) AccountFor(_ context.Context, kind application.AccountKind, owner string) (application.Account, error) {
	id := accountID(kind, owner)
	return application.Account{ID: id, Kind: kind, OwnerID: owner, Balance: money.FromMinor(f.balances[id])}, nil
}

func (f *fakeWorkLedger) Balance(_ context.Context, id string) (money.Amount, error) {
	return money.FromMinor(f.balances[id]), nil
}

func (f *fakeWorkLedger) Post(_ context.Context, t application.LedgerTransaction) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	for _, e := range t.Entries {
		if e.AccountID != application.SystemSourceAccountID && f.balances[e.AccountID]+e.Amount.Minor() < 0 {
			return "", application.ErrInsufficientFunds
		}
	}
	for _, e := range t.Entries {
		f.balances[e.AccountID] += e.Amount.Minor()
	}
	f.posts = append(f.posts, t)
	return "tx-" + string(rune('a'+len(f.posts))), nil
}

func (f *fakeWorkLedger) RecordGrant(context.Context, application.RewardGrant) (application.RewardGrant, bool, error) {
	return application.RewardGrant{}, false, stderrors.New("not used")
}

// workPolicy answers each lever with its own value, and remembers which
// jurisdictions were asked, so a test can see the policy came through the
// resolver for the right city.
type workPolicy struct {
	values map[string]int64
	asked  []string
}

func (p *workPolicy) Get(_ context.Context, jurisdictionID, lever string) (application.PolicyValue, error) {
	v, ok := p.values[lever]
	if !ok {
		return application.PolicyValue{}, application.ErrUnknownLever
	}
	p.asked = append(p.asked, jurisdictionID+"/"+lever)
	return application.PolicyValue{JurisdictionID: jurisdictionID, Lever: lever, Value: v}, nil
}

type workTx struct {
	*fakeTx
	w *workWorld
}

func (t workTx) Employment() application.EmploymentRepository { return t.w.jobs }
func (t workTx) Education() application.EducationRepository   { return t.w.edu }
func (t workTx) Crime() application.CrimeRepository           { return noCrime{} }
func (t workTx) Ledger() application.LedgerRepository         { return t.w.ledger }

type workWorld struct {
	jobs   *fakeEmployment
	edu    *fakeEducation
	ledger *fakeWorkLedger
}

type workUOW struct {
	tx        *fakeTx
	w         *workWorld
	commits   int
	rollbacks int
}

func (u *workUOW) Do(ctx context.Context, fn func(context.Context, application.Tx) error) error {
	restore := []func(){u.tx.snapshot(), u.w.jobs.snapshot(), u.w.edu.snapshot(), u.w.ledger.snapshot()}
	if err := fn(ctx, workTx{fakeTx: u.tx, w: u.w}); err != nil {
		u.rollbacks++
		for _, r := range restore {
			r()
		}
		return err
	}
	u.commits++
	return nil
}

// --- harness -----------------------------------------------------------

type workHarness struct {
	jobs   *JobsHandler
	edu    *EducationHandler
	uow    *workUOW
	policy *workPolicy
	now    time.Time
	player *application.Player
}

// shippedSnapshot is the content that actually ships, so these tests exercise
// the real careers and courses.
func shippedSnapshot(t *testing.T) *content.Snapshot {
	t.Helper()
	pack, err := content.Load("../../../configs/content")
	if err != nil {
		t.Fatalf("loading shipped content: %v", err)
	}
	snap, err := content.BuildSnapshot(7, pack)
	if err != nil {
		t.Fatalf("building shipped content: %v", err)
	}
	return snap
}

type fixedContent struct{ snap *content.Snapshot }

func (f fixedContent) Current() *content.Snapshot { return f.snap }

const workTelegramID = 4242

// workScale is the game clock these tests run on, the one the game ships
// with: a game hour is a real minute.
const workScale = 60

func newWorkHarness(t *testing.T) *workHarness {
	t.Helper()
	h := &workHarness{now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	h.uow = &workUOW{tx: newFakeTx(), w: &workWorld{
		jobs: &fakeEmployment{current: map[string]application.Employment{}, residence: map[string]string{},
			working: map[string]application.ShiftSession{}, sessions: map[string]application.ShiftSession{}},
		edu:    &fakeEducation{active: map[string]application.Enrollment{}, certs: map[string][]application.Certification{}},
		ledger: &fakeWorkLedger{balances: map[string]int64{}},
	}}
	h.policy = &workPolicy{values: map[string]int64{
		leverMinimumWage: 100, leverIncomeTax: 500, leverShiftWindowHours: 24, leverShiftsBeforeFatigue: 4,
	}}
	clock := func() time.Time { return h.now }
	source := fixedContent{shippedSnapshot(t)}
	cities := newFakeCities()
	h.jobs = NewJobsHandler(h.uow, &seqIDs{}, messages(t), source, cities, h.policy, workScale, DefaultPageSize, testIdempotencyTTL, clock)
	h.edu = NewEducationHandler(h.uow, &seqIDs{}, messages(t), source, cities, workScale, DefaultPageSize, testIdempotencyTTL, clock)

	city := tehranID
	h.player = &application.Player{ID: "p-work", TelegramUserID: workTelegramID, CityID: &city, Language: "en"}
	h.uow.tx.players.byTelegramID[workTelegramID] = h.player
	h.uow.w.jobs.residence[h.player.ID] = tehranID
	h.uow.tx.stats.rows[h.player.ID] = storedStats(application.Stats{PlayerID: h.player.ID, UpdatedAt: h.now}, player.NewStats())
	// Courses are joined at the university quarter, where the player stands.
	h.uow.tx.places.at[h.player.ID] = "university"
	return h
}

func (h *workHarness) meta(requestID, command string) envelope.Metadata {
	m := meta("bot-1", workTelegramID, requestID)
	m.Command = command
	m.Language = "en"
	return m
}

func (h *workHarness) cash() int64 {
	return h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)]
}

func (h *workHarness) outboxSubjects() []string {
	var out []string
	for _, r := range h.uow.tx.outbox.records {
		out = append(out, r.Subject)
	}
	return out
}

func workText(resp *presenter.Response) string {
	if resp == nil {
		return ""
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

// --- work --------------------------------------------------------------

// finishShift delivers the scheduler's job.finish_shift for the player's
// shift in progress, at the moment it ends, as dispatch requestID.
func (h *workHarness) finishShift(t *testing.T, requestID string) (*presenter.Response, error) {
	t.Helper()
	s, ok := h.uow.w.jobs.working[h.player.ID]
	if !ok {
		t.Fatal("no shift in progress to finish")
	}
	if h.now.Before(s.EndsAt) {
		h.now = s.EndsAt
	}
	m := h.meta(requestID, "job.finish_shift")
	m.TelegramUserID = 0
	return h.jobs.FinishShift(context.Background(), m, FinishShiftRequest{
		ActorID: h.player.ID, ReferenceType: "shift_sessions", ReferenceID: s.ID,
	})
}

// Applying, then starting a shift, charges its energy and nothing else: the
// player is at work until the shift ends on the game clock. When the
// scheduler ends it, the wage is paid from the base employer, income tax is
// withheld into the city's treasury at the city's rate, and the XP, skill XP
// and performance land — all in one unit of work.
func TestApplyThenWorkPaysWageAndWithholdsTax(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()

	resp, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(resp.Text, "Sales Trainee") {
		t.Errorf("hired screen = %q, want the position's title", resp.Text)
	}
	emp, ok := h.uow.w.jobs.current[h.player.ID]
	if !ok || emp.CareerCode != "retail" || emp.Tier != 0 || emp.Rate != 120 || emp.CityID != tehranID {
		t.Fatalf("employment = %+v, want retail entry at 120 in tehran", emp)
	}

	resp, err = h.jobs.Work(ctx, h.meta("req-work-1", "job.work"))
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	// The retail entry shift is 4 game hours: 4 real minutes at 60.
	if !strings.Contains(resp.Text, "4m") {
		t.Errorf("shift started screen = %q, want the real 4-minute wait", resp.Text)
	}
	stats := h.uow.tx.stats.rows[h.player.ID]
	if stats.Energy != player.DefaultMaxEnergy-15 || stats.XP != 0 {
		t.Errorf("after the start: energy %d, xp %d; want the energy spent and no xp yet", stats.Energy, stats.XP)
	}
	if h.cash() != 0 || len(h.uow.w.jobs.shifts) != 0 || len(h.uow.w.ledger.posts) != 0 {
		t.Fatal("starting a shift paid something")
	}
	session, ok := h.uow.w.jobs.working[h.player.ID]
	if !ok || session.EndsAt.Sub(session.StartedAt) != 4*time.Minute || session.FatigueBPS != 10_000 {
		t.Fatalf("session = %+v, want 4 real minutes at full output", session)
	}
	action := h.uow.tx.actions.scheduled[0]
	if action.ActionType != application.ShiftActionType || !action.FinishAt.Equal(session.EndsAt) ||
		action.ReferenceID != session.ID {
		t.Errorf("scheduled %+v, want the shift's end at %s", action, session.EndsAt)
	}

	// The job screen shows the shift in progress and no button to start
	// another.
	h.now = h.now.Add(time.Minute)
	status, err := h.jobs.Status(ctx, h.meta("req-status", "job.status"))
	if err != nil {
		t.Fatal(err)
	}
	if text := workText(status); !strings.Contains(text, "3m") || strings.Contains(text, screens.AddrJobWork) {
		t.Errorf("status while working = %q, want 3m left and no work button", text)
	}

	resp, err = h.finishShift(t, "dispatch-1")
	if err != nil {
		t.Fatalf("FinishShift: %v", err)
	}
	if got := h.cash(); got != 114 {
		t.Errorf("cash after one shift = %d, want 120 wage - 6 tax = 114", got)
	}
	treasury := h.uow.w.ledger.balances[accountID(application.AccountCityTreasury, tehranID)]
	if treasury != 6 {
		t.Errorf("treasury = %d, want 6 (5%% of 120)", treasury)
	}
	if len(h.uow.w.ledger.posts) != 2 ||
		h.uow.w.ledger.posts[0].Reason != application.ReasonBaseEmployerSalary ||
		h.uow.w.ledger.posts[1].Reason != application.ReasonIncomeTax {
		t.Fatalf("posts = %+v, want the wage then the tax", h.uow.w.ledger.posts)
	}
	stats = h.uow.tx.stats.rows[h.player.ID]
	if stats.Energy != player.DefaultMaxEnergy-15 {
		t.Errorf("energy = %d, want %d", stats.Energy, player.DefaultMaxEnergy-15)
	}
	if stats.XP != 10 {
		t.Errorf("xp = %d, want 10", stats.XP)
	}
	skills := h.uow.tx.skills.rows[h.player.ID]
	if len(skills) != 1 || skills[0].Code != "management" || skills[0].XP != 15 {
		t.Errorf("skills = %+v, want management +15", skills)
	}
	emp = h.uow.w.jobs.current[h.player.ID]
	if emp.Performance != 51 || emp.ShiftsInTier != 1 || emp.TotalShifts != 1 || emp.TotalEarned != 120 {
		t.Errorf("employment after a shift = %+v", emp)
	}
	if len(emp.RecentShifts) != 1 || !emp.RecentShifts[0].Equal(session.StartedAt) {
		t.Errorf("fatigue history = %v, want the shift's start", emp.RecentShifts)
	}
	if len(h.uow.w.jobs.shifts) != 1 || h.uow.w.jobs.shifts[0].Gross != 120 || h.uow.w.jobs.shifts[0].Tax != 6 ||
		h.uow.w.jobs.shifts[0].ID != session.ID {
		t.Errorf("shifts = %+v, want one payroll row under the session's id", h.uow.w.jobs.shifts)
	}
	if s := h.uow.w.jobs.sessions[session.ID]; s.Status != application.ShiftCompleted {
		t.Errorf("session status = %q, want completed", s.Status)
	}
	want := []string{subjects.Event("job", "hired"), subjects.Event("job", "shift_started"), subjects.Event("job", "shift_worked")}
	if got := h.outboxSubjects(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
	for _, asked := range h.policy.asked {
		if !strings.HasPrefix(asked, "jur-tehran/") {
			t.Errorf("policy asked of %s, want only tehran's jurisdiction", asked)
		}
	}
	if resp == nil || !strings.Contains(resp.Text, "114") {
		t.Errorf("shift notice = %q, want the net pay", workText(resp))
	}
}

// Pressing "work" again while a shift runs — a double press, a second
// device, or simply impatience — starts nothing and charges nothing.
func TestASecondShiftWhileWorkingIsRefused(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.jobs.Work(ctx, h.meta("req-work-1", "job.work")); err != nil {
		t.Fatal(err)
	}
	energy := h.uow.tx.stats.rows[h.player.ID].Energy
	for i, req := range []string{"req-work-2", "req-work-3"} {
		resp, err := h.jobs.Work(ctx, h.meta(req, "job.work"))
		if err != nil || !strings.Contains(resp.Text, "at work") {
			t.Fatalf("Work #%d while working = %q, %v; want the at-work refusal", i+2, workText(resp), err)
		}
	}
	if got := h.uow.tx.stats.rows[h.player.ID].Energy; got != energy {
		t.Errorf("energy = %d, want %d: a refused start charged", got, energy)
	}
	if len(h.uow.w.jobs.sessions) != 1 || len(h.uow.tx.actions.scheduled) != 1 {
		t.Errorf("sessions %d, scheduled %d; want exactly one of each", len(h.uow.w.jobs.sessions), len(h.uow.tx.actions.scheduled))
	}
	// Promotion and leaving wait for the shift too.
	for _, call := range []func() (*presenter.Response, error){
		func() (*presenter.Response, error) { return h.jobs.Promote(ctx, h.meta("req-p", "job.promote")) },
		func() (*presenter.Response, error) {
			return h.jobs.Quit(ctx, h.meta("req-q", "job.quit"), QuitRequest{Confirm: screens.QuitConfirmation})
		},
	} {
		resp, err := call()
		if err != nil || !strings.Contains(resp.Text, "at work") {
			t.Errorf("while working = %q, %v; want the at-work refusal", workText(resp), err)
		}
	}
	if len(h.uow.w.jobs.current) != 1 {
		t.Error("the job ended in the middle of a shift")
	}
}

// A redelivered start is not a second shift, and a redelivered end is not a
// second wage: a shift is paid exactly once.
func TestWorkIsIdempotent(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := h.jobs.Work(ctx, h.meta("req-work", "job.work")); err != nil {
			t.Fatalf("Work #%d: %v", i+1, err)
		}
	}
	if n := len(h.uow.w.jobs.sessions); n != 1 {
		t.Fatalf("sessions = %d, want 1 for one request delivered twice", n)
	}
	session := h.uow.w.jobs.working[h.player.ID]
	if _, err := h.finishShift(t, "dispatch-1"); err != nil {
		t.Fatal(err)
	}
	req := FinishShiftRequest{ActorID: h.player.ID, ReferenceType: "shift_sessions", ReferenceID: session.ID}
	for _, id := range []string{"dispatch-1", "dispatch-2"} {
		m := h.meta(id, "job.finish_shift")
		m.TelegramUserID = 0
		resp, err := h.jobs.FinishShift(ctx, m, req)
		if err != nil || resp != nil {
			t.Fatalf("replayed FinishShift = %v, %v; want nothing", resp, err)
		}
	}
	if n := len(h.uow.w.jobs.shifts); n != 1 {
		t.Fatalf("payroll rows = %d, want 1", n)
	}
	if got := h.cash(); got != 114 {
		t.Errorf("cash = %d, want one shift's pay", got)
	}
}

// The scheduler can lag or the clock can step, but a shift is never settled
// before its end: an early delivery is a retryable fault that pays nothing.
func TestAShiftIsNotPaidEarly(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.jobs.Work(ctx, h.meta("req-work", "job.work")); err != nil {
		t.Fatal(err)
	}
	session := h.uow.w.jobs.working[h.player.ID]
	m := h.meta("dispatch-early", "job.finish_shift")
	m.TelegramUserID = 0
	if _, err := h.jobs.FinishShift(ctx, m, FinishShiftRequest{ActorID: h.player.ID, ReferenceID: session.ID}); err == nil {
		t.Fatal("an early FinishShift succeeded")
	}
	if h.cash() != 0 || len(h.uow.w.jobs.shifts) != 0 || len(h.uow.w.jobs.working) != 1 {
		t.Error("an early end paid or ended the shift")
	}
	// The retry, once the time is up, is not taken for a replay.
	if _, err := h.finishShift(t, "dispatch-early"); err != nil {
		t.Fatal(err)
	}
	if h.cash() != 114 {
		t.Errorf("cash = %d after the retry, want 114", h.cash())
	}
}

// Travel waits for the shift: a player at work cannot leave town.
func TestTravelWhileWorkingIsRefused(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.jobs.Work(ctx, h.meta("req-work", "job.work")); err != nil {
		t.Fatal(err)
	}
	if err := refuseAtWork(ctx, workTx{fakeTx: h.uow.tx, w: h.uow.w}, h.player.ID); !isSentinel(err, application.ErrShiftInProgress) {
		t.Fatalf("refuseAtWork = %v, want ErrShiftInProgress", err)
	}
	if _, err := h.finishShift(t, "dispatch-1"); err != nil {
		t.Fatal(err)
	}
	if err := refuseAtWork(ctx, workTx{fakeTx: h.uow.tx, w: h.uow.w}, h.player.ID); err != nil {
		t.Fatalf("after the shift refuseAtWork = %v, want nil", err)
	}
}

// The minimum wage in force raises a shift's pay above an older, lower rate.
func TestMinimumWageRaisesPay(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	h.policy.values[leverMinimumWage] = 200
	h.policy.values[leverIncomeTax] = 0
	if _, err := h.jobs.Work(ctx, h.meta("req-work", "job.work")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.finishShift(t, "dispatch-1"); err != nil {
		t.Fatal(err)
	}
	if got := h.cash(); got != 200 {
		t.Errorf("cash = %d, want the 200 minimum wage, untaxed", got)
	}
	if len(h.uow.w.ledger.posts) != 1 {
		t.Errorf("posts = %d, want only the wage when there is no tax", len(h.uow.w.ledger.posts))
	}
}

// A player who does not live in the city cannot take a job there (no work
// permits yet), and is told why rather than given an error.
func TestNonResidentIsRefusedWithTheReason(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.w.jobs.residence[h.player.ID] = berlinID
	resp, err := h.jobs.Apply(context.Background(), h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"})
	if err != nil {
		t.Fatalf("Apply = %v, want a refusal screen", err)
	}
	if !strings.Contains(resp.Text, "Tehran") || !strings.Contains(resp.Text, "❌") {
		t.Errorf("refusal = %q, want the residence requirement", resp.Text)
	}
	if len(h.uow.w.jobs.current) != 0 || len(h.uow.tx.outbox.records) != 0 {
		t.Error("a refused application left a job or an event behind")
	}
	// The refusal rolled the reservation back, so the same press is retried
	// for real once the player qualifies.
	h.uow.w.jobs.residence[h.player.ID] = tehranID
	if _, err := h.jobs.Apply(context.Background(), h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	if len(h.uow.w.jobs.current) != 1 {
		t.Error("the retried application was taken for a replay")
	}
}

// A career that needs a certificate lists it; one not offered in the city is
// refused as not offered.
func TestApplyRefusalsNameWhatIsMissing(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	// technology is not offered in tehran (the test city is not in its list).
	resp, err := h.jobs.Apply(ctx, h.meta("req-1", "job.apply"), JobRequest{Role: "technology"})
	if err != nil || !strings.Contains(resp.Text, "not offered") {
		t.Fatalf("Apply(technology) = %q, %v; want not offered", workText(resp), err)
	}
	resp, err = h.jobs.Apply(ctx, h.meta("req-2", "job.apply"), JobRequest{Role: "nonexistent"})
	if err != nil || !strings.Contains(resp.Text, "not offered") {
		t.Fatalf("Apply(nonexistent) = %q, %v; want not offered", workText(resp), err)
	}
}

// A shift the player has no energy for is refused with the numbers, and
// nothing is written.
func TestWorkWithoutEnergyChangesNothing(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	row := h.uow.tx.stats.rows[h.player.ID]
	row.Energy = 5
	h.uow.tx.stats.rows[h.player.ID] = row

	_, err := h.jobs.Work(ctx, h.meta("req-work", "job.work"))
	if !stderrors.Is(err, player.ErrNotEnoughEnergy) {
		t.Fatalf("Work = %v, want ErrNotEnoughEnergy", err)
	}
	if h.cash() != 0 || len(h.uow.w.jobs.sessions) != 0 || len(h.uow.tx.actions.scheduled) != 0 ||
		h.uow.tx.stats.rows[h.player.ID].Energy != 5 {
		t.Error("a refused shift started, scheduled or spent something")
	}
}

// A shift is worked where the job is.
func TestWorkAwayFromTheJobIsRefused(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	elsewhere := berlinID
	h.player.CityID = &elsewhere
	resp, err := h.jobs.Work(ctx, h.meta("req-work", "job.work"))
	if err != nil || !strings.Contains(resp.Text, "Tehran") {
		t.Fatalf("Work away = %q, %v; want the job's city named", workText(resp), err)
	}
	if len(h.uow.w.jobs.sessions) != 0 {
		t.Error("a shift was started away from the job")
	}
}

// A promotion is refused with every missing requirement, and granted once
// they are met, raising the pay to the new position's base.
func TestPromotion(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	resp, err := h.jobs.Promote(ctx, h.meta("req-p1", "job.promote"))
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	for _, want := range []string{"Performance 55", "Level 3", "Management"} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("promotion refusal = %q, want it to mention %q", resp.Text, want)
		}
	}

	emp := h.uow.w.jobs.current[h.player.ID]
	emp.Performance, emp.ShiftsInTier = 60, 6
	emp.TierSince = h.now.Add(-48 * time.Hour)
	h.uow.w.jobs.current[h.player.ID] = emp
	row := h.uow.tx.stats.rows[h.player.ID]
	row.Level = 3
	h.uow.tx.stats.rows[h.player.ID] = row
	h.uow.tx.skills.rows[h.player.ID] = []application.Skill{{PlayerID: h.player.ID, Code: "management", Level: 2, XP: 300}}

	status, err := h.jobs.Status(ctx, h.meta("req-s", "job.status"))
	if err != nil || !strings.Contains(workText(status), AddrJobPromoteData()) {
		t.Fatalf("status = %q, %v; want the promotion button", workText(status), err)
	}
	resp, err = h.jobs.Promote(ctx, h.meta("req-p2", "job.promote"))
	if err != nil || !strings.Contains(resp.Text, "Sales Associate") {
		t.Fatalf("Promote = %q, %v", workText(resp), err)
	}
	emp = h.uow.w.jobs.current[h.player.ID]
	if emp.Tier != 1 || emp.Rate != 190 || emp.ShiftsInTier != 0 || !emp.TierSince.Equal(h.now) {
		t.Errorf("promoted employment = %+v", emp)
	}
}

// AddrJobPromoteData is the promotion button's address, as the screen emits it.
func AddrJobPromoteData() string { return screens.AddrJobPromote }

// Leaving asks first, and only the confirmation ends the job.
func TestQuitAsksFirst(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	resp, err := h.jobs.Quit(ctx, h.meta("req-q1", "job.quit"), QuitRequest{})
	if err != nil || !strings.Contains(workText(resp), "job:quit:yes") {
		t.Fatalf("Quit = %q, %v; want a confirmation", workText(resp), err)
	}
	if len(h.uow.w.jobs.current) != 1 {
		t.Fatal("asking to quit ended the job")
	}
	if _, err := h.jobs.Quit(ctx, h.meta("req-q2", "job.quit"), QuitRequest{Confirm: screens.QuitConfirmation}); err != nil {
		t.Fatal(err)
	}
	if len(h.uow.w.jobs.current) != 0 || len(h.uow.w.jobs.ended) != 1 {
		t.Error("the confirmed quit did not end the job")
	}
}

// The openings list only what the base employer offers in the player's city,
// never a code.
func TestOpeningsListTheCitysCareersByName(t *testing.T) {
	h := newWorkHarness(t)
	resp, err := h.jobs.List(context.Background(), h.meta("req-l", "job.list"), PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "Retail") || strings.Contains(resp.Text, "retail") {
		t.Errorf("openings = %q, want career names and no codes", resp.Text)
	}
	if strings.Contains(workText(resp), "Technology") {
		t.Errorf("openings = %q, list a career not offered here", workText(resp))
	}
}

// --- study -------------------------------------------------------------

// Enrolling charges the fee into the sink and schedules the completion; the
// completion issues the certificate and the skill XP once.
func TestEnrollThenComplete(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 1000

	resp, err := h.edu.Enroll(ctx, h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid", Method: "cash"})
	if err != nil || !strings.Contains(resp.Text, "First Aid") {
		t.Fatalf("Enroll = %q, %v", workText(resp), err)
	}
	if got := h.cash(); got != 400 {
		t.Errorf("cash = %d, want 1000 - 600 fee", got)
	}
	if h.uow.w.ledger.balances[application.SystemSinkAccountID] != 600 {
		t.Error("the fee did not leave into the sink")
	}
	if n := len(h.uow.tx.actions.scheduled); n != 1 {
		t.Fatalf("scheduled = %d actions, want 1", n)
	}
	action := h.uow.tx.actions.scheduled[0]
	// first_aid is a 2-hour course: 2 real minutes on the game clock.
	if action.ActionType != application.EducationActionType || !action.FinishAt.Equal(h.now.Add(2*time.Minute)) {
		t.Errorf("action = %+v, want education due in 2 real minutes", action)
	}
	enrolment := h.uow.w.edu.active[h.player.ID]

	// A second course while one runs is refused, and costs nothing.
	resp, err = h.edu.Enroll(ctx, h.meta("req-e2", "education.enroll"), CourseRequest{Course: "driving_licence", Method: "cash"})
	if err != nil || !strings.Contains(resp.Text, "already studying") {
		t.Fatalf("second Enroll = %q, %v; want already enrolled", workText(resp), err)
	}
	if got := h.cash(); got != 400 {
		t.Errorf("a refused enrolment charged: cash = %d", got)
	}

	h.now = h.now.Add(2 * time.Minute)
	req := CompleteCourseRequest{ActorID: h.player.ID, ReferenceID: enrolment.ID, ReferenceType: "enrollments"}
	sched := h.meta("dispatch-1", "education.complete")
	sched.TelegramUserID = 0
	resp, err = h.edu.Complete(ctx, sched, req)
	if err != nil || resp == nil || !strings.Contains(resp.Text, "First Aid") {
		t.Fatalf("Complete = %q, %v", workText(resp), err)
	}
	certs := h.uow.w.edu.certs[h.player.ID]
	if len(certs) != 1 || certs[0].CourseCode != "first_aid" {
		t.Errorf("certificates = %+v", certs)
	}
	skills := h.uow.tx.skills.rows[h.player.ID]
	if len(skills) != 1 || skills[0].Code != "medicine" || skills[0].XP != 150 || skills[0].Level != 1 {
		t.Errorf("skills = %+v, want medicine 150 xp, level 1", skills)
	}

	// The same due row delivered again does nothing.
	sched.RequestID = "dispatch-2"
	resp, err = h.edu.Complete(ctx, sched, req)
	if err != nil || resp != nil {
		t.Fatalf("second Complete = %v, %v; want nothing", resp, err)
	}
	if len(h.uow.w.edu.certs[h.player.ID]) != 1 || h.uow.tx.skills.rows[h.player.ID][0].XP != 150 {
		t.Error("a repeated completion granted twice")
	}
	subjectsSeen := h.outboxSubjects()
	sort.Strings(subjectsSeen)
	want := []string{subjects.Event("education", "completed"), subjects.Event("education", "enrolled")}
	if strings.Join(subjectsSeen, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", subjectsSeen, want)
	}

	// The certificate now opens a course that required it... and the list no
	// longer offers the certificate already held.
	list, err := h.edu.List(ctx, h.meta("req-list", "education.list"), PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(workText(list), "education:view:first_aid") {
		t.Errorf("the list still offers a held certificate:\n%s", workText(list))
	}
}

// A player who cannot pay the fee either way is told the fee and both
// balances, privately, and is not enrolled.
func TestEnrollWithoutTheFee(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 100
	h.uow.w.ledger.balances[accountID(application.AccountPlayerBank, h.player.ID)] = 250
	resp, err := h.edu.Enroll(context.Background(), h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid", Method: "cash"})
	if err != nil || !strings.Contains(resp.Text, "600") || !strings.Contains(resp.Text, "100") ||
		!strings.Contains(resp.Text, "250") || !resp.Private {
		t.Fatalf("Enroll = %q, %v; want the fee and both balances, privately", workText(resp), err)
	}
	if len(h.uow.w.edu.active) != 0 || len(h.uow.tx.actions.scheduled) != 0 {
		t.Error("an unpaid enrolment was recorded or scheduled")
	}
}

// The owner's case: 105 in cash, the fee in the bank. The price screen
// offers the card alone, with a note, and the card pays.
func TestEnrollByCardWhenCashIsShort(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 105
	h.uow.w.ledger.balances[accountID(application.AccountPlayerBank, h.player.ID)] = 5000

	view, err := h.edu.View(ctx, h.meta("req-v", "education.view"), CourseRequest{Course: "first_aid"})
	if err != nil {
		t.Fatal(err)
	}
	text := workText(view)
	if !strings.Contains(text, "education:enroll:first_aid:card") || strings.Contains(text, "education:enroll:first_aid:cash") {
		t.Fatalf("the price screen should offer the card alone:\n%s", text)
	}
	if !strings.Contains(text, "pay by card") {
		t.Errorf("no note says why cash is not offered:\n%s", text)
	}

	// A press without a method shows the same price screen and charges
	// nothing.
	resp, err := h.edu.Enroll(ctx, h.meta("req-e0", "education.enroll"), CourseRequest{Course: "first_aid"})
	if err != nil || !strings.Contains(workText(resp), "education:enroll:first_aid:card") {
		t.Fatalf("Enroll without a method = %q, %v", workText(resp), err)
	}
	if len(h.uow.w.edu.active) != 0 {
		t.Fatal("a press without a method enrolled")
	}

	resp, err = h.edu.Enroll(ctx, h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid", Method: "card"})
	if err != nil || !strings.Contains(resp.Text, "First Aid") {
		t.Fatalf("Enroll by card = %q, %v", workText(resp), err)
	}
	if h.cash() != 105 {
		t.Errorf("cash = %d, want untouched 105", h.cash())
	}
	if bank := h.uow.w.ledger.balances[accountID(application.AccountPlayerBank, h.player.ID)]; bank != 4400 {
		t.Errorf("bank = %d, want 5000 - 600", bank)
	}
	if h.uow.w.ledger.balances[application.SystemSinkAccountID] != 600 {
		t.Error("the fee did not reach the sink")
	}
}

// Both purses covering the fee offer both buttons; a method the course does
// not take is refused as a forged press.
func TestEnrollOffersBothMethods(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 1000
	h.uow.w.ledger.balances[accountID(application.AccountPlayerBank, h.player.ID)] = 1000
	view, err := h.edu.View(ctx, h.meta("req-v", "education.view"), CourseRequest{Course: "first_aid"})
	if err != nil {
		t.Fatal(err)
	}
	text := workText(view)
	if !strings.Contains(text, "education:enroll:first_aid:card") || !strings.Contains(text, "education:enroll:first_aid:cash") {
		t.Fatalf("both methods should be offered:\n%s", text)
	}
	if _, err := h.edu.Enroll(ctx, h.meta("req-x", "education.enroll"), CourseRequest{Course: "first_aid", Method: "cheque"}); !stderrors.Is(err, application.ErrPaymentNotAccepted) {
		t.Fatalf("a made-up method = %v", err)
	}
}

// A course taught in another city is shown with where it is taught, and
// without a way to enrol here; a course that does not exist is not on offer.
func TestCoursesTaughtElsewhereSayWhere(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	for code, city := range map[string]string{"nursing": "Fenwick Span", "culinary_arts": "Brennhaven"} {
		resp, err := h.edu.View(ctx, h.meta("req-"+code, "education.view"), CourseRequest{Course: code})
		if err != nil || !strings.Contains(resp.Text, city) || strings.Contains(workText(resp), "education:enroll") {
			t.Errorf("View(%s) = %q, %v; want where it is taught and no enrolment", code, workText(resp), err)
		}
	}
	resp, err := h.edu.View(ctx, h.meta("req-x", "education.view"), CourseRequest{Course: "alchemy"})
	if err != nil || !strings.Contains(resp.Text, "not on offer") {
		t.Errorf("View(alchemy) = %q, %v; want not on offer", workText(resp), err)
	}
}

// A course is joined where it is taught: from the city centre, the walk to
// the university quarter is offered instead, and nothing is charged.
func TestEnrolNeedsTheUniversity(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.tx.places.at[h.player.ID] = ""
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 1000
	resp, err := h.edu.Enroll(context.Background(), h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid", Method: "cash"})
	if err != nil || !strings.Contains(workText(resp), "place:go:university") {
		t.Fatalf("Enroll from the centre = %q, %v; want the walk to the university", workText(resp), err)
	}
	if h.cash() != 1000 || len(h.uow.w.edu.active) != 0 {
		t.Error("an enrolment away from the university charged or enrolled")
	}
}

// The completion payload survives the schedule's jsonb as written.
func TestEducationPayloadRoundTrips(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 1000
	if _, err := h.edu.Enroll(context.Background(), h.meta("req-e", "education.enroll"), CourseRequest{Course: "evening_accounting", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	var p EducationActionPayload
	if err := json.Unmarshal(h.uow.tx.actions.scheduled[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.PlayerID != h.player.ID || p.CourseCode != "evening_accounting" || p.EnrollmentID == "" {
		t.Errorf("payload = %+v", p)
	}
}
