package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The Activities hub and the health home (ADR 0038 sections 3 and 4.3). Like
// every screen of the split (ADR 0039) they carry facts and codes only: each
// edge words them from its own catalogue.

// The screens' names on the wire.
const (
	ScreenActivitiesHub = "activities_hub"
	ScreenHealthHome    = "health_home"
)

var (
	screenActivitiesHub = presentation.Define[ActivitiesHubView](ScreenActivitiesHub, "life")
	screenHealthHome    = presentation.Define[HealthHomeView](ScreenHealthHome, "life", presentation.Private())
)

// The addresses of the two screens.
const (
	AddrActivitiesHub = "activities:hub"
	AddrHealthHome    = "health:home"
)

// The activities a hub entry can be, by code.
const (
	ActivityWork     = "work"
	ActivityLearn    = "learn"
	ActivityHealth   = "health"
	ActivityTraining = "training"
	ActivityCrime    = "crime"
	ActivityMissions = "missions"
	ActivityRankings = "rankings"
)

// Why the crime hub has nothing to offer (ADR 0038 section 4.4).
const (
	// CrimeEmptyLevelTooLow: the player is below crime.min_level.
	CrimeEmptyLevelTooLow = "level_too_low"
	// CrimeEmptyNoVenue: the settlement they stand in has no place crime can
	// happen at (a village, the neutral city, the road).
	CrimeEmptyNoVenue = "no_venue"
	// CrimeEmptyNoTargets: there is a venue, but no crime here can be
	// attempted by this player now.
	CrimeEmptyNoTargets = "no_targets"
)

// ActivityPlace is the settlement the player stands in. Tier is "village",
// "town" or "city" (a content city, the neutral one included, is a city);
// Neutral says it is the neutral city.
type ActivityPlace struct {
	Code, Name, Tier string
	Neutral          bool
}

// ActivityEntry is one activity listed to the player. Command is what opens
// it. An activity that is not listed is not mentioned at all.
type ActivityEntry struct {
	Code    string
	Command string
}

// ActivitiesHubView is the Activities hub: where the player stands and what
// they are offered there.
type ActivitiesHubView struct {
	Place   ActivityPlace
	Entries []ActivityEntry
}

// ActivitiesHub is the hub of activities.
func ActivitiesHub(c presentation.Ctx, v ActivitiesHubView) *presentation.Response {
	a := make([]presentation.Action, 0, len(v.Entries)+2)
	for _, e := range v.Entries {
		a = append(a, presentation.Do(e.Command).Named("activity."+e.Code).About(e.Code))
	}
	a = append(a, back(AddrHome), refresh(AddrActivitiesHub))
	return screenActivitiesHub.Response(c.Lang, v, a...)
}

// Why the health home has nothing to offer.
const (
	// HealthEmptyNoFacility: no place of care stands here; rest and the way to
	// the neutral city are what is left.
	HealthEmptyNoFacility = "no_facility"
)

// The kinds of place of care.
const (
	FacilityHealthHouse = "health_house"
	FacilityClinic      = "clinic"
)

// HealthStay is a hospital stay in progress.
type HealthStay struct {
	Remaining time.Duration
	EndsAt    time.Time
}

// HealthRest is resting at the player's own home: Has says they have one,
// CanRest that they may rest now and Wait what is left of the cool-down.
type HealthRest struct {
	Has     bool
	CanRest bool
	Wait    time.Duration
}

// HealthFacility is a place of care that stands in the player's settlement.
type HealthFacility struct {
	Kind     string
	Building presentation.Named
}

// HealthHomeView is the player's health, where to rest and sleep, the places
// of care that stand where they are and, when none can help, where to go.
type HealthHomeView struct {
	Place             ActivityPlace
	Health, MaxHealth int
	// Admitted is the hospital stay the player is in, nil when free.
	Admitted *HealthStay
	Rest     HealthRest
	// Facilities are the places of care standing here.
	Facilities []HealthFacility
	// Refer is the neutral city the player is sent to for what is beyond
	// the village's care; nil in a city, which has its own hospital.
	Refer *presentation.Named
	// Empty is why there is nothing here but rest, "" when a place of care
	// stands.
	Empty string
}

// HealthHome is the health screen of a settlement.
func HealthHome(c presentation.Ctx, v HealthHomeView) *presentation.Response {
	a := []presentation.Action{presentation.Do("life.me").Named("health.sleep")}
	if v.Rest.Has {
		a = append(a, presentation.Do("settlement.home.rest").Named("health.rest"))
	}
	a = append(a, presentation.Do("health.hospital").Named("health.hospital"),
		back(AddrActivitiesHub), refresh(AddrHealthHome))
	return screenHealthHome.Response(c.Lang, v, a...)
}
