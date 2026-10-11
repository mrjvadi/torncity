package application

import (
	"context"
	"time"
)

// The ports of survival (migration 0147; docs/adr/0070): a player's thirst and warmth, the garments they wear and the sickness
// they carry. The rules are internal/domain/survival and handlers/survival.go.

// The causes a hospital stay can have besides the wounds (docs/adr/0070 3.7): nobody dies, a collapse is a rest in care.
const (
	CauseExposure    = "exposure"
	CauseDehydration = "dehydration"
)

// The sicknesses (player_illness.kind).
const (
	IllnessWaterborne = "waterborne"
	IllnessChill      = "chill"
)

// ReasonRiverDrink and the item reasons of survival.
const (
	// ItemDrunk is water a player drank from the settlement's well or from their bag: units leaving the world.
	ItemDrunk ItemReason = "drunk"
	// ItemGarmentWorn is a garment put on: it leaves the bag for the player's wardrobe (player_garments).
	ItemGarmentWorn ItemReason = "garment_worn"
	// ItemBoiled is the firewood burnt to make river water safe.
	ItemBoiled ItemReason = "boiled"
)

func init() {
	itemReasons[ItemDrunk] = true
	itemReasons[ItemGarmentWorn] = true
	itemReasons[ItemBoiled] = true
}

// SurvivalRules are the settings of survival (config settlement.survival_*): from RuleAt plus GraceDays on, thirst and warmth drift
// for the people of founded settlements; before that nothing changes. The zero value leaves survival off.
type SurvivalRules struct {
	RuleAt    time.Time
	GraceDays int64
}

// Enabled reports whether the rule is configured.
func (r SurvivalRules) Enabled() bool { return !r.RuleAt.IsZero() }

// On reports whether survival applies at now.
func (r SurvivalRules) On(now time.Time) bool {
	return r.Enabled() && !now.Before(r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0))))
}

// SurvivalReference is the reference type of the item-journal legs of survival.
const SurvivalReference = "player_survival"

// SurvivalState is a player's thirst and warmth, each with the instant it describes.
type SurvivalState struct {
	PlayerID               string
	ThirstMilli            int64
	ThirstAt               time.Time
	WarmthMilli            int64
	WarmthAt               time.Time
	ShelterUntil           time.Time
	ShelterBonus           int
	ColdArmed, ThirstArmed bool
	NotifiedAt             time.Time
}

// Garment is a garment of a player's wardrobe.
type Garment struct {
	ID, PlayerID, Item, Slot string
	// UsesLeft is the wear left in minutes of being worn.
	UsesLeft  int
	Worn      bool
	Status    string
	CountedAt time.Time
	CreatedAt time.Time
}

// Garment statuses.
const (
	GarmentKept    = "kept"
	GarmentWornOut = "worn_out"
)

// Illness is a sickness of a player.
type Illness struct {
	ID, PlayerID, Kind string
	OnsetAt, EndsAt    time.Time
	Status             string
	SettlementID       string
	BuildingID         string
	MedicineItem       string
	MedicineUnits      int
	TreatedAt          time.Time
}

// SurvivalRepository persists survival. Reach it through Tx.Survival.
type SurvivalRepository interface {
	// State returns the player's state, nil when none was ever written. Lock serialises one player's changes (the stats row is the
	// lock every activity takes; this one orders the survival rows).
	State(ctx context.Context, playerID string) (*SurvivalState, error)
	SaveState(ctx context.Context, s SurvivalState) error
	// Garments lists the garments of the wardrobe that are not worn out, oldest first.
	Garments(ctx context.Context, playerID string) ([]Garment, error)
	AddGarment(ctx context.Context, g Garment) error
	// SetWorn puts a garment on or takes it off, counting its wear from at.
	SetWorn(ctx context.Context, id string, worn bool, at time.Time) error
	// SetUses records the wear left at the instant counted; a garment at 0 is worn out.
	SetUses(ctx context.Context, id string, uses int, counted time.Time) error
	// Illnesses lists the player's active sicknesses.
	Illnesses(ctx context.Context, playerID string) ([]Illness, error)
	// Fall records a new sickness; false when the player already has one of the kind.
	Fall(ctx context.Context, i Illness) (bool, error)
	// End ends a sickness. Treat records a medicine that shortened it.
	End(ctx context.Context, id string, at time.Time) error
	Treat(ctx context.Context, id string, endsAt time.Time, item string, units int, settlementID, buildingID string, at time.Time) error
}
