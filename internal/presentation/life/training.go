package life

import "github.com/mrjvadi/torncity/internal/presentation"

// Training (the «تمرین» activity; docs/research/2026-10-03-activities-audit.md section 7).
// The core sends facts and codes only; each edge words them.

const (
	ScreenTrainingHome = "training_home"
	ScreenTrained      = "trained"
)

var (
	screenTrainingHome = presentation.Define[TrainingHomeView](ScreenTrainingHome, "life", presentation.Private())
	screenTrained      = presentation.Define[TrainedView](ScreenTrained, "life", presentation.Private())
)

// The addresses.
const (
	AddrTrainingHome  = "training:home"
	AddrTrainingStart = "training:start"
)

// The venues a session can be at, by code.
const (
	VenueYard   = "yard"   // open ground, bodyweight exercise, free
	VenueGround = "ground" // a settlement's training ground, with a trainer
	VenueGym    = "gym"    // the neutral city's gym
)

// TrainingVenue is one place the player can train here, or could if a building stood.
type TrainingVenue struct {
	Code string
	// EfficiencyBPS is how well the venue trains (10000 = full).
	EfficiencyBPS int64
	// Fee is a session's price in minor units (0 = free).
	Fee int64
	// Available says the venue stands here; Missing names the building that
	// would make it so when it does not.
	Available bool
	Missing   *presentation.Named
}

// TrainingHomeView is the training screen: the player's own condition and the venues.
type TrainingHomeView struct {
	Place presentation.Named
	// Energy, MaxEnergy, Stamina and StrengthLevel are the player's own.
	Energy, MaxEnergy, Stamina, StrengthLevel int
	// EnergyCost is what a session spends.
	EnergyCost int
	// MaxEnergyCap is the most training can add to the energy bar.
	MaxEnergyCap int
	Venues       []TrainingVenue
}

// TrainingHome is the training screen.
func TrainingHome(c presentation.Ctx, v TrainingHomeView) *presentation.Response {
	var a []presentation.Action
	for _, venue := range v.Venues {
		if venue.Available {
			a = append(a, presentation.Do("training.start", venue.Code).Named("training.start").About(venue.Code))
		}
	}
	a = append(a, back(AddrActivitiesHub), refresh(AddrTrainingHome))
	return screenTrainingHome.Response(c.Lang, v, a...)
}

// TrainedView is a finished session.
type TrainedView struct {
	Venue string
	// Stamina is the stamina gained, MaxEnergyAdded the energy bar growth, StrengthXP
	// the strength experience, StrengthLevel the level reached when it rose (else 0).
	Stamina, MaxEnergyAdded, StrengthLevel int
	StrengthXP                             int64
	Fee                                    int64
	// Energy and MaxEnergy are what is left after paying for it.
	Energy, MaxEnergy int
}

// Trained is a session done.
func Trained(c presentation.Ctx, v TrainedView) *presentation.Response {
	return screenTrained.Response(c.Lang, v, presentation.Do("training.home").Named("training.again"), back(AddrActivitiesHub))
}
