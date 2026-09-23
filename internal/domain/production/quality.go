package production

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// ErrInvalidQualityInput means a quality, skill or condition is outside
// [0, 100], a loss is out of bounds, or the roll is outside
// [0, item.RollScale).
var ErrInvalidQualityInput = errors.New("production: invalid quality input")

// Quality weights, in percent. They are rules: what matters for the quality of
// a made thing, and how much, is the same for every product. With machines,
// their condition takes a fifth of the weight; without, it has no say and the
// others share it.
const (
	weightInputWithMachines = 50
	weightSkillWithMachines = 30
	weightMachineCondition  = 20
	weightInputHandmade     = 60
	weightSkillHandmade     = 40

	// QualitySwing is how far, in quality points, the roll can move a
	// result either way.
	QualitySwing = 10
)

// QualityInputs is what a finished order's quality depends on, each on the
// 0..100 scale.
type QualityInputs struct {
	// InputQuality is the quality of what was consumed; see InputQuality.
	InputQuality int
	// WorkerSkill is the crew's relevant skill level.
	WorkerSkill int
	// Machines is whether machines did part of the work; when false,
	// MachineCondition is ignored.
	Machines         bool
	MachineCondition int
	// DesignQualityLossBPS is Plan.QualityLossBPS: what a reverse
	// engineered design gives up.
	DesignQualityLossBPS int64
}

// RollQuality is the quality of an order's output. The roll is an input in
// [0, item.RollScale), drawn by the caller:
//
//	base    = 50% input + 30% skill + 20% machine condition   (with machines)
//	        = 60% input + 40% skill                           (handmade)
//	after   = base × (10000 − designLoss) / 10000
//	swing   = (roll − 5000) × 2 × QualitySwing / 10000        (±10 points)
//	quality = clamp(after + swing, 0, 100)
//
// Computed in hundredths of a point and truncated once. A copied design pays
// its loss here on every unit it ever makes.
func RollQuality(q QualityInputs, roll int) (int, error) {
	for _, v := range []int{q.InputQuality, q.WorkerSkill, q.MachineCondition} {
		if v < 0 || v > item.MaxQuality {
			return 0, fmt.Errorf("%w: %d", ErrInvalidQualityInput, v)
		}
	}
	if q.DesignQualityLossBPS < 0 || q.DesignQualityLossBPS > item.MaxQualityLossBPS {
		return 0, fmt.Errorf("%w: design loss %d bps", ErrInvalidQualityInput, q.DesignQualityLossBPS)
	}
	if roll < 0 || roll >= item.RollScale {
		return 0, fmt.Errorf("%w: roll %d", ErrInvalidQualityInput, roll)
	}

	var base int64 // hundredths of a quality point, 0..10000
	if q.Machines {
		base = int64(weightInputWithMachines*q.InputQuality +
			weightSkillWithMachines*q.WorkerSkill +
			weightMachineCondition*q.MachineCondition)
	} else {
		base = int64(weightInputHandmade*q.InputQuality + weightSkillHandmade*q.WorkerSkill)
	}
	after := base * (item.BPS - q.DesignQualityLossBPS) / item.BPS
	swing := (int64(roll) - item.RollScale/2) * 2 * QualitySwing * 100 / item.RollScale
	return int(max(0, min(after+swing, item.MaxQuality*100)) / 100), nil
}

// InputQuality is the quantity-weighted quality of what an order consumed:
// Σ quality × quantity / Σ quantity, truncated. Every consumed component must
// have a quality in [0, 100]. An empty recipe (author, serve) has nothing to
// bring down its quality and scores the maximum, leaving skill to decide.
func InputQuality(consumed item.Recipe, quality map[string]int) (int, error) {
	if len(consumed) == 0 {
		return item.MaxQuality, nil
	}
	num, den := new(big.Int), new(big.Int)
	for _, in := range consumed {
		v, ok := quality[in.Component]
		if !ok || v < 0 || v > item.MaxQuality {
			return 0, fmt.Errorf("%w: %q quality %d", ErrInvalidQualityInput, in.Component, v)
		}
		num.Add(num, new(big.Int).Mul(big.NewInt(int64(v)), big.NewInt(in.Quantity)))
		den.Add(den, big.NewInt(in.Quantity))
	}
	if den.Sign() <= 0 {
		return 0, fmt.Errorf("%w: consumed quantity %s", ErrInvalidQualityInput, den)
	}
	return int(num.Quo(num, den).Int64()), nil
}
