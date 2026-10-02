package village

import "github.com/mrjvadi/torncity/internal/presentation"

// The work home (ADR 0038 section 4.1): the one «کار» screen. Both doors, the
// Activities hub and the village's hiring board, open it. It shows the shift the
// player is working now, the jobs on the board and the workplaces they can start
// a shift at, and, when there is nothing to do, why and the one step that helps.

// ScreenWorkHome is the screen's name on the wire.
const ScreenWorkHome = "work_home"

// AddrWorkHome is the address of the work home.
const AddrWorkHome = "work:home"

var screenWorkHome = presentation.Define[WorkHomeView](ScreenWorkHome, "village", presentation.Private())

// Why the work home has nothing to start.
const (
	// WorkEmptyNoSettlement: the player belongs to no village; careers of the
	// city are their work.
	WorkEmptyNoSettlement = "no_settlement"
	// WorkEmptyNotResident: the viewer does not live in the village.
	WorkEmptyNotResident = "not_resident"
	// WorkEmptyNoJobs: the village has no open job and no workplace standing.
	WorkEmptyNoJobs = "no_jobs"
)

// The one step that helps when the work home is empty.
const (
	// WorkNextBuild: the head raises a workplace (the build menu).
	WorkNextBuild = "build"
	// WorkNextAskHead: a resident asks their village head to build one.
	WorkNextAskHead = "ask_head"
	// WorkNextJoin: the viewer moves into the village first.
	WorkNextJoin = "join"
)

// WorkHomePlace is the settlement the work is in; Tier is its stage.
type WorkHomePlace struct {
	Code, Name, Tier string
}

// WorkHomeView is the work home.
type WorkHomeView struct {
	Place    WorkHomePlace
	Resident bool
	// Energy and MaxEnergy are the viewer's own. A village shift costs none:
	// the numbers are shown so the screen and the status bar agree.
	Energy, MaxEnergy int
	// Working is the shift the viewer is on now, nil when idle.
	Working *LaborShiftLine
	// Jobs are the open jobs of the board; Workplaces the standing buildings a
	// shift can be started at.
	Jobs       []LaborJobLine
	Workplaces []WorkplaceLine
	Market     LaborMarketLine
	// IsHead says the viewer is the village's head: they post jobs and raise
	// workplaces.
	IsHead bool
	// Empty is why there is nothing to start now, "" when there is; Next the
	// one step that helps.
	Empty string
	Next  string
}

// WorkHome is the work home.
func WorkHome(c presentation.Ctx, v WorkHomeView) *presentation.Response {
	var a []presentation.Action
	for _, j := range v.Jobs {
		if j.CanTake {
			a = append(a, act(AddrLaborTake, j.ID).Named("labor.take").About(j.Building.Code))
		}
	}
	for _, w := range v.Workplaces {
		if w.Ready && w.Busy < w.Workers {
			a = append(a, act(AddrWork, w.ID).Named("village.work.start").About(w.Building.Code))
		}
	}
	a = append(a, act(AddrLaborBoard).Named("labor.board"), act(AddrLaborMine).Named("labor.mine"))
	if v.Next == WorkNextBuild {
		a = append(a, act(AddrBuildMenu).Named("village.build"))
	}
	a = append(a, back("activities:hub"), refresh(AddrWorkHome))
	return screenWorkHome.Response(c.Lang, v, a...)
}
