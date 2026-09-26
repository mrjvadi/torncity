package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Diplomacy (docs/adr/0022-military-and-diplomacy.md): the sanctions board
// and the flow that imposes and lifts a sanction; the treaties board and the
// flows that propose, answer and end a treaty; the public record; the
// refusal a sanction answers a blocked action with; and the lines a
// country's groups read.
//
// Sanctions and treaties are public business: the boards and the record read
// anywhere. Deciding is the office holder's, in their private chat. A measure
// is named diplomacy.measure.<code>, a ground diplomacy.ground.<code>, a kind
// of treaty diplomacy.treaty.<code>.

// Callback addresses of the diplomacy screens.
const (
	AddrSanctions = "diplomacy:sanctions"
	AddrImpose    = "diplomacy:impose"
	AddrLift      = "diplomacy:lift"
	AddrTreaties  = "diplomacy:treaties"
	AddrPropose   = "diplomacy:propose"
	AddrAnswer    = "diplomacy:answer"
	AddrEndTreaty = "diplomacy:end"
	AddrDipHist   = "diplomacy:history"
)

// Arguments diplomacy buttons carry.
const (
	// DiplomacyConfirm confirms a decision.
	DiplomacyConfirm = "yes"
	// ChooseGround moves the impose flow from its measures to its ground.
	ChooseGround = "-"
	// AnswerAccept and AnswerDecline answer a proposal.
	AnswerAccept  = "accept"
	AnswerDecline = "decline"
)

// MeasureName names a sanction measure.
func (c Context) MeasureName(code string) string { return c.named("diplomacy.measure."+code, code) }

// GroundName names the ground of a sanction.
func (c Context) GroundName(code string) string { return c.named("diplomacy.ground."+code, code) }

// TreatyName names a kind of treaty.
func (c Context) TreatyName(n Named) string { return c.named("diplomacy.treaty."+n.Code, n.Name) }

// measureList renders measures joined.
func (c Context) measureList(ms []string) string {
	out := ""
	for i, m := range ms {
		if i > 0 {
			out += c.T("gov.list_separator", nil)
		}
		out += c.MeasureName(m)
	}
	return out
}

// SanctionLine is one sanction on a board.
type SanctionLine struct {
	No       int64
	Imposer  GovPlace
	Target   GovPlace
	Measures []string
	Ground   string
	By       *GovPlayer
	Office   string
	// Since is how long it has stood; InForceIn how long until it binds,
	// zero once it does.
	Since     time.Duration
	InForceIn time.Duration
	// Liftable is set on the viewer's own when they may lift it now;
	// LiftableIn how long until they may.
	Liftable   bool
	LiftableIn time.Duration
}

// SanctionsView is a country's sanctions board.
type SanctionsView struct {
	Country GovPlace
	// Imposed are the country's sanctions on others; Suffered others' on
	// it.
	Imposed  []SanctionLine
	Suffered []SanctionLine
	// CanImpose is the viewer who decides the country's sanctions.
	CanImpose bool
	Notice    string
}

func sanctionLines(c Context, s SanctionLine, imposed bool) []string {
	other := s.Target
	key := "diplomacy.sanctions.line_on"
	if !imposed {
		other, key = s.Imposer, "diplomacy.sanctions.line_by"
	}
	lines := []string{c.T(key, map[string]any{"no": s.No, "country": c.PlaceName(other),
		"measures": c.measureList(s.Measures), "ground": c.GroundName(s.Ground)})}
	if s.InForceIn > 0 {
		lines = append(lines, c.T("diplomacy.sanctions.pending", map[string]any{"in": FormatSpan(c, s.InForceIn)}))
	} else {
		lines = append(lines, c.T("diplomacy.sanctions.since", map[string]any{"since": FormatSpan(c, s.Since)}))
	}
	if s.By != nil {
		lines = append(lines, c.T("diplomacy.sanctions.by", map[string]any{"office": c.OfficeName(s.Office),
			"player": c.govPlayer(s.By)}))
	}
	return lines
}

// Sanctions renders a country's sanctions board.
func Sanctions(c Context, v SanctionsView) *presenter.Response {
	return c.withView(renderSanctions(c, v), ScreenSanctions, v)
}

func renderSanctions(c Context, v SanctionsView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, c.T("diplomacy.sanctions.title", map[string]any{"country": c.PlaceName(v.Country)}))
	kb := keyboards.New()
	imposed := []string{c.T("diplomacy.sanctions.imposed", nil)}
	if len(v.Imposed) == 0 {
		imposed = append(imposed, c.T("diplomacy.sanctions.none", nil))
	}
	for _, s := range v.Imposed {
		imposed = append(imposed, sanctionLines(c, s, true)...)
		if v.CanImpose && !c.Shared {
			if s.Liftable {
				kb.Add(c.T("diplomacy.button.lift", map[string]any{"no": s.No, "country": c.PlaceName(s.Target)}),
					AddrLift, strconv.FormatInt(s.No, 10))
			} else if s.LiftableIn > 0 {
				imposed = append(imposed, c.T("diplomacy.sanctions.liftable_in", map[string]any{"in": FormatSpan(c, s.LiftableIn)}))
			}
		}
	}
	suffered := []string{c.T("diplomacy.sanctions.suffered", nil)}
	if len(v.Suffered) == 0 {
		suffered = append(suffered, c.T("diplomacy.sanctions.none", nil))
	}
	for _, s := range v.Suffered {
		suffered = append(suffered, sanctionLines(c, s, false)...)
	}
	blocks = append(blocks, body(imposed...), body(suffered...), c.T("diplomacy.sanctions.footer", nil))
	if v.CanImpose && !c.Shared {
		kb.Add(c.T("diplomacy.button.impose", nil), AddrImpose)
	}
	history, _ := keyboards.Button(c.T("diplomacy.button.history", nil), AddrDipHist, v.Country.Code)
	treaties, _ := keyboards.Button(c.T("diplomacy.button.treaties", nil), AddrTreaties, v.Country.Code)
	kb.Row(treaties, history)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrSanctions, v.Country.Code)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// MeasureToggle is one measure of the impose flow and whether it is chosen.
type MeasureToggle struct {
	Code string
	On   bool
	// Mask is the mask pressing it leads to.
	Mask int
}

// ImposeView is the impose flow: choose the target, then the measures, then
// the ground, then confirm.
type ImposeView struct {
	Country GovPlace
	// Targets are the countries to choose from, before one is chosen.
	Targets []GovPlace
	Target  *GovPlace
	Mask    int
	// Measures are the toggles, while choosing them.
	Measures []MeasureToggle
	// Chosen are the measures chosen, for the ground and confirm steps.
	Chosen []string
	// Grounds are offered once the measures are chosen; Ground is the one
	// chosen.
	Grounds []string
	Ground  string
	// Notice is how long the sanction waits before it binds, and the least
	// it stands.
	Notice, MinDuration time.Duration
}

// Impose renders the impose flow.
func Impose(c Context, v ImposeView) *presenter.Response {
	return c.withView(renderImpose(c, v), ScreenImpose, v)
}

func renderImpose(c Context, v ImposeView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("diplomacy.impose.title", map[string]any{"country": c.PlaceName(v.Country)})}
	back := keyboards.Data(AddrSanctions, v.Country.Code)
	mask := strconv.Itoa(v.Mask)
	switch {
	case v.Target == nil:
		lines = append(lines, c.T("diplomacy.impose.choose_target", nil))
		if len(v.Targets) == 0 {
			lines = append(lines, c.T("diplomacy.impose.no_targets", nil))
		}
		for _, t := range v.Targets {
			kb.Add(c.PlaceName(t), AddrImpose, t.Code)
		}
	case v.Ground == "" && len(v.Grounds) == 0:
		lines = append(lines, c.T("diplomacy.impose.choose_measures", map[string]any{"target": c.PlaceName(*v.Target)}))
		var buttons []presenter.Button
		for _, m := range v.Measures {
			key := "diplomacy.button.measure_off"
			if m.On {
				key = "diplomacy.button.measure_on"
			}
			if b, ok := keyboards.Button(c.T(key, map[string]any{"measure": c.MeasureName(m.Code)}), AddrImpose,
				v.Target.Code, strconv.Itoa(m.Mask)); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(2, buttons...)
		if v.Mask != 0 {
			kb.Add(c.T("diplomacy.button.next", nil), AddrImpose, v.Target.Code, mask, ChooseGround)
		}
		back = AddrImpose
	case v.Ground == "":
		lines = append(lines, c.T("diplomacy.impose.choose_ground", map[string]any{"target": c.PlaceName(*v.Target),
			"measures": c.measureList(v.Chosen)}))
		var buttons []presenter.Button
		for _, g := range v.Grounds {
			if b, ok := keyboards.Button(c.GroundName(g), AddrImpose, v.Target.Code, mask, g); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(2, buttons...)
		back = keyboards.Data(AddrImpose, v.Target.Code, mask)
	default:
		lines = append(lines, c.T("diplomacy.impose.confirm", map[string]any{"target": c.PlaceName(*v.Target),
			"measures": c.measureList(v.Chosen), "ground": c.GroundName(v.Ground),
			"notice": FormatSpan(c, v.Notice), "min": FormatSpan(c, v.MinDuration)}))
		kb.Add(c.T("diplomacy.button.confirm_impose", nil), AddrImpose, v.Target.Code, mask, v.Ground, DiplomacyConfirm)
		back = keyboards.Data(AddrImpose, v.Target.Code, mask, ChooseGround)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// LiftView asks the holder to confirm lifting a sanction.
type LiftView struct {
	Country  GovPlace
	Sanction SanctionLine
}

// Lift renders the confirmation of lifting a sanction.
func Lift(c Context, v LiftView) *presenter.Response {
	return c.withView(renderLift(c, v), ScreenLift, v)
}

func renderLift(c Context, v LiftView) *presenter.Response {
	s := v.Sanction
	kb := keyboards.New()
	kb.Add(c.T("diplomacy.button.confirm_lift", nil), AddrLift, strconv.FormatInt(s.No, 10), DiplomacyConfirm)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrSanctions, v.Country.Code)}))
	return c.respond(c.T("diplomacy.lift.confirm", map[string]any{"no": s.No, "target": c.PlaceName(s.Target),
		"measures": c.measureList(s.Measures)}), kb.Build())
}

// TreatyLine is one treaty on a board.
type TreatyLine struct {
	No    int64
	Kind  Named
	Other GovPlace
	// Status is the treaty's status at now (a proposal past its expiry is
	// expired); Incoming is a proposal made to the viewer's country.
	Status   string
	Incoming bool
	// ExpiresIn is how long a proposal has left; Since how long ago the
	// treaty reached its status.
	ExpiresIn time.Duration
	Since     time.Duration
}

// TreatiesView is a country's treaties board.
type TreatiesView struct {
	Country  GovPlace
	Treaties []TreatyLine
	// CanAct is the viewer who concludes the country's treaties.
	CanAct bool
	Notice string
}

// Treaties renders a country's treaties board.
func Treaties(c Context, v TreatiesView) *presenter.Response {
	return c.withView(renderTreaties(c, v), ScreenTreaties, v)
}

func renderTreaties(c Context, v TreatiesView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, c.T("diplomacy.treaties.title", map[string]any{"country": c.PlaceName(v.Country)}))
	kb := keyboards.New()
	act := v.CanAct && !c.Shared
	var lines []string
	for _, t := range v.Treaties {
		args := map[string]any{"no": t.No, "kind": c.TreatyName(t.Kind), "country": c.PlaceName(t.Other),
			"in": FormatSpan(c, t.ExpiresIn), "since": FormatSpan(c, t.Since)}
		no := strconv.FormatInt(t.No, 10)
		switch {
		case t.Status == "proposed" && t.Incoming:
			lines = append(lines, c.T("diplomacy.treaties.incoming", args))
			if act {
				accept, _ := keyboards.Button(c.T("diplomacy.button.accept", args), AddrAnswer, no, AnswerAccept)
				decline, _ := keyboards.Button(c.T("diplomacy.button.decline", args), AddrAnswer, no, AnswerDecline)
				kb.Row(accept, decline)
			}
		case t.Status == "proposed":
			lines = append(lines, c.T("diplomacy.treaties.outgoing", args))
			if act {
				kb.Add(c.T("diplomacy.button.withdraw", args), AddrEndTreaty, no)
			}
		case t.Status == "active":
			lines = append(lines, c.T("diplomacy.treaties.active", args))
			if act {
				kb.Add(c.T("diplomacy.button.terminate", args), AddrEndTreaty, no)
			}
		default:
			lines = append(lines, c.T("diplomacy.treaties.ended_"+t.Status, args))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("diplomacy.treaties.none", nil))
	}
	blocks = append(blocks, body(lines...), c.T("diplomacy.treaties.footer", nil))
	if act {
		kb.Add(c.T("diplomacy.button.propose", nil), AddrPropose)
	}
	sanctions, _ := keyboards.Button(c.T("diplomacy.button.sanctions", nil), AddrSanctions, v.Country.Code)
	history, _ := keyboards.Button(c.T("diplomacy.button.history", nil), AddrDipHist, v.Country.Code)
	kb.Row(sanctions, history)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrTreaties, v.Country.Code)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// ProposeView is the propose flow: choose the partner, then the kind, then
// confirm.
type ProposeView struct {
	Country  GovPlace
	Partners []GovPlace
	Partner  *GovPlace
	Kinds    []Named
	Kind     *Named
	// TTL is how long the partner has to answer.
	TTL time.Duration
}

// Propose renders the propose flow.
func Propose(c Context, v ProposeView) *presenter.Response {
	return c.withView(renderPropose(c, v), ScreenPropose, v)
}

func renderPropose(c Context, v ProposeView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("diplomacy.propose.title", map[string]any{"country": c.PlaceName(v.Country)})}
	back := keyboards.Data(AddrTreaties, v.Country.Code)
	switch {
	case v.Partner == nil:
		lines = append(lines, c.T("diplomacy.propose.choose_partner", nil))
		if len(v.Partners) == 0 {
			lines = append(lines, c.T("diplomacy.impose.no_targets", nil))
		}
		for _, p := range v.Partners {
			kb.Add(c.PlaceName(p), AddrPropose, p.Code)
		}
	case v.Kind == nil:
		lines = append(lines, c.T("diplomacy.propose.choose_kind", map[string]any{"partner": c.PlaceName(*v.Partner)}))
		for _, k := range v.Kinds {
			kb.Add(c.TreatyName(k), AddrPropose, v.Partner.Code, k.Code)
		}
		back = AddrPropose
	default:
		lines = append(lines, c.T("diplomacy.propose.confirm", map[string]any{"partner": c.PlaceName(*v.Partner),
			"kind": c.TreatyName(*v.Kind), "ttl": FormatSpan(c, v.TTL)}))
		kb.Add(c.T("diplomacy.button.confirm_propose", nil), AddrPropose, v.Partner.Code, v.Kind.Code, DiplomacyConfirm)
		back = keyboards.Data(AddrPropose, v.Partner.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// EndTreatyView asks the holder to confirm withdrawing a proposal or ending
// a treaty.
type EndTreatyView struct {
	Country GovPlace
	Treaty  TreatyLine
}

// EndTreaty renders the confirmation of withdrawing or ending a treaty.
func EndTreaty(c Context, v EndTreatyView) *presenter.Response {
	return c.withView(renderEndTreaty(c, v), ScreenEndTreaty, v)
}

func renderEndTreaty(c Context, v EndTreatyView) *presenter.Response {
	t := v.Treaty
	key, button := "diplomacy.end.confirm_terminate", "diplomacy.button.confirm_terminate"
	if t.Status == "proposed" {
		key, button = "diplomacy.end.confirm_withdraw", "diplomacy.button.confirm_withdraw"
	}
	kb := keyboards.New()
	kb.Add(c.T(button, nil), AddrEndTreaty, strconv.FormatInt(t.No, 10), DiplomacyConfirm)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrTreaties, v.Country.Code)}))
	return c.respond(c.T(key, map[string]any{"no": t.No, "kind": c.TreatyName(t.Kind), "country": c.PlaceName(t.Other)}),
		kb.Build())
}

// DiplomacyEntry is one line of the public record.
type DiplomacyEntry struct {
	// Kind is the event (application.Event*).
	Kind     string
	Country  GovPlace
	Other    GovPlace
	Measures []string
	Ground   string
	Treaty   Named
	No       int64
	By       *GovPlayer
	Office   string
	Ago      time.Duration
}

// DiplomacyHistoryView is one page of the public record.
type DiplomacyHistoryView struct {
	Country GovPlace
	Entries []DiplomacyEntry
	Page    int
	Pages   int
}

// DiplomacyHistory renders the public record.
func DiplomacyHistory(c Context, v DiplomacyHistoryView) *presenter.Response {
	return c.withView(renderDiplomacyHistory(c, v), ScreenDiplomacyHistory, v)
}

func renderDiplomacyHistory(c Context, v DiplomacyHistoryView) *presenter.Response {
	lines := []string{c.T("diplomacy.history.title", map[string]any{"country": c.PlaceName(v.Country)})}
	if len(v.Entries) == 0 {
		lines = append(lines, c.T("diplomacy.history.empty", nil))
	}
	for _, e := range v.Entries {
		lines = append(lines, c.T("diplomacy.history."+e.Kind, map[string]any{"country": c.PlaceName(e.Country),
			"other": c.PlaceName(e.Other), "measures": c.measureList(e.Measures), "ground": c.GroundName(e.Ground),
			"kind": c.TreatyName(e.Treaty), "no": e.No}))
		if e.By != nil {
			lines = append(lines, c.T("diplomacy.history.by", map[string]any{"office": c.OfficeName(e.Office),
				"player": c.govPlayer(e.By), "ago": FormatSpan(c, e.Ago)}))
		}
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrSanctions, v.Country.Code),
		Prefix: keyboards.Data(AddrDipHist, v.Country.Code), Page: v.Page, HasPrev: v.Page > 1, HasNext: v.Page < v.Pages,
		RefreshData: keyboards.Data(AddrDipHist, v.Country.Code, strconv.Itoa(max(v.Page, 1)))}))
	return c.respond(body(lines...), kb.Build())
}

// Diplomacy refusals.
const (
	DiplomacyRefusedNotFound  = "not_found"
	DiplomacyRefusedNotHolder = "not_holder"
	DiplomacyRefusedSelf      = "self"
	DiplomacyRefusedStanding  = "standing"
	DiplomacyRefusedTooSoon   = "too_soon"
	DiplomacyRefusedOpen      = "open"
	DiplomacyRefusedState     = "state"
	DiplomacyRefusedNoCountry = "no_country"
)

// DiplomacyRefusalView is a refused diplomacy command.
type DiplomacyRefusalView struct {
	Kind    string
	Country GovPlace
	Office  string
	// In is how long until it becomes possible (too_soon).
	In   time.Duration
	Back []string
}

// DiplomacyRefusal renders a refused diplomacy command.
func DiplomacyRefusal(c Context, v DiplomacyRefusalView) *presenter.Response {
	return c.withView(renderDiplomacyRefusal(c, v), ScreenDiplomacyRefusal, v)
}

func renderDiplomacyRefusal(c Context, v DiplomacyRefusalView) *presenter.Response {
	kb := keyboards.New()
	back := AddrGovCity
	if v.Country.Code != "" {
		back = keyboards.Data(AddrSanctions, v.Country.Code)
	}
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("diplomacy.refused."+v.Kind, map[string]any{"country": c.PlaceName(v.Country),
		"office": c.OfficeName(v.Office), "in": FormatSpan(c, v.In)}), kb.Build())
}

// SanctionBlockedView is a cross-border action a sanction blocked.
type SanctionBlockedView struct {
	Measure string
	Imposer GovPlace
	Target  GovPlace
	Back    []string
}

// SanctionBlocked renders the refusal every blocked cross-border action
// answers with: which measure of whose sanction on whom.
func SanctionBlocked(c Context, v SanctionBlockedView) *presenter.Response {
	return c.withView(renderSanctionBlocked(c, v), ScreenSanctionBlocked, v)
}

func renderSanctionBlocked(c Context, v SanctionBlockedView) *presenter.Response {
	kb := keyboards.New()
	sanctions, _ := keyboards.Button(c.T("diplomacy.button.sanctions", nil), AddrSanctions, v.Imposer.Code)
	kb.Row(sanctions)
	back := AddrHome
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("diplomacy.blocked."+v.Measure, map[string]any{"imposer": c.PlaceName(v.Imposer),
		"target": c.PlaceName(v.Target)}), kb.Build())
}

// TreatyNoticeView is a private notice of diplomacy: a proposal made to the
// holder's country.
type TreatyNoticeView struct {
	Country GovPlace
	Other   GovPlace
	Kind    Named
	No      int64
	TTL     time.Duration
}

// TreatyProposedNotice tells the partner's foreign minister of a proposal.
func TreatyProposedNotice(c Context, v TreatyNoticeView) *presenter.Response {
	return c.withView(renderTreatyProposedNotice(c, v), ScreenTreatyProposedNotice, v)
}

func renderTreatyProposedNotice(c Context, v TreatyNoticeView) *presenter.Response {
	kb := keyboards.New()
	no := strconv.FormatInt(v.No, 10)
	args := map[string]any{"no": v.No, "kind": c.TreatyName(v.Kind), "country": c.PlaceName(v.Other), "in": FormatSpan(c, v.TTL)}
	accept, _ := keyboards.Button(c.T("diplomacy.button.accept", args), AddrAnswer, no, AnswerAccept)
	decline, _ := keyboards.Button(c.T("diplomacy.button.decline", args), AddrAnswer, no, AnswerDecline)
	kb.Row(accept, decline)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrTreaties, v.Country.Code)}))
	return c.respond(c.T("diplomacy.notice.proposed", args), kb.Build())
}

// SanctionImposedAnnouncement is a line in both countries' groups.
func SanctionImposedAnnouncement(c Context, imposer, target GovPlace, measures []string, ground string) string {
	return c.T("diplomacy.announce.imposed", map[string]any{"imposer": c.PlaceName(imposer), "target": c.PlaceName(target),
		"measures": c.measureList(measures), "ground": c.GroundName(ground)})
}

// SanctionLiftedAnnouncement is a line in both countries' groups.
func SanctionLiftedAnnouncement(c Context, imposer, target GovPlace) string {
	return c.T("diplomacy.announce.lifted", map[string]any{"imposer": c.PlaceName(imposer), "target": c.PlaceName(target)})
}

// TreatySignedAnnouncement is a line in both countries' groups.
func TreatySignedAnnouncement(c Context, a, b GovPlace, kind Named) string {
	return c.T("diplomacy.announce.signed", map[string]any{"a": c.PlaceName(a), "b": c.PlaceName(b), "kind": c.TreatyName(kind)})
}

// TreatyEndedAnnouncement is a line in both countries' groups: a party
// ended a treaty.
func TreatyEndedAnnouncement(c Context, by, other GovPlace, kind Named) string {
	return c.T("diplomacy.announce.terminated", map[string]any{"by": c.PlaceName(by), "other": c.PlaceName(other),
		"kind": c.TreatyName(kind)})
}
