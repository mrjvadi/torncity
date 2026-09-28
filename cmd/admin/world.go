package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// worldGenParams converts config.WorldGen into worldgen.Params. Mirrors
// cmd/game's and cmd/worldpreview's own copies of the same conversion: each
// process that calls worldgen.Generate makes it, by the design WorldGen's
// own doc comment in internal/config states.
func worldGenParams(wg config.WorldGen) worldgen.Params {
	return worldgen.Params{
		CellCount:              wg.CellCount,
		NeighborK:              wg.NeighborK,
		PlateCount:             wg.PlateCount,
		OceanicPlateFraction:   worldgen.Permille(wg.OceanicPlateFraction),
		LandFraction:           worldgen.Permille(wg.LandFraction),
		NoiseOctaves:           wg.NoiseOctaves,
		NoiseBaseFrequency:     wg.NoiseBaseFrequency,
		NoisePersistence:       worldgen.Permille(wg.NoisePersistence),
		WarpAmplitude:          wg.WarpAmplitude,
		WarpFrequency:          wg.WarpFrequency,
		BoundaryInfluenceSteps: wg.BoundaryInfluenceSteps,
		MoistureBands:          wg.MoistureBands,
		RiverFlowThreshold:     wg.RiverFlowThreshold,
		LakeMinDepth:           worldgen.Elevation(wg.LakeMinDepth),
		LakeMinAreaCells:       wg.LakeMinAreaCells,

		PlanetRadiusKm:              wg.PlanetRadiusKm,
		ChunkBaseLOD:                int8(wg.ChunkBaseLOD),
		ChunkTileEdge:               wg.ChunkTileEdge,
		ChunkDetailFrequency:        wg.ChunkDetailFrequency,
		ChunkDetailAmplitude:        worldgen.Elevation(wg.ChunkDetailAmplitude),
		ChunkStreamFrequency:        wg.ChunkStreamFrequency,
		ChunkStreamAmplitude:        worldgen.Elevation(wg.ChunkStreamAmplitude),
		ChunkDepositTilesPerDeposit: wg.ChunkDepositTilesPerDeposit,
	}
}

// Operator tooling for the world registry (docs/adr/0028-world-and-
// settlements.md section 2). A world is a seed, a generator version and a
// hash of the parameters it was generated under — never the planet itself,
// which every replica regenerates in memory from those three things
// (application.WorldCache). The owner chooses the production seed; this
// tool only records the choice, once, and refuses to create a second world
// while one is already active.

func worldUsage() {
	fmt.Fprint(os.Stderr, `usage: admin world <command>

  create --seed N --reason "why" [--by NAME]
                            create and activate the one live world from
                            seed N. Refused while a world is already active
                            (a world is never replaced from under a live
                            game): roll back with the world's own migration
                            if a mistaken seed must be undone before anyone
                            has founded on it.
  show                      the active world's seed, generator version and
                            when and by whom it was created

TORN_CONTENT_DIR overrides the content directory (default `+defaultContentDir+`),
read for the world-generation content (world.yml) the params hash covers.
--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and create is refused when none of them names anybody.

DATABASE_URL must be set.
`)
}

func worldCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		worldUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		return worldCreate(ctx, args[1:])
	case "show":
		return worldShow(ctx)
	}
	worldUsage()
	os.Exit(2)
	return nil
}

func worldCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("world create", flag.ExitOnError)
	fs.Usage = worldUsage
	seedText := fs.String("seed", "", "the world seed, a positive integer (required)")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *reason == "" {
		return fmt.Errorf("world create: --reason is required")
	}
	if *seedText == "" {
		return fmt.Errorf("world create: --seed is required")
	}
	seed, err := strconv.ParseUint(*seedText, 10, 64)
	if err != nil {
		return fmt.Errorf("world create: --seed %q is not a whole non-negative number: %w", *seedText, err)
	}
	who, err := operator.resolve("world create", os.LookupEnv)
	if err != nil {
		return err
	}

	pack, err := content.LoadWorldGen(contentDir())
	if err != nil {
		return fmt.Errorf("world create: loading world generation content: %w", err)
	}
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("world create: world generation content is invalid: %w", err)
	}
	wgContent, err := pack.ToContent()
	if err != nil {
		return fmt.Errorf("world create: %w", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("world create: loading configuration: %w", err)
	}
	params := worldGenParams(cfg.WorldGen)

	// A generation smoke test: a seed that cannot actually produce a world
	// (a bad params/content combination) must be caught here, not the first
	// time a group tries to found on it.
	if _, err := worldgen.Generate(seed, params, wgContent); err != nil {
		return fmt.Errorf("world create: seed %d does not generate a usable world with the current parameters: %w", seed, err)
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	worlds := postgres.NewWorldRepository(pool)
	w, err := worlds.Create(ctx, application.World{
		Seed: seed, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: paramsHash(params, wgContent), CreatedBy: who,
	})
	if stderrors.Is(err, application.ErrWorldAlreadyActive) {
		return fmt.Errorf("world create: a world is already active; a world is never replaced from under a live game")
	}
	if err != nil {
		return fmt.Errorf("world create: %w", err)
	}
	fmt.Printf("created and activated world %s\nseed:              %d\ngenerator version: %d\nparams hash:       %s\nby:                %s\nreason:            %s\n",
		w.ID, w.Seed, w.GeneratorVersion, w.ParamsHash, who, *reason)
	return nil
}

func worldShow(ctx context.Context) error {
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	w, err := postgres.NewWorldRepository(pool).Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		fmt.Println("no world has been created yet")
		return nil
	}
	if err != nil {
		return fmt.Errorf("world show: %w", err)
	}
	fmt.Printf("active world %s\nseed:              %d\ngenerator version: %d\nparams hash:       %s\ncreated by:        %s\ncreated at:        %s\n",
		w.ID, w.Seed, w.GeneratorVersion, w.ParamsHash, w.CreatedBy, w.CreatedAt.Format(time.RFC3339))
	return nil
}

// paramsHash hashes the generation parameters and content: not the planet
// itself (that is worldgen.World.Fingerprint, computed after generation),
// but the INPUTS a replica must agree on before it trusts its own in-memory
// regeneration for this world id.
func paramsHash(params worldgen.Params, c worldgen.Content) string {
	// Marshalled rather than hashed field-by-field: both types are plain,
	// already-exported structs with no cycles, and JSON gives a stable,
	// portable encoding without hand-listing every field here to rot when
	// one is added. This hash is a drift DETECTOR, not a security boundary,
	// so JSON's encoding is more than exact enough for it.
	buf, err := json.Marshal(struct {
		Params  worldgen.Params
		Content worldgen.Content
	}{params, c})
	if err != nil {
		// Neither type can fail to marshal (no channels, no funcs); kept as
		// a hard error rather than swallowed so a future field that CAN
		// fail to marshal is caught here, not shipped as an empty hash.
		panic(fmt.Sprintf("postgres: hashing world params: %v", err))
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}
