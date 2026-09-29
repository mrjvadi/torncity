package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/presence"
)

type fakePresenceStore struct {
	online map[string]bool
	err    error
}

func (f *fakePresenceStore) Beat(_ context.Context, id string, _ time.Time) error {
	f.online[id] = true
	return nil
}

func (f *fakePresenceStore) Online(_ context.Context, ids []string) (map[string]bool, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = f.online[id]
	}
	return out, nil
}

type fakePresenceRepo struct {
	facts     map[string]PresenceFacts
	contacts  map[string]bool
	countries map[string]string
}

func (f *fakePresenceRepo) Visibility(_ context.Context, id string) (presence.Visibility, error) {
	x, ok := f.facts[id]
	if !ok {
		return "", ErrPlayerNotFound
	}
	return x.Visibility, nil
}
func (f *fakePresenceRepo) SetVisibility(context.Context, string, presence.Visibility) error { return nil }
func (f *fakePresenceRepo) Facts(_ context.Context, ids []string) (map[string]PresenceFacts, error) {
	out := map[string]PresenceFacts{}
	for _, id := range ids {
		if x, ok := f.facts[id]; ok {
			out[id] = x
		}
	}
	return out, nil
}
func (f *fakePresenceRepo) ContactsAmong(context.Context, string, []string) (map[string]bool, error) {
	return f.contacts, nil
}
func (f *fakePresenceRepo) CountryOf(context.Context, []string) (map[string]string, error) {
	return f.countries, nil
}
func (f *fakePresenceRepo) Roster(_ context.Context, settlement string, _ int) ([]string, error) {
	var out []string
	for id, x := range f.facts {
		if x.CityID == settlement || x.ResidenceCityID == settlement {
			out = append(out, id)
		}
	}
	return out, nil
}
func (f *fakePresenceRepo) Member(_ context.Context, id, settlement string) (bool, error) {
	x := f.facts[id]
	return x.CityID == settlement || x.ResidenceCityID == settlement, nil
}

func presenceRig() (*PresenceService, *fakePresenceStore, *fakePresenceRepo) {
	store := &fakePresenceStore{online: map[string]bool{"viewer": true, "mate": true, "friend": true, "citizen": true, "stranger": true}}
	repo := &fakePresenceRepo{
		facts: map[string]PresenceFacts{
			"viewer":   {PlayerID: "viewer", DisplayName: "V", Visibility: presence.Everyone, CityID: "A", ResidenceCityID: "A"},
			"mate":     {PlayerID: "mate", DisplayName: "M", Visibility: presence.Nobody, CityID: "A", ResidenceCityID: "A", PlaceCode: "market", OpenActions: []string{"work_shift"}},
			"friend":   {PlayerID: "friend", DisplayName: "F", Visibility: presence.Contacts, CityID: "B", ResidenceCityID: "B", PlaceCode: "hall", OpenActions: []string{"hospital_discharge", "work_shift", "market_expiry"}},
			"citizen":  {PlayerID: "citizen", DisplayName: "C", Visibility: presence.Everyone, CityID: "C", ResidenceCityID: "C", PlaceCode: "x"},
			"citizen2": {PlayerID: "citizen2", DisplayName: "C2", Visibility: presence.Contacts, CityID: "C", ResidenceCityID: "C"},
			"stranger": {PlayerID: "stranger", DisplayName: "S", Visibility: presence.Everyone, CityID: "D", ResidenceCityID: "D"},
		},
		contacts:  map[string]bool{"friend": true},
		countries: map[string]string{"A": "K1", "B": "K2", "C": "K1"},
	}
	return &PresenceService{Store: store, Repo: repo, RosterLimit: 50}, store, repo
}

func TestStatusFollowsTheRelation(t *testing.T) {
	s, _, _ := presenceRig()
	ctx := context.Background()
	cases := []struct {
		target string
		want   presence.Detail
	}{
		// Same settlement: everything, even for a player who chose nobody.
		{"mate", presence.Detail{Visible: true, Online: true, Activity: presence.Working, Place: "market"}},
		// A friend elsewhere: status and activity, never the place; the
		// strongest open action wins and a market expiry is not activity.
		{"friend", presence.Detail{Visible: true, Online: true, Activity: presence.Hospital}},
		// Same country, target open to everyone: online only.
		{"citizen", presence.Detail{Visible: true, Online: true}},
		// Same country but the target chose contacts: nothing.
		{"citizen2", presence.Detail{}},
		// No tie at all: nothing.
		{"stranger", presence.Detail{}},
		{"viewer", presence.Detail{Visible: true, Online: true, Activity: presence.Idle}},
	}
	for _, c := range cases {
		got, err := s.Status(ctx, "viewer", c.target)
		if err != nil {
			t.Fatal(err)
		}
		if got.Detail != c.want {
			t.Errorf("%s: %+v, want %+v", c.target, got.Detail, c.want)
		}
	}
	if _, err := s.Status(ctx, "viewer", "nobody-here"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("unknown target: %v", err)
	}
}

func TestViewerWhoChoseNobodySeesNoPresence(t *testing.T) {
	s, _, repo := presenceRig()
	v := repo.facts["viewer"]
	v.Visibility = presence.Nobody
	repo.facts["viewer"] = v
	got, err := s.Status(context.Background(), "viewer", "mate")
	if err != nil || got.Visible {
		t.Fatalf("%+v %v: hiding your own presence hides everyone else's from you", got, err)
	}
	list, err := s.Players(context.Background(), "viewer", "A")
	if err != nil || !list.Hidden || len(list.Players) != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	for _, p := range list.Players {
		if p.PlayerID != "viewer" && p.Visible {
			t.Errorf("%s shown to a hidden viewer", p.PlayerID)
		}
	}
}

func TestPlayersIsForMembersOnly(t *testing.T) {
	s, store, _ := presenceRig()
	ctx := context.Background()
	if _, err := s.Players(ctx, "stranger", "A"); !errors.Is(err, ErrNotInSettlement) {
		t.Fatalf("a non-member read the list: %v", err)
	}
	list, err := s.Players(ctx, "viewer", "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Players) != 2 || list.Online != 2 || list.Hidden {
		t.Errorf("list = %+v", list)
	}
	delete(store.online, "mate")
	list, _ = s.Players(ctx, "viewer", "A")
	if list.Online != 1 {
		t.Errorf("online = %d after a player's key expired", list.Online)
	}
	store.err = errors.New("redis down")
	list, err = s.Players(ctx, "viewer", "A")
	if err != nil || list.Online != 0 {
		t.Errorf("with Redis down everyone reads offline, not an error: %+v %v", list, err)
	}
}

// The group screen's roster ignores the presser's own setting and never
// reveals it: it is the settlement's civic list.
func TestCivicRosterIgnoresTheViewersSetting(t *testing.T) {
	s, _, _ := presenceRig()
	list, err := s.Civic(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Players) != 2 || list.Online != 2 {
		t.Fatalf("civic = %+v", list)
	}
	for _, p := range list.Players {
		if !p.Visible || !p.Online {
			t.Errorf("%s: %+v", p.PlayerID, p.Detail)
		}
	}
}

func TestMemberships(t *testing.T) {
	s, _, repo := presenceRig()
	got, err := s.Memberships(context.Background(), "viewer")
	if err != nil || len(got) != 1 || got[0] != "A" {
		t.Fatalf("home and here are one settlement: %v %v", got, err)
	}
	v := repo.facts["viewer"]
	v.CityID = "B"
	repo.facts["viewer"] = v
	got, _ = s.Memberships(context.Background(), "viewer")
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("home first, then where they stand: %v", got)
	}
	if _, err := s.Memberships(context.Background(), "ghost"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("unknown player: %v", err)
	}
}
