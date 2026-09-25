package war

// Dice. A battle is resolved from one seed the caller injects (an
// operation's is derived from its id), and every chance in it — whether one
// interceptor kills one target, whether one bomb hits — is a separate roll
// addressed by what it is for: Roll(seed, stage, target, shot). A roll never
// depends on how many rolls came before it, so adding an interceptor adds
// rolls without moving any other: that is what makes "more interceptors
// never lets more through" true shot by shot, not just on average.

// Stages of a battle, the first coordinate of every roll.
const (
	stageSweep int64 = iota + 1
	stagePatrol
	stageLayer
	stageDelivery
	stageDefenceHit
	stageGroundTarget
	stageGroundHit
	stageGroundKill
)

// splitmix64 is one step of SplitMix64 (Steele, Lea and Flood, 2014): a
// bijective mixer with good avalanche, enough to turn coordinates into
// independent-looking dice.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// Roll is a die in 0..9999 for the seed and the coordinates: the same
// arguments always give the same value. A chance of p basis points
// succeeds when Roll(...) < p.
func Roll(seed int64, coords ...int64) int64 {
	h := splitmix64(uint64(seed))
	for _, c := range coords {
		h = splitmix64(h ^ uint64(c))
	}
	return int64(h % BPSWhole)
}

// pick is a die in 0..n-1, for choosing among n.
func pick(seed int64, n int, coords ...int64) int {
	if n <= 1 {
		return 0
	}
	h := splitmix64(uint64(seed))
	for _, c := range coords {
		h = splitmix64(h ^ uint64(c))
	}
	return int(h % uint64(n))
}

// codeKey turns a code into a die coordinate (FNV-1a), so a layer's dice
// are its own whatever other layers stand beside it.
func codeKey(code string) int64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(code); i++ {
		h ^= uint64(code[i])
		h *= 1099511628211
	}
	return int64(h)
}
