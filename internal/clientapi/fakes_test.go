package clientapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// memCodes is ClientLinkCodes in memory, with the same single use and
// expiry as the Redis store.
type memCodes struct {
	mu    sync.Mutex
	now   func() time.Time
	codes map[string]memCode
}

type memCode struct {
	claim   application.ClientLinkClaim
	expires time.Time
}

func newMemCodes(now func() time.Time) *memCodes {
	return &memCodes{now: now, codes: map[string]memCode{}}
}

func (m *memCodes) put(code string, claim application.ClientLinkClaim, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[code] = memCode{claim: claim, expires: m.now().Add(ttl)}
}

func (m *memCodes) Issue(_ context.Context, claim application.ClientLinkClaim, requestID string, ttl time.Duration, _ int) (application.ClientLinkCode, error) {
	code := fmt.Sprintf("CODE%04d", len(m.codes))
	m.put(code, claim, ttl)
	return application.ClientLinkCode{Code: code, ExpiresAt: m.now().Add(ttl)}, nil
}

func (m *memCodes) Redeem(_ context.Context, code string) (application.ClientLinkClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[code]
	delete(m.codes, code)
	if !ok || !m.now().Before(c.expires) {
		return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
	}
	return c.claim, nil
}

// memDevices is ClientDevices in memory, with the repository's rotation and
// reuse rules.
type memDevices struct {
	mu      sync.Mutex
	devices map[string]*application.ClientDevice
	tokens  map[string]*memToken
	order   []string
}

type memToken struct {
	device  string
	expires time.Time
	used    bool
}

func newMemDevices() *memDevices {
	return &memDevices{devices: map[string]*application.ClientDevice{}, tokens: map[string]*memToken{}}
}

func (m *memDevices) Create(_ context.Context, d application.ClientDevice, hash string, expires time.Time, max int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := d
	m.devices[d.ID] = &cp
	m.order = append(m.order, d.ID)
	m.tokens[hash] = &memToken{device: d.ID, expires: expires}
	active := 0
	for i := len(m.order) - 1; i >= 0; i-- {
		x := m.devices[m.order[i]]
		if x.PlayerID != d.PlayerID || x.RevokedAt != nil {
			continue
		}
		active++
		if active > max {
			t := d.CreatedAt
			x.RevokedAt = &t
		}
	}
	return nil
}

func (m *memDevices) Rotate(_ context.Context, oldHash, newHash string, now, expires time.Time) (application.ClientDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[oldHash]
	if !ok {
		return application.ClientDevice{}, application.ErrClientTokenInvalid
	}
	d := m.devices[t.device]
	switch {
	case d.RevokedAt != nil:
		return application.ClientDevice{}, application.ErrClientTokenInvalid
	case t.used:
		d.RevokedAt = &now
		return application.ClientDevice{}, application.ErrClientTokenReused
	case !now.Before(t.expires):
		return application.ClientDevice{}, application.ErrClientTokenInvalid
	}
	t.used = true
	m.tokens[newHash] = &memToken{device: d.ID, expires: expires}
	d.LastSeenAt = now
	return *d, nil
}

func (m *memDevices) Get(_ context.Context, id string) (application.ClientDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return application.ClientDevice{}, application.ErrClientDeviceNotFound
	}
	return *d, nil
}

func (m *memDevices) Active(_ context.Context, playerID string) ([]application.ClientDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []application.ClientDevice
	for _, id := range m.order {
		if d := m.devices[id]; d.PlayerID == playerID && d.RevokedAt == nil {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (m *memDevices) Revoke(_ context.Context, playerID, deviceID, _ string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[deviceID]
	if !ok || d.PlayerID != playerID || d.RevokedAt != nil {
		return false, nil
	}
	d.RevokedAt = &now
	return true, nil
}

// memPlayers reads and creates players in memory.
type memPlayers struct {
	mu      sync.Mutex
	players map[string]*application.Player
	created int
}

func (m *memPlayers) GetByID(_ context.Context, id string) (*application.Player, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.players[id]
	if !ok {
		return nil, application.ErrPlayerNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memPlayers) EnsurePlayer(_ context.Context, tg int64, username, name, lang, _ string, _ int64) (*application.Player, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.players {
		if p.TelegramUserID == tg {
			cp := *p
			return &cp, nil
		}
	}
	m.created++
	p := &application.Player{ID: fmt.Sprintf("tg-player-%d", tg), TelegramUserID: tg, Username: username,
		DisplayName: name, Language: lang, PublicCode: "NEW0001"}
	m.players[p.ID] = p
	cp := *p
	return &cp, nil
}

// memOnce is Once in memory.
type memOnce struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (m *memOnce) Once(_ context.Context, key string, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen[key] {
		return false, nil
	}
	m.seen[key] = true
	return true, nil
}

// memLimits is Limiter in memory.
type memLimits struct {
	mu     sync.Mutex
	counts map[string]int
}

func (m *memLimits) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[key]++
	return m.counts[key] <= limit, nil
}

// ids counts up.
type ids struct {
	mu sync.Mutex
	n  int
}

func (i *ids) next() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", i.n)
}

// clock is a settable now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
