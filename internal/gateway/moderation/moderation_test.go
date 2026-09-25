package moderation

import (
	"context"
	"errors"
	"testing"
	"time"
)

type source struct {
	s     Standing
	err   error
	reads int
}

func (f *source) Standing(context.Context, int64, time.Time) (Standing, error) {
	f.reads++
	return f.s, f.err
}

type cache struct {
	m   map[int64]Standing
	ttl time.Duration
}

func (c *cache) Get(_ context.Context, id int64) (Standing, bool, error) {
	s, ok := c.m[id]
	return s, ok, nil
}

func (c *cache) Set(_ context.Context, id int64, s Standing, ttl time.Duration) error {
	c.m[id], c.ttl = s, ttl
	return nil
}

func TestVerdicts(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name    string
		s       Standing
		inGroup bool
		want    Verdict
	}{
		{"nothing", Standing{}, true, Allowed},
		{"muted in a group", Standing{Muted: true}, true, Muted},
		{"muted in private", Standing{Muted: true}, false, Allowed},
		{"banned in private", Standing{Banned: true}, false, Banned},
		{"banned in a group", Standing{Banned: true, Muted: true}, true, Banned},
		{"ended", Standing{Banned: true, Until: now.Add(-time.Second)}, false, Allowed},
		{"not yet ended", Standing{Banned: true, Until: now.Add(time.Minute)}, false, Banned},
	}
	for _, c := range cases {
		if got := verdict(c.s, c.inGroup, now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTheCacheSavesTheSource(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	src := &source{s: Standing{Muted: true}}
	ch := &cache{m: map[int64]Standing{}}
	c := &Checker{Source: src, Cache: ch, TTL: 30 * time.Second, Now: func() time.Time { return now }}
	for range 3 {
		if v, err := c.Blocks(t.Context(), 7, true); err != nil || v != Muted {
			t.Fatalf("%v %v", v, err)
		}
	}
	if src.reads != 1 {
		t.Fatalf("the source was read %d times", src.reads)
	}
	if ch.ttl != 30*time.Second {
		t.Fatalf("ttl %s", ch.ttl)
	}
}

func TestAShortModerationIsNotCachedPastItsEnd(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ch := &cache{m: map[int64]Standing{}}
	c := &Checker{Source: &source{s: Standing{Banned: true, Until: now.Add(5 * time.Second)}}, Cache: ch,
		TTL: time.Minute, Now: func() time.Time { return now }}
	if v, _ := c.Blocks(t.Context(), 7, false); v != Banned {
		t.Fatalf("%v", v)
	}
	if ch.ttl != 5*time.Second {
		t.Fatalf("cached for %s", ch.ttl)
	}
}

func TestAFailureLetsTheCommandThrough(t *testing.T) {
	c := &Checker{Source: &source{err: errors.New("down")}}
	if v, err := c.Blocks(t.Context(), 7, true); v != Allowed || err == nil {
		t.Fatalf("%v %v", v, err)
	}
	var none *Checker
	if v, err := none.Blocks(t.Context(), 7, true); v != Allowed || err != nil {
		t.Fatalf("a nil checker: %v %v", v, err)
	}
}
