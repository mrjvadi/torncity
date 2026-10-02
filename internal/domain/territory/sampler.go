package territory

import (
	"math"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// WorldSampler reads the generated world along a ray: from the observer
// tile's centre it walks a great circle at the bin's bearing and reads the
// tile elevation under each sample. The sea surface counts as sea level
// (a ship or a coast is seen over the water, not over the sea floor).
//
// The great-circle step uses trigonometry, which is not bit-identical across
// math libraries; replicas of one binary agree, and the result is computed
// once and stored (ADR 0042 4.4), so no cross-language determinism is needed.
type WorldSampler struct {
	W        *worldgen.World
	Src      worldgen.TileSource
	Observer worldgen.TileID
}

// ObserverGroundM implements Sampler.
func (s WorldSampler) ObserverGroundM() (int, error) { return s.at(s.Observer) }

// PlanetRadiusM implements Sampler.
func (s WorldSampler) PlanetRadiusM() int64 { return int64(s.W.Params.PlanetRadiusKm * 1000) }

// GroundM implements Sampler.
func (s WorldSampler) GroundM(bin, bins, dM int) (int, error) {
	lat0, lon0 := s.W.TileCentre(s.Observer)
	la, lo := destination(lat0, lon0, 360*float64(bin)/float64(bins), float64(dM)/(s.W.Params.PlanetRadiusKm*1000))
	return s.at(s.W.TileOfLatLon(la, lo))
}

func (s WorldSampler) at(t worldgen.TileID) (int, error) {
	ct, err := s.Src.TileAt(t)
	if err != nil {
		return 0, err
	}
	if ct.IsOcean() {
		return 0, nil
	}
	return int(ct.Elevation), nil
}

// destination is the point reached from (lat, lon) degrees travelling the
// angular distance ang (radians) on the bearing brg (degrees clockwise from
// north).
func destination(lat, lon, brg, ang float64) (float64, float64) {
	const r = math.Pi / 180
	la, lo, b := lat*r, lon*r, brg*r
	la2 := math.Asin(math.Sin(la)*math.Cos(ang) + math.Cos(la)*math.Sin(ang)*math.Cos(b))
	lo2 := lo + math.Atan2(math.Sin(b)*math.Sin(ang)*math.Cos(la), math.Cos(ang)-math.Sin(la)*math.Sin(la2))
	return la2 / r, lo2 / r
}
