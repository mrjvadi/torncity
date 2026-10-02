package military

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/society"
)

// The shared pieces of a view, as the core names them.
type (
	// Named is a content entry: its code and its authored name.
	Named = presentation.Named
	// GovPlace is a city or a country, by code.
	GovPlace = presentation.GovPlace
	// Good names what a stock of arms or a listing is: a good or a design.
	Good = presentation.Good
	// CompanyRef names a company, its code and its type.
	CompanyRef = presentation.CompanyRef
	// GovOffice is one office and who sits in it.
	GovOffice = society.GovOffice
)

// AttributeLine is one computed attribute of a design, in its unit's base
// number: the name keys the attribute (and so its unit), the value is data.
type AttributeLine struct {
	Name  string
	Value int64
	// Observable attributes are what a buyer sees on the market.
	Observable bool
}

// InjuryLine is what a strike did to a player: what it took, where it left
// them, and whether it put them in hospital.
type InjuryLine struct {
	Damage, Health, Max int
	Hospital            bool
	EndsAt              time.Time
}

// Notice codes: what just happened, shown on the screen the player is brought
// back to.
const (
	// NoticeStationStarted: equipment is on its way to a garrison (Count of
	// Good, to City, taking Time).
	NoticeStationStarted = "station_started"
	// NoticeBuyDone: arms were bought (Count of Good for Total).
	NoticeBuyDone = "buy_done"
	// NoticeDeclareDone: a war was declared on Target, fightable after Time.
	NoticeDeclareDone = "declare_done"
	// NoticeResumeDone: a war was resumed, fightable after Time.
	NoticeResumeDone = "resume_done"
	// NoticeLaunchDone: an operation of Kind was ordered against City and
	// strikes after Time.
	NoticeLaunchDone = "launch_done"
	// NoticeJoinDone: the country joined a war beside Target.
	NoticeJoinDone = "join_done"
	// NoticeProposeDone: a proposal of Kind (ceasefire or peace) was made to
	// Target.
	NoticeProposeDone = "propose_done"
	// NoticeAnswerAccept and NoticeAnswerDecline: a proposal was answered.
	NoticeAnswerAccept  = "answer_accept"
	NoticeAnswerDecline = "answer_decline"
)

// Notice is what just happened, as a code with its facts; the edge words it.
type Notice struct {
	Code  string
	Count int64
	Good  Good
	// CityCode and City name the city it was about.
	CityCode, City string
	// Time is a real-time span (a move, a notice, a preparation).
	Time time.Duration
	// Total is money, minor units.
	Total int64
	// Kind is an operation's kind, or a proposal's.
	Kind   string
	Target GovPlace
}
