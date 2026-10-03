package roads

// SlopeBPS is the planner's slope multiplier (see slopeBPS) for a rise of dh
// metres over a run of lengthM, for a class that builds up to maxGradeBPS; ok
// is false when the grade is more than three times the class limit. The lot
// router (internal/domain/landroad) prices its steps with the same rule, so a
// line chosen on tiles and refined on lots agrees on what is steep.
func SlopeBPS(dh, lengthM, maxGradeBPS int) (bps int, ok bool) {
	if lengthM <= 0 || maxGradeBPS <= 0 {
		return 0, false
	}
	if dh < 0 {
		dh = -dh
	}
	return slopeBPS(dh, lengthM, maxGradeBPS)
}
