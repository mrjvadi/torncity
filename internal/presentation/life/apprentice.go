package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The teaching network (docs/adr/0071): apprenticeships and the directory of teachers. The core sends codes and numbers; each client
// words them from its own locale.

// The screens.
const (
	ScreenApprentices = "apprentices"
	ScreenTeachers    = "teachers"
)

// More addresses the teaching screens lead to.
const (
	AddrApprentices = "settlement:apprentice.mine"
	AddrTeachers    = "education:teachers"
	AddrApprAccept  = "settlement:apprentice.accept"
	AddrApprLeave   = "settlement:apprentice.leave"
	AddrApprDismiss = "settlement:apprentice.dismiss"
	AddrApprExam    = "settlement:apprentice.exam"
	AddrApprJoin    = "settlement:apprentice.join"
)

// The roles a line is seen from, and the kinds of teacher the directory lists.
const (
	RoleApprentice = "apprentice"
	RoleMaster     = "master"

	DirKindClass        = "class"      // a certificate holder who teaches the course
	DirKindMaster       = "master"     // a player master who takes apprentices at his workshop
	DirKindNPC          = "npc_master" // the pool's craftsman at a public workshop
	DirKindCity         = "city"       // the neutral city's school
	ExamNotice          = "exam"
	NoticeApprOffered   = "offered"
	NoticeApprStarted   = "started"
	NoticeApprLeft      = "left"
	NoticeApprDismissed = "dismissed"
	NoticeApprPassed    = "passed"
	NoticeApprFailed    = "failed"
	NoticeApprDeclined  = "declined"
)

// ApprenticeLine is one apprenticeship, seen by one of its two people.
type ApprenticeLine struct {
	ID    string
	Craft string
	// Role is what the viewer is in it; With the other person (empty for an NPC master) and Workshop the building.
	Role     string
	With     presentation.Named
	Workshop presentation.Named
	// Status is offered, active, completed, left, dismissed or declined; NPC says the master is the pool's craftsman.
	Status string
	NPC    bool
	// Fee and WageShareBPS are the terms; TermDays the term; EndsAt when it ends (zero for an offer).
	Fee          int64
	WageShareBPS int
	TermDays     int
	EndsAt       time.Time
	// Shifts counts the finished shifts at the workshop; ExamShifts what the exam asks; ExtraXP the learning the contract gave.
	Shifts, ExamShifts int
	ExtraXP            int64
	// Certified says the exam was passed.
	Certified bool
	// CanAccept, CanExam, CanLeave, CanDismiss say what the viewer may do now.
	CanAccept, CanExam, CanLeave, CanDismiss bool
}

// ApprenticesView is the screen of one's apprenticeships: those as an apprentice, and those as a master.
type ApprenticesView struct {
	Lines []ApprenticeLine
	// Bounds are what an offer may ask (the offer form names them).
	Bounds *ApprenticeBounds
	// CanJoin lists the crafts the viewer could join at a public workshop of the settlement now (an NPC master teaches to the NPC level).
	CanJoin []ApprenticeJoinLine
	// Candidates are the residents a master with a workshop of his own may offer a place (not bound to a master now), to pick from instead of typing a code.
	Candidates []presentation.Named
	Notice  string
}

// ApprenticeJoinLine is a public workshop whose pool craftsman takes apprentices.
type ApprenticeJoinLine struct {
	Workshop presentation.Named
	Craft    string
	// UpTo is the craft level the NPC master teaches to.
	UpTo int
}

// TeacherEntry is one place a craft can be learned.
type TeacherEntry struct {
	// Kind is a TeacherKind*; Settlement where, KM how far; Teacher the person (empty for an NPC or the city's school).
	Kind       string
	Settlement presentation.Named
	KM         int
	Teacher    presentation.Named
	// CraftLevel and TeachingLevel are the teacher's skills; UpTo the level a student can reach under him.
	CraftLevel, TeachingLevel, UpTo int
	// Fee is the class fee; FreeSeats how many more he takes; Here says it is the viewer's own settlement.
	Fee       int64
	FreeSeats int
	Here      bool
}

// TeachersView is the directory of teachers of one craft.
type TeachersView struct {
	Craft   string
	Course  presentation.Named
	Entries []TeacherEntry
	// Crafts lists the crafts a directory can be asked for.
	Crafts []string
	// StartsAt is the day the teacher's skill starts to count, set only until then.
	StartsAt *time.Time `json:"starts_at"`
}

// ScreenApprenticeRefusal is a refused apprenticeship command.
const ScreenApprenticeRefusal = "apprentice_refusal"

// ApprenticeRefusalView is a refused apprenticeship command; Kind is the reason.
type ApprenticeRefusalView struct {
	Kind string
	// Bounds are what an offer may ask, set for the range refusal.
	Bounds *ApprenticeBounds
}

// ApprenticeBounds are the least and most an offer may ask: the term in days, the fee, and the share of the wage the apprentice keeps.
type ApprenticeBounds struct {
	MinDays, MaxDays, DefaultDays             int
	MaxFee                                    int64
	MinShareBPS, DefaultShareBPS, MaxShareBPS int
}

// ApprenticeRefusalCode is the refusal code of a kind.
func ApprenticeRefusalCode(kind string) string { return "apprentice_" + kind }

var screenApprenticeRefusal = presentation.Define[ApprenticeRefusalView](ScreenApprenticeRefusal, "life", presentation.Private(), presentation.Refusal())

// ApprenticeRefusal is a refused apprenticeship command.
func ApprenticeRefusal(c presentation.Ctx, v ApprenticeRefusalView) *presentation.Response {
	return screenApprenticeRefusal.Response(c.Lang, v, act(AddrApprentices).Named("apprentice.mine"), back(AddrHome)).Refused(ApprenticeRefusalCode(v.Kind), nil)
}

var (
	screenApprentices = presentation.Define[ApprenticesView](ScreenApprentices, "life", presentation.Private())
	screenTeachers    = presentation.Define[TeachersView](ScreenTeachers, "life", presentation.Private())
)

// Apprentices is the screen of one's apprenticeships.
func Apprentices(c presentation.Ctx, v ApprenticesView) *presentation.Response {
	var a []presentation.Action
	for _, l := range v.Lines {
		if l.CanAccept {
			a = append(a, act(AddrApprAccept, l.ID).Named("apprentice.accept").About(l.ID))
		}
		if l.CanExam {
			a = append(a, act(AddrApprExam, l.ID, "pass").Named("apprentice.exam").About(l.ID))
		}
		if l.CanLeave {
			a = append(a, act(AddrApprLeave, l.ID).Named("apprentice.leave").About(l.ID))
		}
		if l.CanDismiss {
			a = append(a, act(AddrApprDismiss, l.ID, "idle").Named("apprentice.dismiss").About(l.ID))
		}
	}
	for _, j := range v.CanJoin {
		a = append(a, act(AddrApprJoin, j.Workshop.Code).Named("apprentice.join").About(j.Workshop.Code))
	}
	a = append(a, back(AddrHome), refresh(AddrApprentices))
	return screenApprentices.Response(c.Lang, v, a...)
}

// Teachers is the directory of teachers of a craft.
func Teachers(c presentation.Ctx, v TeachersView) *presentation.Response {
	var a []presentation.Action
	for _, cr := range v.Crafts {
		if cr != v.Craft {
			a = append(a, act(AddrTeachers, cr).Named("education.teachers").About(cr))
		}
	}
	a = append(a, back(AddrHome), refresh(AddrTeachers, v.Craft))
	return screenTeachers.Response(c.Lang, v, a...)
}
