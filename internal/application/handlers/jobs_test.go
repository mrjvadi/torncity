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

// The shared fake transaction reaches no job or study repository; the work
// tests below wrap it with one that does. nil makes an accidental use by any
// other test fail loudly.
func (t *fakeTx) Employment() application.EmploymentRepository { return nil }
func (t *fakeTx) Education() application.EducationRepository   { return nil }

// --- fakes -------------------------------------------------------------

type fakeEmployment struct {
	current   map[string]application.Employment
	ended     []application.Employment
	shifts    []application.WorkShift
	residence map[string]string
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
	return func() { f.current, f.ended, f.shifts, f.residence = current, ended, shifts, residence }
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

func newWorkHarness(t *testing.T) *workHarness {
	t.Helper()
	h := &workHarness{now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	h.uow = &workUOW{tx: newFakeTx(), w: &workWorld{
		jobs:   &fakeEmployment{current: map[string]application.Employment{}, residence: map[string]string{}},
		edu:    &fakeEducation{active: map[string]application.Enrollment{}, certs: map[string][]application.Certification{}},
		ledger: &fakeWorkLedger{balances: map[string]int64{}},
	}}
	h.policy = &workPolicy{values: map[string]int64{
		leverMinimumWage: 100, leverIncomeTax: 500, leverShiftWindowHours: 24, leverShiftsBeforeFatigue: 4,
	}}
	clock := func() time.Time { return h.now }
	source := fixedContent{shippedSnapshot(t)}
	cities := newFakeCities()
	h.jobs = NewJobsHandler(h.uow, &seqIDs{}, messages(t), source, cities, h.policy, DefaultPageSize, testIdempotencyTTL, clock)
	h.edu = NewEducationHandler(h.uow, &seqIDs{}, messages(t), source, cities, DefaultPageSize, testIdempotencyTTL, clock)

	city := tehranID
	h.player = &application.Player{ID: "p-work", TelegramUserID: workTelegramID, CityID: &city, Language: "en"}
	h.uow.tx.players.byTelegramID[workTelegramID] = h.player
	h.uow.w.jobs.residence[h.player.ID] = tehranID
	h.uow.tx.stats.rows[h.player.ID] = storedStats(application.Stats{PlayerID: h.player.ID, UpdatedAt: h.now}, player.NewStats())
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

// Applying, then working a shift, pays the wage from the base employer,
// withholds income tax into the city's treasury at the city's rate, spends
// the shift's energy, and records all of it — the whole of a shift in one
// unit of work.
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
	stats := h.uow.tx.stats.rows[h.player.ID]
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
	if len(h.uow.w.jobs.shifts) != 1 || h.uow.w.jobs.shifts[0].Gross != 120 || h.uow.w.jobs.shifts[0].Tax != 6 {
		t.Errorf("shifts = %+v", h.uow.w.jobs.shifts)
	}
	want := []string{subjects.Event("job", "hired"), subjects.Event("job", "shift_worked")}
	if got := h.outboxSubjects(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
	for _, asked := range h.policy.asked {
		if !strings.HasPrefix(asked, "jur-tehran/") {
			t.Errorf("policy asked of %s, want only tehran's jurisdiction", asked)
		}
	}
	if !strings.Contains(resp.Text, "114") {
		t.Errorf("shift screen = %q, want the net pay", resp.Text)
	}
}

// A redelivered shift is not worked twice.
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
	if n := len(h.uow.w.jobs.shifts); n != 1 {
		t.Fatalf("shifts = %d, want 1 for one request delivered twice", n)
	}
	if got := h.cash(); got != 114 {
		t.Errorf("cash = %d, want one shift's pay", got)
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
	if h.cash() != 0 || len(h.uow.w.jobs.shifts) != 0 || h.uow.tx.stats.rows[h.player.ID].Energy != 5 {
		t.Error("a refused shift paid, recorded or spent something")
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
	if len(h.uow.w.jobs.shifts) != 0 {
		t.Error("a shift was worked away from the job")
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

	resp, err := h.edu.Enroll(ctx, h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid"})
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
	if action.ActionType != application.EducationActionType || !action.FinishAt.Equal(h.now.Add(2*time.Hour)) {
		t.Errorf("action = %+v, want education due in 2h", action)
	}
	enrolment := h.uow.w.edu.active[h.player.ID]

	// A second course while one runs is refused, and costs nothing.
	resp, err = h.edu.Enroll(ctx, h.meta("req-e2", "education.enroll"), CourseRequest{Course: "driving_licence"})
	if err != nil || !strings.Contains(resp.Text, "already studying") {
		t.Fatalf("second Enroll = %q, %v; want already enrolled", workText(resp), err)
	}
	if got := h.cash(); got != 400 {
		t.Errorf("a refused enrolment charged: cash = %d", got)
	}

	h.now = h.now.Add(2 * time.Hour)
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

// A player who cannot pay the fee is told the fee and their cash, and is not
// enrolled.
func TestEnrollWithoutTheFee(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 100
	resp, err := h.edu.Enroll(context.Background(), h.meta("req-e", "education.enroll"), CourseRequest{Course: "first_aid"})
	if err != nil || !strings.Contains(resp.Text, "600") || !strings.Contains(resp.Text, "100") {
		t.Fatalf("Enroll = %q, %v; want the fee and the cash", workText(resp), err)
	}
	if len(h.uow.w.edu.active) != 0 || len(h.uow.tx.actions.scheduled) != 0 {
		t.Error("an unpaid enrolment was recorded or scheduled")
	}
}

// A course taught in another city, or one whose prerequisite is missing, is
// not on offer at all.
func TestCoursesNotReachedAreNotOffered(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	for _, code := range []string{"nursing", "culinary_arts"} {
		resp, err := h.edu.View(ctx, h.meta("req-"+code, "education.view"), CourseRequest{Course: code})
		if err != nil || !strings.Contains(resp.Text, "not on offer") {
			t.Errorf("View(%s) = %q, %v; want not on offer", code, workText(resp), err)
		}
	}
}

// The completion payload survives the schedule's jsonb as written.
func TestEducationPayloadRoundTrips(t *testing.T) {
	h := newWorkHarness(t)
	h.uow.w.ledger.balances[accountID(application.AccountPlayerCash, h.player.ID)] = 1000
	if _, err := h.edu.Enroll(context.Background(), h.meta("req-e", "education.enroll"), CourseRequest{Course: "evening_accounting"}); err != nil {
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
