// Command worldpreview generates one planet and renders it to PNGs and a
// JSON summary, so a generation change can be looked at rather than only
// tested.
//
// It is a command of its own rather than an `admin world preview`
// subcommand because it needs none of what makes admin's subcommands admin
// subcommands: no DATABASE_URL, no audit log, no operator name. It is a pure
// function of a seed, a cell count and configs/content/world.yml, run
// locally by whoever is iterating on the generator.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

func main() {
	seed := flag.Uint64("seed", 42, "world seed")
	cells := flag.Int("cells", 0, "override configs/config.yml's worldgen.cell_count (0 = use it as configured)")
	contentDir := flag.String("content", "configs/content", "content directory (must contain world.yml)")
	configPath := flag.String("config", config.DefaultPath, "config file holding the worldgen: tuning section")
	outDir := flag.String("out", "", "output directory for the PNGs, summary.json and legend.txt (required)")
	width := flag.Int("width", 1600, "output image width in pixels")
	height := flag.Int("height", 800, "output image height in pixels")
	flag.Parse()

	if *outDir == "" {
		fmt.Fprintln(os.Stderr, "worldpreview: --out is required")
		os.Exit(2)
	}
	if err := run(*seed, *cells, *contentDir, *configPath, *outDir, *width, *height); err != nil {
		fmt.Fprintln(os.Stderr, "worldpreview:", err)
		os.Exit(1)
	}
}

// worldGenParams converts config.WorldGen — plain ints/float64s, because
// internal/config imports no internal/domain package (see WorldGen's own
// doc comment) — into the typed worldgen.Params the generator actually
// takes. This is the one place that conversion happens.
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

func run(seed uint64, cellOverride int, contentDir, configPath, outDir string, width, height int) error {
	pack, err := content.LoadWorldGen(contentDir)
	if err != nil {
		return fmt.Errorf("loading world content: %w", err)
	}
	c, err := pack.ToContent()
	if err != nil {
		return fmt.Errorf("validating world content: %w", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", configPath, err)
	}
	params := worldGenParams(cfg.WorldGen)
	if cellOverride > 0 {
		params.CellCount = cellOverride
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	start := time.Now()
	w, err := worldgen.Generate(seed, params, c)
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("generating world: %w", err)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	heapDeltaMB := float64(after.HeapAlloc-before.HeapAlloc) / (1024 * 1024)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outDir, err)
	}

	renderStart := time.Now()
	grids := buildPixelGrids(w, width, height)
	fmt.Printf("pixel grid built in %s\n", time.Since(renderStart))

	if err := renderReliefBiome(w, grids, width, height, filepath.Join(outDir, "relief_biome.png")); err != nil {
		return fmt.Errorf("rendering relief/biome map: %w", err)
	}
	if err := renderResources(w, grids, width, height, filepath.Join(outDir, "resources.png")); err != nil {
		return fmt.Errorf("rendering resources map: %w", err)
	}
	if err := renderPolitical(w, grids, width, height, filepath.Join(outDir, "political_empty.png")); err != nil {
		return fmt.Errorf("rendering political-empty map: %w", err)
	}
	if err := writeLegend(w, filepath.Join(outDir, "legend.txt")); err != nil {
		return fmt.Errorf("writing legend: %w", err)
	}

	summary := w.BuildSummary()
	summary.GenerationTimeMS = elapsed.Milliseconds()

	summaryJSON, err := json.MarshalIndent(struct {
		worldgen.Summary
		Seed             uint64  `json:"seed"`
		GeneratorVersion int     `json:"generator_version"`
		HeapDeltaMB      float64 `json:"heap_delta_mb"`
	}{summary, seed, worldgen.GeneratorVersion, heapDeltaMB}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling summary: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.json"), summaryJSON, 0o644); err != nil {
		return fmt.Errorf("writing summary.json: %w", err)
	}

	fmt.Printf("seed=%d cells=%d generation=%s heap_delta=%.1fMB land=%.1f%%\n",
		seed, params.CellCount, elapsed, heapDeltaMB, summary.LandPercent)
	fmt.Println(string(summaryJSON))
	return nil
}
