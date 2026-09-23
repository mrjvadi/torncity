package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

type fakeSource struct {
	version int
	pack    *content.Pack
	err     error
	loads   int
}

func (f *fakeSource) Active(context.Context) (postgres.ActiveVersion, error) {
	return postgres.ActiveVersion{Version: f.version}, nil
}

func (f *fakeSource) LoadActive(context.Context) (*content.Pack, error) {
	f.loads++
	if f.err != nil {
		return nil, f.err
	}
	p := *f.pack
	p.Version = f.version
	return &p, nil
}

func reloadPack() *content.Pack {
	return &content.Pack{
		Schema: 1,
		Cities: []content.CityDef{
			{Code: "a", Name: "A", CostOfLiving: 1, SpawnWeight: 1, Country: "home"},
			{Code: "b", Name: "B", CostOfLiving: 1, Country: "home"},
		},
		Routes: []content.RouteDef{{From: "a", To: "b", Distance: 10}},
		Levels: []content.LevelDef{
			{Code: content.CountryLevel, Parents: []string{content.WorldLevel}},
			{Code: content.CityLevel, Parents: []string{content.CountryLevel}},
		},
		Jurisdictions: []content.JurisdictionDef{{Code: "home", Name: "Home", Level: content.CountryLevel}},
	}
}

// A newer active version is swapped in; the same version is not read again;
// a version that cannot be served is skipped once and the old one kept.
func TestReloadSwapsANewerVersionOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := content.NewRegistry()
	src := &fakeSource{version: 3, pack: reloadPack()}

	failed := reloadOnce(context.Background(), src, registry, 0, logger)
	if registry.Version() != 3 || failed != 0 || src.loads != 1 {
		t.Fatalf("after the first check: version %d, failed %d, loads %d", registry.Version(), failed, src.loads)
	}
	reloadOnce(context.Background(), src, registry, 0, logger)
	if src.loads != 1 {
		t.Errorf("the version in force was read again")
	}

	src.version, src.err = 4, errors.New("boom")
	failed = reloadOnce(context.Background(), src, registry, 0, logger)
	if registry.Version() != 3 || failed != 4 {
		t.Errorf("a broken version replaced the served one: version %d, failed %d", registry.Version(), failed)
	}
	reloadOnce(context.Background(), src, registry, failed, logger)
	if src.loads != 2 {
		t.Errorf("a version known to fail was read again: %d loads", src.loads)
	}
}
