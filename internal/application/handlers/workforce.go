package handlers

// The workforce: one ledger of the seats the settlement's people hold (roadmap 2.2, ADR
// 0041, docs/research/2026-10-06-w1-core.md). The labour pool is the people the homes
// give; every claim on it is counted here, in one place, so the keepers, the shopkeeper,
// the teachers, the trainer and the shifts of every kind read the same numbers.
//
// Priority is explicit: the standing posts (a teacher at the school, the shopkeeper,
// a keeper for each store) are filled first, from StaffFree; the day labour (shifts)
// takes what is left. A busy construction day therefore never leaves a store unkept.

// seatClaims is the pool and what holds it.
type seatClaims struct {
	// Pool is the people the homes give (labor.PoolSize).
	Pool int64
	// Shifts counts the NPCs on a running shift, of any kind.
	Shifts int64
	// Teachers counts the NPC teachers of the school.
	Teachers int64
	// Shop is the shopkeeper's seat (1 when the founding stall stands), Keepers the
	// seats of the stores the last settled day kept.
	Shop, Keepers int64
	// Scholars counts the NPC scholars the last settled research day seated in the research buildings (ADR 0048).
	Scholars int64
	// Clerks counts the market clerk the last market day seated (ADR 0049), 0 or 1.
	Clerks int64
	// Services counts the staff the last settled service day seated at the watch posts and inns (ADR 0052).
	Services int64
	// Stalls counts the keepers stall owners hired (ADR 0062): each holds a seat while hired.
	Stalls int64
}

// Reserved is the seats the standing posts hold apart from the teachers.
func (c seatClaims) Reserved() int64 { return c.Shop + c.Keepers + c.Scholars + c.Clerks + c.Services + c.Stalls }

// StaffFree is the pool less the teachers: the people the standing posts are filled from.
func (c seatClaims) StaffFree() int64 { return max(c.Pool-c.Teachers, 0) }

// Free is the pool less every NPC at work and the teachers, before the posts reserved.
func (c seatClaims) Free() int64 { return max(c.Pool-c.Shifts-c.Teachers, 0) }

// Available is how many people are free for hire once the standing posts are seated.
func (c seatClaims) Available() int64 { return max(c.Free()-c.Reserved(), 0) }
