package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// War (docs/adr/0022-military-and-diplomacy.md, part two): the war board —
// who is at war with whom, the ceasefires and peaces on the table, the cities
// occupied and damaged, the latest operations told in bands — and the flows
// that declare a war, join an ally's, propose, answer and resume; the war
// room where a commander picks a target and launches an operation with an
// estimate of its odds; the operation report, exact, for the office holders;
// and the lines the groups read.
//
// What anyone may read is the board, in bands: a strike's damage is «light»
// or «heavy», its losses «a few» or «a squadron», never a count. The exact
// report goes privately to the commander who launched it and to the
// defender's head of state. A war's ground is war.ground.<code>, a damage
// band war.damage.<code>, an operation war.op.<kind>, an objective
// war.objective.<code>.

// Callback addresses of the war screens.
const (
	AddrWarBoard   = "war:board"
	AddrWarDeclare = "war:declare"
	AddrWarJoin    = "war:join"
	AddrWarPropose = "war:propose"
	AddrWarAnswer  = "war:answer"
	AddrWarResume  = "war:resume"
	AddrWarRoom    = "war:room"
	AddrWarTarget  = "war:target"
	AddrWarLaunch  = "war:launch"
)

// WarConfirm confirms a decision of war.
const WarConfirm = "yes"

// WarAllUnits is the class argument of a ground assault: every ground unit
// of the garrison goes.
const WarAllUnits = "all"

// WarGroundName names the ground a war was declared on.
func (c Context) WarGroundName(code string) string { return c.named("war.ground."+code, code) }

// DamageBandName names a band of damage.
func (c Context) DamageBandName(code string) string { return c.named("war.damage."+code, code) }

// OperationName names a kind of operation.
func (c Context) OperationName(kind string) string { return c.named("war.op."+kind, kind) }

// ObjectiveName names an operation's objective.
func (c Context) ObjectiveName(code string) string { return c.named("war.objective."+code, code) }

// ProposalLine is a ceasefire or a peace on the table.
type ProposalLine struct {
	No   int64
	Kind string
	// Other is the principal on the other side; Incoming a proposal made
	// to the viewer's country.
	Other     GovPlace
	Incoming  bool
	ExpiresIn time.Duration
}

// WarLine is one war on the board.
type WarLine struct {
	No                 int64
	Attacker, Defender GovPlace
	// Allies of each side that joined.
	AttackerAllies, DefenderAllies []GovPlace
	Ground                         string
	// Status is declared, active, ceasefire or ended; ActiveIn and
	// ActiveAt when a declared war may be fought.
	Status   string
	ActiveIn time.Duration
	ActiveAt time.Time
	Since    time.Duration
	// Broke is a declaration that broke a treaty between the two.
	Broke     bool
	Proposals []ProposalLine
	// For the viewer: may propose a ceasefire or a peace, resume after a
	// ceasefire, answer an incoming proposal.
	CanPropose, CanResume bool
}

// JoinLine is a war the viewer's country may join beside an ally.
type JoinLine struct {
	WarNo int64
	Ally  GovPlace
	Enemy GovPlace
}

// OccupationLine is a city held by a country the content does not put it in.
type OccupationLine struct {
	CityCode, City string
	Controller     GovPlace
	DeJure         GovPlace
	Since          time.Duration
}

// DamageLine is a damaged city, in a band.
type DamageLine struct {
	CityCode, City string
	Band           string
	ClosedIn       time.Duration
}

// OperationLine is an operation on the board: told in bands in public.
type OperationLine struct {
	No             int64
	Kind           string
	Objective      string
	Country        GovPlace
	CityCode, City string
	Target         GovPlace
	// Pending is an operation under way: StrikesIn to go.
	Pending   bool
	StrikesIn time.Duration
	CalledOff bool
	// Bands of what it did: the damage, each side's losses.
	DamageBand    string
	LostBand      string
	EnemyLostBand string
	Captured      bool
	Ago           time.Duration
}

// WarBoardView is the war board of a country.
type WarBoardView struct {
	Country    GovPlace
	Wars       []WarLine
	Joinable   []JoinLine
	Occupied   []OccupationLine
	Damaged    []DamageLine
	Operations []OperationLine
	// CanDeclare is the head of state (or acting for one); CanCommand an
	// office holder who opens the war room.
	CanDeclare, CanCommand bool
	Notice                 string
}

// warStatusKey words a war's status.
func warLine(c Context, w WarLine) []string {
	args := map[string]any{"no": w.No, "attacker": c.PlaceName(w.Attacker), "defender": c.PlaceName(w.Defender),
		"ground": c.WarGroundName(w.Ground), "in": FormatDuration(c, w.ActiveIn), "at": FormatClock(c, w.ActiveAt),
		"since": FormatSpan(c, w.Since)}
	lines := []string{c.T("war.board.war_"+w.Status, args)}
	if w.Broke {
		lines = append(lines, c.T("war.board.broke", nil))
	}
	if len(w.AttackerAllies) > 0 {
		lines = append(lines, c.T("war.board.allies", map[string]any{"side": c.PlaceName(w.Attacker),
			"allies": c.placeList(w.AttackerAllies)}))
	}
	if len(w.DefenderAllies) > 0 {
		lines = append(lines, c.T("war.board.allies", map[string]any{"side": c.PlaceName(w.Defender),
			"allies": c.placeList(w.DefenderAllies)}))
	}
	for _, p := range w.Proposals {
		pa := map[string]any{"no": p.No, "kind": c.T("war.proposal."+p.Kind, nil), "country": c.PlaceName(p.Other),
			"in": FormatSpan(c, p.ExpiresIn)}
		key := "war.board.proposal_out"
		if p.Incoming {
			key = "war.board.proposal_in"
		}
		lines = append(lines, c.T(key, pa))
	}
	return lines
}

// placeList joins places.
func (c Context) placeList(ps []GovPlace) string {
	out := ""
	for i, p := range ps {
		if i > 0 {
			out += c.T("gov.list_separator", nil)
		}
		out += c.PlaceName(p)
	}
	return out
}

// operationLine renders an operation on the board.
func operationLine(c Context, o OperationLine) string {
	args := map[string]any{"no": o.No, "op": c.OperationName(o.Kind), "country": c.PlaceName(o.Country),
		"city": c.CityName(o.CityCode, o.City), "target": c.PlaceName(o.Target), "in": FormatDuration(c, o.StrikesIn),
		"ago": FormatSpan(c, o.Ago), "damage": c.DamageBandName(o.DamageBand), "lost": c.BandName(o.LostBand),
		"enemy_lost": c.BandName(o.EnemyLostBand), "objective": c.ObjectiveName(o.Objective)}
	switch {
	case o.Pending:
		return c.T("war.board.op_pending", args)
	case o.CalledOff:
		return c.T("war.board.op_called_off", args)
	case o.Captured:
		return c.T("war.board.op_captured", args)
	}
	line := c.T("war.board.op_done", args)
	if o.DamageBand != "" {
		line += c.T("war.board.op_damage", args)
	}
	if o.LostBand != "" || o.EnemyLostBand != "" {
		line += c.T("war.board.op_losses", map[string]any{"lost": c.lossBand(o.LostBand),
			"enemy_lost": c.lossBand(o.EnemyLostBand)})
	}
	return line
}

// lossBand words a band of losses, «none» for no loss.
func (c Context) lossBand(band string) string {
	if band == "" {
		return c.T("war.band_none", nil)
	}
	return c.BandName(band)
}

// WarBoard renders the war board.
func WarBoard(c Context, v WarBoardView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, c.T("war.board.title", map[string]any{"country": c.PlaceName(v.Country)}))
	kb := keyboards.New()
	act := !c.Shared
	if len(v.Wars) == 0 {
		blocks = append(blocks, c.T("war.board.peace", nil))
	}
	for _, w := range v.Wars {
		blocks = append(blocks, body(warLine(c, w)...))
		if !act {
			continue
		}
		no := strconv.FormatInt(w.No, 10)
		for _, p := range w.Proposals {
			if p.Incoming && w.CanPropose {
				pn := strconv.FormatInt(p.No, 10)
				args := map[string]any{"no": p.No, "kind": c.T("war.proposal."+p.Kind, nil)}
				accept, _ := keyboards.Button(c.T("war.button.accept", args), AddrWarAnswer, pn, AnswerAccept)
				decline, _ := keyboards.Button(c.T("war.button.decline", args), AddrWarAnswer, pn, AnswerDecline)
				kb.Row(accept, decline)
			}
		}
		if w.CanPropose {
			var row []presenter.Button
			if w.Status == "active" || w.Status == "declared" {
				if b, ok := keyboards.Button(c.T("war.button.ceasefire", map[string]any{"no": w.No}), AddrWarPropose, no,
					"ceasefire"); ok {
					row = append(row, b)
				}
			}
			if b, ok := keyboards.Button(c.T("war.button.peace", map[string]any{"no": w.No}), AddrWarPropose, no, "peace"); ok {
				row = append(row, b)
			}
			kb.Row(row...)
		}
		if w.CanResume {
			kb.Add(c.T("war.button.resume", map[string]any{"no": w.No}), AddrWarResume, no)
		}
	}
	if len(v.Joinable) > 0 {
		lines := []string{c.T("war.board.joinable", nil)}
		for _, j := range v.Joinable {
			args := map[string]any{"no": j.WarNo, "ally": c.PlaceName(j.Ally), "enemy": c.PlaceName(j.Enemy)}
			lines = append(lines, c.T("war.board.join_line", args))
			if act && v.CanDeclare {
				kb.Add(c.T("war.button.join", args), AddrWarJoin, strconv.FormatInt(j.WarNo, 10))
			}
		}
		blocks = append(blocks, body(lines...))
	}
	if len(v.Occupied) > 0 {
		lines := []string{c.T("war.board.occupied", nil)}
		for _, o := range v.Occupied {
			lines = append(lines, c.T("war.board.occupied_line", map[string]any{"city": c.CityName(o.CityCode, o.City),
				"controller": c.PlaceName(o.Controller), "country": c.PlaceName(o.DeJure), "since": FormatSpan(c, o.Since)}))
		}
		blocks = append(blocks, body(lines...))
	}
	if len(v.Damaged) > 0 {
		lines := []string{c.T("war.board.damaged", nil)}
		for _, d := range v.Damaged {
			args := map[string]any{"city": c.CityName(d.CityCode, d.City), "band": c.DamageBandName(d.Band),
				"in": FormatDuration(c, d.ClosedIn)}
			line := c.T("war.board.damaged_line", args)
			if d.ClosedIn > 0 {
				line += c.T("war.board.closed", args)
			}
			lines = append(lines, line)
		}
		blocks = append(blocks, body(lines...))
	}
	if len(v.Operations) > 0 {
		lines := []string{c.T("war.board.operations", nil)}
		for _, o := range v.Operations {
			lines = append(lines, operationLine(c, o))
		}
		blocks = append(blocks, body(lines...))
	}
	blocks = append(blocks, c.T("war.board.footer", nil))
	if act && v.CanCommand {
		kb.Add(c.T("war.button.room", nil), AddrWarRoom)
	}
	if act && v.CanDeclare {
		kb.Add(c.T("war.button.declare", nil), AddrWarDeclare)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrWarBoard, v.Country.Code)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// DeclareView is the flow that declares a war: the country, the ground,
// then confirm.
type DeclareView struct {
	Country GovPlace
	Targets []GovPlace
	Target  *GovPlace
	Grounds []string
	Ground  string
	// Notice is how long until the war may be fought (real time).
	Notice time.Duration
	// Breaks are the treaties with the target the declaration ends; Allies
	// the target's allies who will be told.
	Breaks []Named
	Allies []GovPlace
}

// Declare renders the declaration flow.
func Declare(c Context, v DeclareView) *presenter.Response {
	lines := []string{c.T("war.declare.title", map[string]any{"country": c.PlaceName(v.Country)})}
	kb := keyboards.New()
	back := keyboards.Data(AddrWarBoard, v.Country.Code)
	switch {
	case v.Target == nil:
		lines = append(lines, c.T("war.declare.choose_target", nil))
		if len(v.Targets) == 0 {
			lines = append(lines, c.T("war.declare.no_targets", nil))
		}
		var buttons []presenter.Button
		for _, t := range v.Targets {
			if b, ok := keyboards.Button(c.PlaceName(t), AddrWarDeclare, t.Code); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(2, buttons...)
	case v.Ground == "":
		lines = append(lines, c.T("war.declare.choose_ground", map[string]any{"target": c.PlaceName(*v.Target)}))
		for _, g := range v.Grounds {
			kb.Add(c.WarGroundName(g), AddrWarDeclare, v.Target.Code, g)
		}
		back = AddrWarDeclare
	default:
		args := map[string]any{"target": c.PlaceName(*v.Target), "ground": c.WarGroundName(v.Ground),
			"in": FormatSpan(c, v.Notice)}
		lines = append(lines, c.T("war.declare.confirm", args))
		for _, t := range v.Breaks {
			lines = append(lines, c.T("war.declare.breaks", map[string]any{"kind": c.TreatyName(t)}))
		}
		if len(v.Allies) > 0 {
			lines = append(lines, c.T("war.declare.allies", map[string]any{"allies": c.placeList(v.Allies)}))
		}
		kb.Add(c.T("war.button.confirm_declare", nil), AddrWarDeclare, v.Target.Code, v.Ground, WarConfirm)
		back = keyboards.Data(AddrWarDeclare, v.Target.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// WarDecisionView confirms joining a war, proposing a ceasefire or a peace,
// or resuming a war.
type WarDecisionView struct {
	// Kind is join, ceasefire, peace or resume.
	Kind    string
	Country GovPlace
	WarNo   int64
	// Other is the principal on the other side; Ally the one joined.
	Other  GovPlace
	Ally   GovPlace
	Notice time.Duration
	TTL    time.Duration
}

// WarDecision renders the confirmation of a decision of war.
func WarDecision(c Context, v WarDecisionView) *presenter.Response {
	no := strconv.FormatInt(v.WarNo, 10)
	args := map[string]any{"no": v.WarNo, "other": c.PlaceName(v.Other), "ally": c.PlaceName(v.Ally),
		"in": FormatSpan(c, v.Notice), "ttl": FormatSpan(c, v.TTL)}
	kb := keyboards.New()
	switch v.Kind {
	case "join":
		kb.Add(c.T("war.button.confirm_join", nil), AddrWarJoin, no, WarConfirm)
	case "resume":
		kb.Add(c.T("war.button.confirm_resume", nil), AddrWarResume, no, WarConfirm)
	default:
		kb.Add(c.T("war.button.confirm_propose", nil), AddrWarPropose, no, v.Kind, WarConfirm)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrWarBoard, v.Country.Code)}))
	return c.respond(c.T("war.decide."+v.Kind, args), kb.Build())
}

// RoomTarget is an enemy city in the war room.
type RoomTarget struct {
	CityCode, City string
	Country        GovPlace
	WarNo          int64
	// DistanceKM is by road from the nearest garrison of ours.
	DistanceKM int64
	DamageBand string
}

// WarRoomView is the war room: the enemy's cities and what is under way.
type WarRoomView struct {
	Country GovPlace
	Targets []RoomTarget
	Running []OperationLine
	// Readiness is ours, in bps.
	Readiness int64
	Notice    string
}

// WarRoom renders the war room.
func WarRoom(c Context, v WarRoomView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, body(c.T("war.room.title", map[string]any{"country": c.PlaceName(v.Country)}),
		c.T("military.forces.readiness", map[string]any{"value": c.T("gov.percent",
			map[string]any{"value": PercentFromBPS(c, int(v.Readiness))})})))
	kb := keyboards.New()
	lines := []string{c.T("war.room.targets", nil)}
	if len(v.Targets) == 0 {
		lines = append(lines, c.T("war.room.no_targets", nil))
	}
	var buttons []presenter.Button
	for _, t := range v.Targets {
		args := map[string]any{"city": c.CityName(t.CityCode, t.City), "country": c.PlaceName(t.Country),
			"km": FormatNumber(c, t.DistanceKM), "damage": c.DamageBandName(t.DamageBand)}
		line := c.T("war.room.target", args)
		if t.DamageBand != "" {
			line += c.T("war.room.target_damage", args)
		}
		lines = append(lines, line)
		if b, ok := keyboards.Button(c.T("war.button.target", args), AddrWarTarget, t.CityCode); ok {
			buttons = append(buttons, b)
		}
	}
	blocks = append(blocks, body(lines...))
	kb.Grid(2, buttons...)
	if len(v.Running) > 0 {
		run := []string{c.T("war.room.running", nil)}
		for _, o := range v.Running {
			run = append(run, operationLine(c, o))
		}
		blocks = append(blocks, body(run...))
	}
	blocks = append(blocks, c.T("war.room.footer", nil))
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrWarBoard, v.Country.Code), RefreshData: AddrWarRoom}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// ForceOption is one operation the viewer's forces could fly or drive at a
// target: the class, how many are ready at the garrison in reach that has
// most, and whether the viewer commands them.
type ForceOption struct {
	Kind           string
	Class          Named
	Ready          int64
	FromCode, From string
	DistanceKM     int64
	// Munitions are the bombs at that garrison (an air strike).
	Munitions int64
	// CanLaunch is the viewer commanding the branch; Office the office
	// that does.
	CanLaunch bool
	Office    string
}

// target is the option's class argument.
func (o ForceOption) target() string {
	if o.Kind == "ground" {
		return WarAllUnits
	}
	return o.Class.Code
}

// WarTargetView is one enemy city and the operations in reach of it.
type WarTargetView struct {
	Country  GovPlace
	Target   RoomTarget
	Options  []ForceOption
	Occupied *OccupationLine
}

// WarTarget renders a target.
func WarTarget(c Context, v WarTargetView) *presenter.Response {
	t := v.Target
	args := map[string]any{"city": c.CityName(t.CityCode, t.City), "country": c.PlaceName(t.Country),
		"damage": c.DamageBandName(t.DamageBand)}
	lines := []string{c.T("war.target.title", args)}
	if t.DamageBand != "" {
		lines = append(lines, c.T("war.target.damage", args))
	}
	if v.Occupied != nil {
		lines = append(lines, c.T("war.target.occupied", map[string]any{"country": c.PlaceName(v.Occupied.DeJure)}))
	}
	kb := keyboards.New()
	opts := []string{c.T("war.target.options", nil)}
	if len(v.Options) == 0 {
		opts = append(opts, c.T("war.target.none", nil))
	}
	for _, o := range v.Options {
		oa := map[string]any{"op": c.OperationName(o.Kind), "class": c.ForceClassName(o.Class),
			"count": FormatNumber(c, o.Ready), "from": c.CityName(o.FromCode, o.From), "km": FormatNumber(c, o.DistanceKM),
			"munitions": FormatNumber(c, o.Munitions), "office": c.OfficeName(o.Office)}
		line := c.T("war.target.option", oa)
		if o.Kind == "air" {
			line += c.T("war.target.option_munitions", oa)
		}
		if !o.CanLaunch {
			line += c.T("war.target.option_office", oa)
		}
		opts = append(opts, line)
		if o.CanLaunch && !c.Shared {
			kb.Add(c.T("war.button.launch", oa), AddrWarLaunch, t.CityCode, o.Kind, o.target())
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrWarRoom, RefreshData: keyboards.Data(AddrWarTarget, t.CityCode)}))
	return c.respond(paragraphs(body(lines...), body(opts...), c.T("war.target.footer", nil)), kb.Build())
}

// Estimate is a commander's estimate of an operation, averaged over the
// content's dice and told in bands: an intelligence estimate, not a count.
type Estimate struct {
	// Chance is likely, even or unlikely: of any warhead arriving (a
	// strike), or of taking the city (an assault).
	Chance string
	// LossBand is our expected losses; DamageBand the expected damage.
	LossBand   string
	DamageBand string
}

// LaunchView is launching an operation: the objective, how many, then the
// estimate and confirm.
type LaunchView struct {
	Country    GovPlace
	Target     RoomTarget
	Option     ForceOption
	Objectives []string
	Objective  string
	Quantities []int64
	Qty        int64
	Confirm    bool
	Prepare    time.Duration
	Munitions  int64
	Estimate   *Estimate
}

// WarLaunch renders the launch flow.
func WarLaunch(c Context, v LaunchView) *presenter.Response {
	o, t := v.Option, v.Target
	args := map[string]any{"op": c.OperationName(o.Kind), "class": c.ForceClassName(o.Class),
		"city": c.CityName(t.CityCode, t.City), "from": c.CityName(o.FromCode, o.From), "km": FormatNumber(c, o.DistanceKM),
		"count": FormatNumber(c, v.Qty), "ready": FormatNumber(c, o.Ready), "time": FormatDuration(c, v.Prepare),
		"objective": c.ObjectiveName(v.Objective), "munitions": FormatNumber(c, v.Munitions)}
	lines := []string{c.T("war.launch.title", args)}
	kb := keyboards.New()
	back := keyboards.Data(AddrWarTarget, t.CityCode)
	switch {
	case v.Objective == "":
		lines = append(lines, c.T("war.launch.choose_objective", args))
		for _, obj := range v.Objectives {
			kb.Add(c.ObjectiveName(obj), AddrWarLaunch, t.CityCode, o.Kind, o.target(), obj)
		}
	case !v.Confirm:
		lines = append(lines, c.T("war.launch.choose_qty", args))
		var buttons []presenter.Button
		for _, n := range v.Quantities {
			if b, ok := keyboards.Button(c.T("military.button.qty", map[string]any{"count": FormatNumber(c, n)}), AddrWarLaunch,
				t.CityCode, o.Kind, o.target(), v.Objective, strconv.FormatInt(n, 10)); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(4, buttons...)
		back = keyboards.Data(AddrWarLaunch, t.CityCode, o.Kind, o.target())
	default:
		lines = append(lines, c.T("war.launch.confirm_"+o.Kind, args))
		if o.Kind == "air" {
			lines = append(lines, c.T("war.launch.munitions", args))
		}
		if e := v.Estimate; e != nil {
			lines = append(lines, "", c.T("war.launch.estimate", nil),
				c.T("war.launch.chance_"+o.Kind, map[string]any{"chance": c.T("war.chance."+e.Chance, nil)}),
				c.T("war.launch.losses", map[string]any{"lost": c.lossBand(e.LossBand)}))
			if o.Kind != "ground" {
				damage := c.T("war.band_none", nil)
				if e.DamageBand != "" {
					damage = c.DamageBandName(e.DamageBand)
				}
				lines = append(lines, c.T("war.launch.damage_"+v.Objective, map[string]any{"damage": damage}))
			}
			lines = append(lines, c.T("war.launch.estimate_note", nil))
		}
		kb.Add(c.T("war.button.confirm_launch", nil), AddrWarLaunch, t.CityCode, o.Kind, o.target(), v.Objective,
			strconv.FormatInt(v.Qty, 10), WarConfirm)
		if o.Kind != "ground" {
			// An assault has no objective or count to choose: back is the
			// target.
			back = keyboards.Data(AddrWarLaunch, t.CityCode, o.Kind, o.target(), v.Objective)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// StrikeReportView is an operation's report, exact: for the commander who
// launched it and the defender's head of state, privately.
type StrikeReportView struct {
	No             int64
	Kind           string
	Objective      string
	Country        GovPlace
	Target         GovPlace
	CityCode, City string
	Class          Named
	// Ours is the report as the attacker reads it; otherwise as the
	// defender does.
	Ours       bool
	CalledOff  bool
	Committed  int64
	Lost       int64
	Damaged    int64
	EnemyLost  int64
	EnemyDmg   int64
	SeenAtKM   int64
	Fired      int64
	Munitions  int64
	Hits       int64
	DamageBPS  int64
	DamageBand string
	Captured   bool
	Liberated  bool
}

// StrikeReport renders an operation's report.
func StrikeReport(c Context, v StrikeReportView) *presenter.Response {
	args := map[string]any{"no": v.No, "op": c.OperationName(v.Kind), "objective": c.ObjectiveName(v.Objective),
		"country": c.PlaceName(v.Country), "target": c.PlaceName(v.Target), "city": c.CityName(v.CityCode, v.City),
		"class": c.ForceClassName(v.Class), "committed": FormatNumber(c, v.Committed), "lost": FormatNumber(c, v.Lost),
		"damaged": FormatNumber(c, v.Damaged), "enemy_lost": FormatNumber(c, v.EnemyLost),
		"enemy_damaged": FormatNumber(c, v.EnemyDmg), "km": FormatNumber(c, v.SeenAtKM), "fired": FormatNumber(c, v.Fired),
		"munitions": FormatNumber(c, v.Munitions), "hits": FormatNumber(c, v.Hits),
		"damage": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.DamageBPS))}),
		"band":   c.DamageBandName(v.DamageBand)}
	side := "defender"
	if v.Ours {
		side = "attacker"
	}
	lines := []string{c.T("war.report.title_"+side, args)}
	if v.CalledOff {
		lines = append(lines, c.T("war.report.called_off", args))
	} else {
		lines = append(lines, c.T("war.report.committed", args))
		if v.Kind != "ground" {
			if v.SeenAtKM > 0 {
				lines = append(lines, c.T("war.report.seen_at", args))
			} else {
				lines = append(lines, c.T("war.report.unseen", args))
			}
			lines = append(lines, c.T("war.report.fired", args))
		}
		switch v.Kind {
		case "missile":
			// A missile is spent either way: the report counts it
			// intercepted or on target.
			lines = append(lines, c.T("war.report.intercepted", args), c.T("war.report.missile_hits", args))
			if v.EnemyLost > 0 || v.EnemyDmg > 0 {
				lines = append(lines, c.T("war.report.enemy_losses", args))
			}
		case "air":
			lines = append(lines, c.T("war.report.losses", args), c.T("war.report.enemy_losses", args),
				c.T("war.report.hits", args))
		default:
			lines = append(lines, c.T("war.report.losses", args), c.T("war.report.enemy_losses", args))
		}
		if v.DamageBPS > 0 {
			lines = append(lines, c.T("war.report.damage", args))
		}
		switch {
		case v.Liberated:
			lines = append(lines, c.T("war.report.liberated", args))
		case v.Captured:
			lines = append(lines, c.T("war.report.captured", args))
		case v.Kind == "ground":
			lines = append(lines, c.T("war.report.held", args))
		}
	}
	lines = append(lines, "", c.T("war.report.secret", nil))
	kb := keyboards.New()
	if v.Ours {
		kb.Add(c.T("war.button.room", nil), AddrWarRoom)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrWarBoard, v.Country.Code)}))
	return c.respond(body(lines...), kb.Build())
}

// WarNoticeView is a private notice of war.
type WarNoticeView struct {
	// Kind is struck (to the players in a struck city), ally (an ally was
	// attacked), proposal (a ceasefire or peace offered) or declared (war
	// declared on the player's country, to its head of state).
	Kind           string
	Country        GovPlace
	Other          GovPlace
	Ally           GovPlace
	CityCode, City string
	WarNo          int64
	ProposalNo     int64
	ProposalKind   string
	Band           string
	In             time.Duration
	// Injury is what a strike did to the player, nil for nothing
	// (docs/adr/0023).
	Injury *InjuryView
}

// WarNotice renders a private notice of war.
func WarNotice(c Context, v WarNoticeView) *presenter.Response {
	args := map[string]any{"country": c.PlaceName(v.Country), "other": c.PlaceName(v.Other), "ally": c.PlaceName(v.Ally),
		"city": c.CityName(v.CityCode, v.City), "no": v.WarNo, "pno": v.ProposalNo,
		"kind": c.T("war.proposal."+v.ProposalKind, nil), "band": c.DamageBandName(v.Band), "in": FormatSpan(c, v.In)}
	kb := keyboards.New()
	switch v.Kind {
	case "ally":
		kb.Add(c.T("war.button.join", args), AddrWarJoin, strconv.FormatInt(v.WarNo, 10))
	case "proposal":
		pn := strconv.FormatInt(v.ProposalNo, 10)
		pa := map[string]any{"no": v.ProposalNo, "kind": args["kind"]}
		accept, _ := keyboards.Button(c.T("war.button.accept", pa), AddrWarAnswer, pn, AnswerAccept)
		decline, _ := keyboards.Button(c.T("war.button.decline", pa), AddrWarAnswer, pn, AnswerDecline)
		kb.Row(accept, decline)
	}
	if v.Injury != nil && v.Injury.Hospital {
		kb.Add(c.T("health.button.hospital", nil), AddrHospital)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrWarBoard, v.Country.Code)}))
	text := c.T("war.notice."+v.Kind, args)
	if v.Injury != nil {
		text = paragraphs(text, body(c.T("health.injury.strike", nil), c.injuryLines(v.Injury)))
	}
	return c.respond(text, kb.Build())
}

// War refusals.
const (
	WarRefusedNotHolder  = "not_holder"
	WarRefusedNoCountry  = "no_country"
	WarRefusedNotFound   = "not_found"
	WarRefusedSelf       = "self"
	WarRefusedAtWar      = "at_war"
	WarRefusedState      = "state"
	WarRefusedNotEnemy   = "not_enemy"
	WarRefusedNotYet     = "not_yet"
	WarRefusedNoForces   = "no_forces"
	WarRefusedNoMunition = "no_munitions"
	WarRefusedOpen       = "open"
	WarRefusedNoAlly     = "no_ally"
	WarRefusedStock      = "stock"
)

// WarRefusalView is a refused decision of war.
type WarRefusalView struct {
	Kind    string
	Country GovPlace
	Office  string
	In      time.Duration
	Max     int64
	Back    []string
}

// WarRefusal renders a refused decision of war.
func WarRefusal(c Context, v WarRefusalView) *presenter.Response {
	kb := keyboards.New()
	back := AddrWarBoard
	if v.Country.Code != "" {
		back = keyboards.Data(AddrWarBoard, v.Country.Code)
	}
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("war.refused."+v.Kind, map[string]any{"country": c.PlaceName(v.Country),
		"office": c.OfficeName(v.Office), "in": FormatDuration(c, v.In), "max": FormatNumber(c, v.Max)}), kb.Build())
}

// WarBlockedView is a journey the war closes.
type WarBlockedView struct {
	// Border is a closed border between From and To; otherwise the city
	// is closed after a strike for In.
	Border         bool
	From, To       GovPlace
	CityCode, City string
	In             time.Duration
	Back           []string
}

// WarBlocked renders a journey the war closes.
func WarBlocked(c Context, v WarBlockedView) *presenter.Response {
	kb := keyboards.New()
	back := AddrHome
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	args := map[string]any{"from": c.PlaceName(v.From), "to": c.PlaceName(v.To), "city": c.CityName(v.CityCode, v.City),
		"in": FormatDuration(c, v.In)}
	key := "war.blocked.city"
	if v.Border {
		key = "war.blocked.border"
	}
	return c.respond(c.T(key, args), kb.Build())
}

// WarDeclaredAnnouncement: a war declared, in both countries' groups.
func WarDeclaredAnnouncement(c Context, attacker, defender GovPlace, ground string, in time.Duration, broke bool) string {
	args := map[string]any{"attacker": c.PlaceName(attacker), "defender": c.PlaceName(defender),
		"ground": c.WarGroundName(ground), "in": FormatSpan(c, in)}
	line := c.T("war.announce.declared", args)
	if broke {
		line += c.T("war.announce.broke", args)
	}
	return line
}

// WarJoinedAnnouncement: an ally joined a war.
func WarJoinedAnnouncement(c Context, ally, beside, enemy GovPlace) string {
	return c.T("war.announce.joined", map[string]any{"ally": c.PlaceName(ally), "beside": c.PlaceName(beside),
		"enemy": c.PlaceName(enemy)})
}

// WarSettledAnnouncement: a ceasefire agreed, a peace signed, or a war
// resumed after a ceasefire (kind ceasefire, peace, resumed).
func WarSettledAnnouncement(c Context, kind string, a, b GovPlace, in time.Duration) string {
	return c.T("war.announce."+kind, map[string]any{"a": c.PlaceName(a), "b": c.PlaceName(b), "in": FormatSpan(c, in)})
}

// StrikeAnnouncement: an operation struck a city, told in bands. result is
// city (the city was hit; damageBand says how hard), defences (its air
// defences were hit), repelled (nothing got through) or held (an assault
// that did not take the city).
func StrikeAnnouncement(c Context, attacker GovPlace, kind, result, cityCode, city string, target GovPlace,
	damageBand string,
) string {
	args := map[string]any{"attacker": c.PlaceName(attacker), "op": c.OperationName(kind),
		"city": c.CityName(cityCode, city), "target": c.PlaceName(target), "damage": c.DamageBandName(damageBand)}
	return c.T("war.announce.strike_"+result, args)
}

// CityTakenAnnouncement: a city changed hands (captured, or liberated by
// its own country).
func CityTakenAnnouncement(c Context, liberated bool, by GovPlace, cityCode, city string, from GovPlace) string {
	args := map[string]any{"by": c.PlaceName(by), "city": c.CityName(cityCode, city), "from": c.PlaceName(from)}
	if liberated {
		return c.T("war.announce.liberated", args)
	}
	return c.T("war.announce.captured", args)
}
