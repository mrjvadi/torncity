package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The research desk (ADR 0048): how many projects the settlement can run, what opens each slot, who works there,
// how fast and at what price, the sharing pacts and the breakthrough progress. Nothing here is worded or laid out.

// The research desk.
const (
	ScreenResearch   = "research"
	AddrResearchDesk = "settlement:research"
)

// The acts of the research desk, the first argument of settlement.research.
const (
	// ResearchActionPost takes a scholar's post in a research building (code: the building id).
	ResearchActionPost = "post"
	// ResearchActionLeave leaves the post the viewer holds.
	ResearchActionLeave = "leave"
	// ResearchActionPropose offers a research-sharing pact to another settlement (code: its code).
	ResearchActionPropose = "propose"
	// ResearchActionAccept and ResearchActionDecline answer a pact offered to the settlement (code: the pact id);
	// ResearchActionEnd ends one.
	ResearchActionAccept  = "accept"
	ResearchActionDecline = "decline"
	ResearchActionEnd     = "end"
)

// Refusal kinds of the research desk.
const (
	// ResearchNoPost: the building takes no scholars, or every post of it is held.
	ResearchNoPost = "research_no_post"
	// ResearchPostHeld: the viewer already holds a scholar's post.
	ResearchPostHeld = "research_post_held"
	// ResearchNoPostHeld: the viewer holds none to leave.
	ResearchNoPostHeld = "research_no_post_held"
	// ResearchNotLiterate: a scholar reads and writes.
	ResearchNotLiterate = "research_not_literate"
	// ResearchPactOpen: the two settlements already have a pact, offered or active.
	ResearchPactOpen = "research_pact_open"
	// ResearchPactSelf: a pact needs another settlement.
	ResearchPactSelf = "research_pact_self"
	// ResearchPactNotFound: no such pact, or it is not the settlement's.
	ResearchPactNotFound = "research_pact_not_found"
	// ResearchNoSlot: no slot is free for a new project (every slot is busy).
	ResearchNoSlot = "research_no_slot"
)

// Why a research building did not work today.
const (
	ResearchIdleNoScholars = "no_scholars"
	ResearchIdleNoUpkeep   = "no_upkeep"
	ResearchIdleNoWage     = "no_wage"
)

// Pact states, as the settlement sees them.
const (
	ResearchPactActive   = "active"
	ResearchPactIncoming = "incoming"
	ResearchPactOutgoing = "outgoing"
)

// ResearchSlotLine is one source of capacity today: the free slot or one staffed building.
type ResearchSlotLine struct {
	// Ref is "free" or the building id.
	Ref      string
	Building presentation.Named
	Capacity int
	Used     int
	// BonusBPS is what the building's shelves and benches add to a project in it; StaffBPS what its scholars add in all
	// (shared by the projects running there).
	BonusBPS int64
	StaffBPS int64
}

// ResearchUpkeepLine is one item a research building uses up on a working day, and whether the stock has it.
type ResearchUpkeepLine struct {
	Item presentation.Named
	Qty  int64
	Have int64
	// StandIn is the older material that still serves in place of Item until the board's StandInUntil (zero value: none);
	// StandInHave is how much of it the stock holds.
	StandIn     presentation.Named
	StandInHave int64
}

// ResearchBuildingLine is one research building and its scholars.
type ResearchBuildingLine struct {
	ID       string
	Building presentation.Named
	// Open says the building worked today; Idle why not (ResearchIdle*), empty when it did.
	Open bool
	Idle string
	// Slots it gives while it works; Needed the scholars it needs to open; Posts the most it seats.
	Slots, Needed, Posts int
	// Players are the player scholars on its posts, NPCs the labour pool's; Wage a scholar's day.
	Players, NPCs int
	Wage          int64
	BonusBPS      int64
	Upkeep        []ResearchUpkeepLine
	// Mine is true when the viewer holds a post here; CanTake when the viewer may take one.
	Mine    bool
	CanTake bool
}

// ResearchProjectLine is one running project with the quote it started on.
type ResearchProjectLine struct {
	Knowledge presentation.Named
	// Slot is "free" or the building id; Building names it.
	Slot     string
	Building presentation.Named
	SpeedBPS int64
	// AheadBPS is the ahead-of-era factor (10000 none), DiscountBPS the breakthrough discount, ShareBPS the sharing bonus.
	AheadBPS, DiscountBPS, ShareBPS int64
	FinishAt                        time.Time
	Left                            time.Duration
}

// ResearchPactLine is one pact of the settlement.
type ResearchPactLine struct {
	ID      string
	Partner presentation.Named
	// State is ResearchPactActive, ResearchPactIncoming (offered to us) or ResearchPactOutgoing (we offered).
	State string
}

// ResearchExperienceLine is the breakthrough progress in one field: real work done, and the points the next project
// of a depth-1 item would take for the whole discount (Need scales with the item's depth).
type ResearchExperienceLine struct {
	Field  string
	Points int64
	// Per is the points per depth that give the whole discount; MaxBPS the discount.
	Per, MaxBPS int64
}

// ResearchBoardView is the research desk.
type ResearchBoardView struct {
	Name string
	// Capacity is the projects the settlement can run now, Running how many it runs.
	Capacity, Running int
	// Frontier is the depth of the knowledge tree the world has reached; an item deeper than it costs more.
	Frontier        int
	LiteracyPercent int
	Slots           []ResearchSlotLine
	Buildings       []ResearchBuildingLine
	Projects        []ResearchProjectLine
	Pacts           []ResearchPactLine
	// Neighbours are settlements a pact may be offered to (their codes are the act's argument).
	Neighbours []presentation.Named
	Experience []ResearchExperienceLine
	// StandInUntil is when the grace of the real goods ends: until then an upkeep line may be met by its StandIn
	// (zero: no grace).
	StandInUntil time.Time
	// MayShare says the viewer holds research.share.
	MayShare bool
	// ShareCapBPS is the most sharing pacts add to a project.
	ShareCapBPS int64
}

var screenResearch = presentation.Define[ResearchBoardView](ScreenResearch, "village")

// ResearchBoard is the research desk.
func ResearchBoard(c presentation.Ctx, v ResearchBoardView) *presentation.Response {
	a := []presentation.Action{back(AddrKnowledgeList), refresh(AddrResearchDesk)}
	for _, b := range v.Buildings {
		switch {
		case b.Mine:
			a = append(a, act(AddrResearchDesk, ResearchActionLeave).Named("research.leave"))
		case b.CanTake:
			a = append(a, act(AddrResearchDesk, ResearchActionPost, b.ID).Named("research.post").About(b.ID))
		}
	}
	if v.MayShare {
		for _, p := range v.Pacts {
			switch p.State {
			case ResearchPactIncoming:
				a = append(a, act(AddrResearchDesk, ResearchActionAccept, p.ID).Named("research.accept").About(p.ID),
					act(AddrResearchDesk, ResearchActionDecline, p.ID).Named("research.decline").About(p.ID))
			default:
				a = append(a, act(AddrResearchDesk, ResearchActionEnd, p.ID).Named("research.end").About(p.ID))
			}
		}
		for _, n := range v.Neighbours {
			a = append(a, act(AddrResearchDesk, ResearchActionPropose, n.Code).Named("research.propose").About(n.Code))
		}
	}
	return screenResearch.Response(c.Lang, v, a...)
}
