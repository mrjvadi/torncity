package panel

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// fakeAccounts keeps accounts, sessions and answers in memory with the
// database's semantics (postgres.PanelAccounts).
type fakeAccounts struct {
	mu       sync.Mutex
	accounts map[string]*postgres.PanelAccount
	sessions map[string]*postgres.PanelSession
	revoked  map[string]bool
	answers  map[string]*postgres.PanelAnswer
	audits   []string
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{accounts: map[string]*postgres.PanelAccount{}, sessions: map[string]*postgres.PanelSession{},
		revoked: map[string]bool{}, answers: map[string]*postgres.PanelAnswer{}}
}

func (f *fakeAccounts) add(a postgres.PanelAccount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[a.Username] = &a
}

func (f *fakeAccounts) ByUsername(_ context.Context, username string) (*postgres.PanelAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[username]
	if !ok {
		return nil, postgres.ErrNoPanelAccount
	}
	c := *a
	return &c, nil
}

func (f *fakeAccounts) byID(id string) *postgres.PanelAccount {
	for _, a := range f.accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (f *fakeAccounts) LoginFailure(_ context.Context, id string, at time.Time, after int, base, max time.Duration) (*time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byID(id)
	a.FailedLogins++
	if a.FailedLogins >= after {
		until := at.Add(lockFor(a.FailedLogins, after, base, max))
		a.LockedUntil = &until
	}
	return a.LockedUntil, nil
}

func (f *fakeAccounts) LoginSuccess(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byID(id)
	a.FailedLogins, a.LockedUntil, a.LastLoginAt = 0, nil, &at
	return nil
}

func (f *fakeAccounts) AuditPanel(_ context.Context, actor, action, _ string, _ map[string]any, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, actor+" "+action)
	return nil
}

func (f *fakeAccounts) CreateSession(_ context.Context, s postgres.PanelSession, replaced string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if replaced != "" {
		f.revoked[replaced] = true
	}
	f.sessions[s.TokenHash] = &s
	return nil
}

func (f *fakeAccounts) Session(_ context.Context, hash string, now time.Time, idle, touch time.Duration) (*postgres.PanelSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[hash]
	if !ok || f.revoked[hash] || !s.ExpiresAt.After(now) || !s.LastSeenAt.After(now.Add(-idle)) {
		return nil, postgres.ErrNoPanelSession
	}
	if a := f.byID(s.AccountID); a == nil || !a.Active() {
		return nil, postgres.ErrNoPanelSession
	}
	if now.Sub(s.LastSeenAt) >= touch {
		s.LastSeenAt = now
	}
	c := *s
	return &c, nil
}

func (f *fakeAccounts) EndSession(_ context.Context, hash string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[hash] = true
	return nil
}

func (f *fakeAccounts) BeginRequest(_ context.Context, account, id, route string, _ time.Time) (*postgres.PanelAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.answers[account+"/"+id]; ok {
		c := *a
		return &c, nil
	}
	f.answers[account+"/"+id] = &postgres.PanelAnswer{Route: route}
	return nil, nil
}

func (f *fakeAccounts) FinishRequest(_ context.Context, account, id string, status int, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.answers[account+"/"+id]
	a.Status, a.Response = status, json.RawMessage(body)
	return nil
}

func (f *fakeAccounts) ForgetRequest(_ context.Context, account, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.answers, account+"/"+id)
	return nil
}

func (f *fakeAccounts) Purge(context.Context, time.Time, time.Time) error { return nil }

// fakeBackend answers reads with fixed data and counts the changes.
type fakeBackend struct {
	mu      sync.Mutex
	grants  []operator.Actor
	cleared []int64
	players []postgres.PlayerHit
	failing error
}

func (b *fakeBackend) Overview(_ context.Context, days int, now time.Time) (Overview, error) {
	return Overview{Days: days, Since: now.Add(-time.Duration(days) * 24 * time.Hour), Total: 1000}, nil
}

func (b *fakeBackend) SearchPlayers(_ context.Context, q string, _ int) ([]postgres.PlayerHit, error) {
	var out []postgres.PlayerHit
	for _, p := range b.players {
		if q == "" || p.Code == q || "@"+p.Username == q {
			out = append(out, p)
		}
	}
	return out, nil
}

func (b *fakeBackend) Player(_ context.Context, code string) (PlayerDetail, error) {
	for _, p := range b.players {
		if p.Code == code {
			return PlayerDetail{Code: p.Code, Name: p.Name}, nil
		}
	}
	return PlayerDetail{}, postgres.ErrNoSuchPlayer
}

func (b *fakeBackend) Cities(context.Context) ([]postgres.CityLine, error) { return nil, nil }
func (b *fakeBackend) City(context.Context, string) (CityDetail, error) {
	return CityDetail{}, postgres.ErrNoSuchCity
}
func (b *fakeBackend) Bots(context.Context) ([]string, error)                          { return []string{"bot01"}, nil }
func (b *fakeBackend) LinkGroup(context.Context, GroupChange, operator.Actor) error   { return nil }
func (b *fakeBackend) UnlinkGroup(context.Context, GroupChange, operator.Actor) error { return nil }
func (b *fakeBackend) Companies(context.Context, int) ([]CompanyLine, error)           { return nil, nil }
func (b *fakeBackend) Company(context.Context, string) (CompanyDetail, error) {
	return CompanyDetail{}, postgres.ErrNotFound
}
func (b *fakeBackend) GrantDefence(context.Context, string, operator.Actor) (int64, error)  { return 1, nil }
func (b *fakeBackend) RevokeDefence(context.Context, string, operator.Actor) (int64, error) { return 1, nil }
func (b *fakeBackend) Seats(context.Context, string, string) ([]Seat, error)                { return nil, nil }
func (b *fakeBackend) ChangeSeat(_ context.Context, s SeatChange, _ bool, _ operator.Actor) (Seat, error) {
	return Seat{Office: s.Office, Seat: s.Seat}, nil
}
func (b *fakeBackend) Policy(context.Context, string, string) ([]PolicyPlace, error) { return nil, nil }
func (b *fakeBackend) Elections(context.Context, int) ([]postgres.ElectionLine, error) {
	return nil, nil
}
func (b *fakeBackend) OpenElection(context.Context, string, string, string, operator.Actor) (OpenedElection, error) {
	return OpenedElection{No: 1}, nil
}
func (b *fakeBackend) Verify(context.Context) (Verification, error) { return Verification{OK: true}, nil }

func (b *fakeBackend) Grant(_ context.Context, player string, amount int64, a operator.Actor) (operator.Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failing != nil {
		return operator.Grant{}, b.failing
	}
	b.grants = append(b.grants, a)
	return operator.Grant{ID: "g1", Label: player, Amount: amount, GrantedBy: "admin:" + a.Name}, nil
}

func (b *fakeBackend) Content(context.Context) (ContentStatus, error) {
	return ContentStatus{Version: 3, Checksum: "abc", LocalChecksum: "def"}, nil
}
func (b *fakeBackend) LoadContent(context.Context, operator.Actor) (ContentLoaded, error) {
	return ContentLoaded{Version: 4}, nil
}
func (b *fakeBackend) Flags(context.Context, string, int) ([]postgres.FlagLine, error) { return nil, nil }
func (b *fakeBackend) Flag(context.Context, int64) (postgres.FlagLine, error) {
	return postgres.FlagLine{}, postgres.ErrNotFound
}
func (b *fakeBackend) ClearFlag(_ context.Context, no int64, _ operator.Actor) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleared = append(b.cleared, no)
	return nil
}
func (b *fakeBackend) Holds(context.Context, int) ([]postgres.HoldLine, error) { return nil, nil }
func (b *fakeBackend) SettleHold(_ context.Context, no int64, release bool, _ operator.Actor) (Settled, error) {
	return Settled{No: no, Status: map[bool]string{true: "released", false: "returned"}[release]}, nil
}
func (b *fakeBackend) Announce(context.Context, Message, operator.Actor) (Announced, error) {
	return Announced{Cities: 2}, nil
}
func (b *fakeBackend) Broadcast(context.Context, Message, operator.Actor) (int, error) { return 1, nil }
func (b *fakeBackend) Audit(context.Context, string, int) ([]postgres.AuditLine, error) {
	return nil, nil
}
