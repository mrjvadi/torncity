// Package recruit holds the rules of specialist recruitment
// (docs/adr/0027-specialist-recruitment.md): each city's finite pool of NPC
// specialists and how it refills, what a specialist expects to be paid, how a
// candidate weighs a company's offer against that expectation, and what
// happens to a hired specialist at each settlement — paid, unpaid, underpaid
// against the market, at the end of the contract.
//
// Everything is whole numbers: money in minor units, shares and chances in
// basis points (10000 is one whole), a roll in [0, BPS). Nothing here reads a
// clock or draws a random number; the caller passes both in, so the same
// inputs always give the same answer and a test can reproduce any outcome.
package recruit

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"math/bits"
)

// BPS is one whole in basis points.
const BPS = 10_000

// Bounds on authored figures: they reject an obvious typo at load and keep
// every product below far from the edge of int64.
const (
	// MaxLevel is the highest skill level a specialist may have; the skill
	// scale of a player is 0..100.
	MaxLevel = 100
	// MaxWage bounds a market wage and every amount of an offer, minor
	// units.
	MaxWage = 1_000_000_000
	// MaxTerm bounds a contract, in periods.
	MaxTerm = 1000
	// MaxPool bounds one pool.
	MaxPool = 1_000_000
)

// ErrInvalid means rules or an offer the functions here cannot apply.
var ErrInvalid = errors.New("recruit: invalid rules or offer")

// mulDiv is floor(a*num/den) for a, num >= 0 and den > 0, saturating at
// MaxInt64 instead of overflowing; anything else is 0.
func mulDiv(a, num, den int64) int64 {
	if a <= 0 || num <= 0 || den <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		return math.MaxInt64
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(q)
}

// Roll is a draw in [0, BPS) from a seed and a position: the same seed and
// position always roll the same, so a repeated check cannot roll again.
func Roll(seed string, at ...int64) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	var b [8]byte
	for _, v := range at {
		binary.BigEndian.PutUint64(b[:], uint64(v))
		_, _ = h.Write(b[:])
	}
	return int64(h.Sum64() % BPS)
}

// Scale applies basis-point factors to v in turn: v × f1/BPS × f2/BPS …,
// each step rounded down. A negative factor counts as zero.
func Scale(v int64, factors ...int64) int64 {
	for _, f := range factors {
		v = mulDiv(v, max(f, 0), BPS)
	}
	return v
}
