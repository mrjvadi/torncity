package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Addresses of the mission screens.
const (
	AddrMissionBoard   = "mission:board"
	AddrMission        = "mission:view"
	AddrMissionAccept  = "mission:accept"
	AddrMissionDeliver = "mission:deliver"
	AddrMissionAbandon = "mission:abandon"
)

// MissionYes confirms giving a mission up.
const MissionYes = "yes"

// What an objective's target names.
const (
	TargetCity           = "city"
	TargetCareer         = "career"
	TargetCareerCategory = "career_category"
	TargetCourse         = "course"
	TargetItem           = "item"
	TargetItemCategory   = "item_category"
	TargetCrime          = "crime"
	TargetCrimeCategory  = "crime_category"
)

// MissionTarget is what an objective's target names.
type MissionTarget struct {
	Kind string
	Code string
	Name string
}

// MissionBoardRef names a board and where it stands.
type MissionBoardRef struct {
	Code  string
	Name  string
	Place Named
	// Open is how many missions the board posts to this player, set on the list of boards.
	Open int
}

// MissionObjective is one objective, with its progress.
type MissionObjective struct {
	Kind        string
	Target      MissionTarget
	Count, Done int64
}

// MissionReward is what completing a mission gives.
type MissionReward struct {
	Cash  int64
	XP    int64
	Items []LootLine
}

// MissionLine is one mission on a board.
type MissionLine struct {
	Mission    Named
	Reward     MissionReward
	Blocked    string
	Wait       time.Duration
	Repeatable bool
	// Objectives are what the mission asks, with no progress yet: a board shows what is wanted.
	Objectives []MissionObjective
}

// MissionBoardView is a city's boards, or one board and its missions.
type MissionBoardView struct {
	CityCode, City string
	// Tier is the stage of the place the player stands in (village, town or city).
	Tier string
	// Currency is the money the cash rewards are paid in where the player stands; nil when
	// the place has none of its own.
	Currency *presentation.Currency
	Boards   []MissionBoardRef
	Board    *MissionBoardRef
	// Here says the player stands at the board: they may take a mission.
	Here     bool
	Missions []MissionLine
}

// MissionView is one mission, or the question of giving it up.
type MissionView struct {
	Mission    Named
	No         int64
	Board      MissionBoardRef
	Objectives []MissionObjective
	Reward     MissionReward
	MinLevel   int
	Requires   []Named
	Blocked    string
	Wait       time.Duration
	Repeatable bool
	// Cooldown and TimeLimit are real waits; zero for none.
	Cooldown  time.Duration
	TimeLimit time.Duration
	// Abandoning asks to confirm giving it up.
	Abandoning bool
	// Max is how many missions may run at once.
	Max int
}

// MissionProgressLine is one of the player's missions.
type MissionProgressLine struct {
	No         int64
	Mission    Named
	Status     string
	Objectives []MissionObjective
	// Cash and Withheld are what a completion paid and what the caps kept.
	Cash, Withheld int64
	// Left is the time left of a time-limited mission.
	Left time.Duration
	// Deliver says goods are handed in for it, at its board.
	Deliver bool
}

// Mission notices on the player's missions screen.
const (
	MissionNoticeAccepted  = "accepted"
	MissionNoticeDelivered = "delivered"
	MissionNoticeCompleted = "completed"
	MissionNoticeAbandoned = "abandoned"
)

// MissionNotice is what just happened, above the player's missions.
type MissionNotice struct {
	Kind               string
	Mission            Named
	Qty                int64
	Cash, Withheld, XP int64
}

// MissionsMineView is the player's missions.
type MissionsMineView struct {
	Notice *MissionNotice
	Max    int
	Active []MissionProgressLine
	Recent []MissionProgressLine
}

// Mission refusal kinds.
const (
	MissionRefusedNotFound         = "not_found"
	MissionRefusedNotHere          = "not_here"
	MissionRefusedBlocked          = "blocked"
	MissionRefusedNotActive        = "not_active"
	MissionRefusedExpired          = "expired"
	MissionRefusedNothingToDeliver = "nothing_to_deliver"
)

// MissionRefusalView is a refused mission request.
type MissionRefusalView struct {
	Kind    string
	Blocked string
	Wait    time.Duration
	Level   int
	Max     int
}
