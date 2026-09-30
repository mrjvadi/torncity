package main

import (
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Operator tooling for where founded settlements stand (docs/adr/0028
// section 3.2). A village founded before site selection looked at the lot
// grid may have most of its lots in a lake; `check-sites` shows which, and
// `relocate` moves one that has built nothing but its founding kit.

func settlementUsage() {
	fmt.Fprint(os.Stderr, `usage: admin settlement <command>

  check-sites               read-only: every founded settlement's cell, grid
                            slide, buildable-lot share and founding kit, and
                            whether it meets settlement.min_buildable_lot_share_bps
  relocate --id UUID --reason "why" [--by NAME]
                            move a settlement to a valid site nearby (same
                            rules, deterministic, keeping the spacing to other
                            settlements) and lay its founding kit on buildable
                            lots, in one transaction, audited. Refused when the
                            village holds anything beyond its founding kit
                            (civic_hall + road) or has construction in progress.
                            The clients are told to refetch the layout.

The world is regenerated from the active world's seed (about 15 seconds).
TORN_CONFIG and TORN_CONTENT_DIR are read like for admin world.
--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and relocate is refused when none of them names anybody.

DATABASE_URL must be set.
`)
}

func settlementCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		settlementUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "check-sites":
		return settlementCheckSites(ctx)
	case "relocate":
		return settlementRelocate(ctx, args[1:])
	}
	settlementUsage()
	os.Exit(2)
	return nil
}

// activeWorldFor regenerates the active world in memory, the way every
// replica does.
func activeWorldFor(ctx context.Context, worlds *postgres.WorldRepository, cfg *config.Config) (application.World, *worldgen.World, error) {
	row, err := worlds.Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		return row, nil, fmt.Errorf("no world has been created yet")
	}
	if err != nil {
		return row, nil, err
	}
	pack, err := content.LoadWorldGen(contentDir())
	if err != nil {
		return row, nil, fmt.Errorf("loading world generation content: %w", err)
	}
	wc, err := pack.ToContent()
	if err != nil {
		return row, nil, err
	}
	w, err := worldgen.Generate(row.Seed, worldGenParams(cfg.WorldGen), wc)
	if err != nil {
		return row, nil, fmt.Errorf("generating world %d: %w", row.Seed, err)
	}
	return row, w, nil
}

// siteParams is the spawn Params the running game uses, built from the same
// configuration, so a relocation obeys exactly the rules a founding does.
func siteParams(cfg *config.Config) (wsettle.Params, error) {
	pen, err := cfg.Settlement.BiomePenaltyMap()
	if err != nil {
		return wsettle.Params{}, err
	}
	return wsettle.Params{
		MinSpawnDistanceKm: cfg.Settlement.MinSpawnDistanceKm,
		ThreatRadiusKm:     cfg.Settlement.ThreatRadiusKm,
		SearchMaxCells:     cfg.Settlement.SearchMaxCells,
		SearchMaxAttempts:  cfg.Settlement.SearchMaxAttempts,
		ExcludedBiomes:     cfg.Settlement.ExcludedBiomes,
		MaxAbsLatitudeDeg:  cfg.Settlement.MaxAbsLatitudeDeg,
		BiomePenalties:     pen,
		Site: wsettle.SiteRules{GridLots: cfg.Settlement.VillageGridLots,
			MinBuildableShareBps: cfg.Settlement.MinBuildableLotShareBps,
			MaxShiftLots:         cfg.Settlement.GridShiftMaxLots},
	}, nil
}

func kitTypes() []string { return postgres.FoundingKitTypes() }

func settlementCheckSites(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement check-sites: loading configuration: %w", err)
	}
	params, err := siteParams(cfg)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	worlds := postgres.NewWorldRepository(pool)
	row, w, err := activeWorldFor(ctx, worlds, cfg)
	if err != nil {
		return fmt.Errorf("settlement check-sites: %w", err)
	}
	sites, err := postgres.NewSettlementOps(pool).Sites(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("settlement check-sites: %w", err)
	}
	if len(sites) == 0 {
		fmt.Println("no settlement has been founded")
		return nil
	}
	fmt.Printf("rule: at least %d bps of %dx%d lots buildable, centre lot and founding kit on land\n\n",
		params.Site.MinBuildableShareBps, params.Site.GridLots, params.Site.GridLots)
	bad := 0
	for _, s := range sites {
		g := wsettle.GridLotsForTier(s.Tier, params.Site.GridLots)
		rep := wsettle.EvaluateSite(w, s.CellID, s.ShiftX, s.ShiftY, g)
		verdict := "ok"
		if !rep.Meets(wsettle.SiteRules{GridLots: g, MinBuildableShareBps: params.Site.MinBuildableShareBps}) {
			verdict = "NEEDS RELOCATION"
			bad++
			if why := postgres.KitRefusal(s.Held, kitTypes()); why != "" {
				verdict += " (cannot relocate: " + why + ")"
			}
		}
		fmt.Printf("%s  %-14s %-16s cell %-6d slide (%d,%d)  %2d/%d lots = %3d%%  centre %-5v  %s\n",
			s.ID, s.Code, s.Name, s.CellID, s.ShiftX, s.ShiftY, rep.BuildableLots, rep.TotalLots, rep.ShareBps()/100,
			rep.CentreBuildable, verdict)
	}
	fmt.Printf("\n%d settlements, %d need relocation\n", len(sites), bad)
	return nil
}

func settlementRelocate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settlement relocate", flag.ExitOnError)
	fs.Usage = settlementUsage
	id := fs.String("id", "", "the settlement's id (admin settlement check-sites lists them)")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Checked before the database is dialled, like every audited command.
	switch {
	case strings.TrimSpace(*reason) == "":
		return fmt.Errorf("settlement relocate: --reason is required")
	case strings.TrimSpace(*id) == "":
		return fmt.Errorf("settlement relocate: --id is required")
	}
	who, err := operator.resolve("settlement relocate", os.LookupEnv)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement relocate: loading configuration: %w", err)
	}
	params, err := siteParams(cfg)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, w, err := activeWorldFor(ctx, postgres.NewWorldRepository(pool), cfg)
	if err != nil {
		return fmt.Errorf("settlement relocate: %w", err)
	}

	move, err := relocateSettlement(ctx, postgres.NewSettlementOps(pool), w, params, strings.TrimSpace(*id), who,
		strings.TrimSpace(*reason), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("settlement relocate: %w", err)
	}
	fmt.Print(move)
	return nil
}

// relocateSettlement plans and commits one relocation and returns the text
// the operator reads.
func relocateSettlement(ctx context.Context, ops *postgres.SettlementOps, w *worldgen.World, params wsettle.Params,
	id, who, reason string, now time.Time,
) (string, error) {
	g := params.Site.GridLots
	done, err := ops.Relocate(ctx, postgres.Relocation{
		SettlementID: id, Actor: who, Reason: reason, At: now, KitTypes: kitTypes(),
		Plan: postgres.RelocationPlan(w, params),
	})
	if err != nil {
		var refused postgres.ErrRelocateRefused
		if stderrors.As(err, &refused) {
			return "", refused
		}
		if stderrors.Is(err, application.ErrCityNotFound) {
			return "", fmt.Errorf("no founded settlement has id %s", id)
		}
		return "", err
	}
	after := wsettle.EvaluateSite(w, done.After.CellID, done.After.ShiftX, done.After.ShiftY, g)
	before := wsettle.EvaluateSite(w, done.Before.CellID, done.Before.ShiftX, done.Before.ShiftY, g)
	var b strings.Builder
	fmt.Fprintf(&b, "relocated %s (%s)\n", done.Before.Name, done.Before.ID)
	fmt.Fprintf(&b, "before: cell %d slide (%d,%d), %d/%d lots buildable\n", done.Before.CellID, done.Before.ShiftX,
		done.Before.ShiftY, before.BuildableLots, before.TotalLots)
	fmt.Fprintf(&b, "after:  cell %d slide (%d,%d), %d/%d lots buildable\n", done.After.CellID, done.After.ShiftX,
		done.After.ShiftY, after.BuildableLots, after.TotalLots)
	for _, k := range done.After.Held {
		fmt.Fprintf(&b, "  %-11s at lot (%d,%d)\n", k.TypeCode, k.LotX, k.LotY)
	}
	fmt.Fprintf(&b, "by:     %s\nreason: %s\nclients are told to refetch the layout (settlement.relocated)\n", who, reason)
	return b.String(), nil
}
