package place

import (
	"errors"
	"testing"
	"time"
)

func samplePlaces() []Place {
	return []Place{
		{Code: "city_centre", Default: true, MoveTime: 10 * time.Minute},
		{Code: "business_district", MoveTime: 15 * time.Minute, Services: []Service{ServiceBank}, WorkCategories: []string{"finance"}},
		{Code: "train_station", Requires: []string{"rail_station"}, MoveTime: 20 * time.Minute, Arrivals: []string{"train"}},
		{Code: "harbour", Cities: []string{"brennhaven"}, MoveTime: 30 * time.Minute, Energy: 2},
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(samplePlaces()); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	bad := samplePlaces()
	bad[1].Default = true
	bad[2].MoveTime = 0
	bad[3].Services = []Service{"casino"}
	bad = append(bad, Place{Code: "bus_depot", MoveTime: time.Minute, Arrivals: []string{"train"}})
	err := Validate(bad)
	if !errors.Is(err, ErrInvalidPlaces) {
		t.Fatalf("Validate(bad) = %v", err)
	}
	for _, want := range []string{"exactly one place", "move time", "unknown service", "stops at both"} {
		if !contains([]string{err.Error()}, err.Error()) || !containsText(err.Error(), want) {
			t.Errorf("no %q in %v", want, err)
		}
	}
}

func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCityMap(t *testing.T) {
	m := CityMap(samplePlaces(), "ostmarch", []string{"bus_terminal"})
	if len(m.Places) != 2 {
		t.Fatalf("ostmarch without a railway has %d places, want 2", len(m.Places))
	}
	m = CityMap(samplePlaces(), "brennhaven", []string{"rail_station"})
	if len(m.Places) != 4 {
		t.Fatalf("brennhaven has %d places, want 4", len(m.Places))
	}
	if p, _ := m.Current("gone"); p.Code != "city_centre" {
		t.Errorf("an unknown stored place resolves to %q, want the default", p.Code)
	}
	if p, _ := m.ForService(ServiceBank); p.Code != "business_district" {
		t.Errorf("the bank is at %q", p.Code)
	}
	if p, _ := m.ForMode("train"); p.Code != "train_station" {
		t.Errorf("trains stop at %q", p.Code)
	}
	if p, _ := m.ForMode("zeppelin"); p.Code != "city_centre" {
		t.Errorf("an unplaced mode lands at %q, want the default", p.Code)
	}
	if p, _ := m.ForWork("finance"); p.Code != "business_district" {
		t.Errorf("finance works at %q", p.Code)
	}
}

func TestStartMove(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	m := CityMap(samplePlaces(), "brennhaven", []string{"rail_station"})
	mv, err := StartMove(m, "", "harbour", now, 60)
	if err != nil {
		t.Fatal(err)
	}
	if mv.From != "city_centre" || mv.Energy != 2 || mv.Duration() != 30*time.Second {
		t.Errorf("move = %+v, want from the centre, 2 energy, 30 real seconds", mv)
	}
	if _, err := StartMove(m, "harbour", "harbour", now, 60); !errors.Is(err, ErrAlreadyThere) {
		t.Errorf("a move to where one stands = %v", err)
	}
	if _, err := StartMove(CityMap(samplePlaces(), "ostmarch", nil), "", "harbour", now, 60); !errors.Is(err, ErrUnknownPlace) {
		t.Errorf("a move to a place the city lacks = %v", err)
	}
}
