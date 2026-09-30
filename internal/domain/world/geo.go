package world

import "math"

// GreatCircleKM is the distance along the surface of a sphere of radiusKM
// between two points given as latitude and longitude in degrees, rounded UP to
// a whole kilometre and never less than one for two different places: a
// journey between them always costs and takes something.
//
// It is the haversine formula. Pure and deterministic: the same numbers always
// give the same distance, in either order. Two identical points are zero apart
// (the caller refuses a journey to where it already stands before it gets
// here).
func GreatCircleKM(lat1, lon1, lat2, lon2, radiusKM float64) int {
	if lat1 == lat2 && lon1 == lon2 {
		return 0
	}
	rad := math.Pi / 180
	p1, p2 := lat1*rad, lat2*rad
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	// Rounding can push a a hair above 1 for antipodal points.
	a = math.Min(1, math.Max(0, a))
	km := 2 * radiusKM * math.Asin(math.Sqrt(a))
	d := int(math.Ceil(km))
	if d < 1 {
		d = 1
	}
	return d
}
