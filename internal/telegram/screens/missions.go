package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Missions (docs/adr/0023-health-missions-factions.md): a city's boards and
// their missions, one mission, the player's missions and their progress, and
// the notice of a mission completed.

// Addresses of the mission screens.
const (
	AddrMissionBoard   = "mission:board"
	AddrMission        = "mission:view"
	AddrMissionAccept  = "mission:accept"
	AddrMissions       = "mission:mine"
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

// targetName names a target in this context's language.
func (c Context) targetName(t MissionTarget) string {
	switch t.Kind {
	case TargetCity:
		return c.CityName(t.Code, t.Name)
	case TargetCareer:
		return c.CareerName(t.Code, t.Name)
	case TargetCareerCategory:
		return c.named("career_category."+t.Code, t.Name)
	case TargetCourse:
		return c.CourseName(t.Code, t.Name)
	case TargetItem:
		return c.ItemName(Named{Code: t.Code, Name: t.Name})
	case TargetItemCategory:
		return c.named("item_category."+t.Code, t.Name)
	case TargetCrime:
		return c.CrimeName(Named{Code: t.Code, Name: t.Name})
	case TargetCrimeCategory:
		return c.CrimeCategoryName(Named{Code: t.Code, Name: t.Name})
	}
	return t.Name
}

// MissionName names a mission.
func (c Context) MissionName(n Named) string { return c.named("mission."+n.Code+".name", n.Name) }

// missionBrief is a mission's one line of story, empty when it has none.
func (c Context) missionBrief(n Named) string {
	key := "mission." + n.Code + ".brief"
	if s := c.T(key, nil); s != key {
		return s
	}
	return ""
}

// MissionBoardRef names a board and where it stands.
type MissionBoardRef struct {
	Code  string
	Name  string
	Place Named
}

func (c Context) boardName(b MissionBoardRef) string { return c.named("mission_board."+b.Code, b.Name) }

// MissionObjective is one objective, with its progress.
type MissionObjective struct {
	Kind        string
	Target      MissionTarget
	Count, Done int64
}

// objectiveLine is one objective as a line.
func (c Context) objectiveLine(o MissionObjective, progress bool) string {
	key := "mission.objective." + o.Kind
	if o.Target.Code == "" {
		key += "_any"
	}
	line := c.T(key, map[string]any{"target": c.targetName(o.Target), "count": FormatNumber(c, o.Count)})
	if !progress {
		return c.T("mission.objective_todo", map[string]any{"objective": line})
	}
	if o.Done >= o.Count {
		return c.T("mission.objective_done", map[string]any{"objective": line})
	}
	return c.T("mission.objective_progress", map[string]any{"objective": line, "done": FormatNumber(c, o.Done),
		"count": FormatNumber(c, o.Count)})
}

// MissionReward is what completing a mission gives.
type MissionReward struct {
	Cash  int64
	XP    int64
	Items []LootLine
}

func (c Context) rewardLine(r MissionReward) string {
	var parts []string
	if r.Cash > 0 {
		parts = append(parts, FormatMoney(c, r.Cash))
	}
	if r.XP > 0 {
		parts = append(parts, c.T("mission.reward_xp", map[string]any{"xp": FormatNumber(c, r.XP)}))
	}
	for _, it := range r.Items {
		parts = append(parts, c.T("mission.reward_item", map[string]any{"item": c.ItemName(it.Item), "qty": FormatNumber(c, it.Qty)}))
	}
	return c.T("mission.reward", map[string]any{"reward": joinWith(c, parts)})
}

// MissionLine is one mission on a board.
type MissionLine struct {
	Mission    Named
	Reward     MissionReward
	Blocked    string
	Wait       time.Duration
	Repeatable bool
}

// blockedLine says why a mission cannot be taken now, empty when it can.
func (c Context) blockedLine(why string, wait time.Duration, level, max int) string {
	if why == "" {
		return ""
	}
	return c.T("mission.blocked."+why, map[string]any{"wait": FormatDuration(c, wait),
		"level": FormatNumber(c, int64(level)), "max": FormatNumber(c, int64(max))})
}

// MissionBoardView is a city's boards, or one board and its missions.
type MissionBoardView struct {
	CityCode, City string
	Boards         []MissionBoardRef
	Board          *MissionBoardRef
	// Here says the player stands at the board: they may take a mission.
	Here     bool
	Missions []MissionLine
}

// MissionBoard renders a city's boards, or one board.
func MissionBoard(c Context, v MissionBoardView) *presenter.Response {
	kb := keyboards.New()
	city := c.CityName(v.CityCode, v.City)
	if v.Board == nil {
		lines := []string{c.T("mission.boards_title", map[string]any{"city": city})}
		for _, b := range v.Boards {
			lines = append(lines, c.T("mission.board_line", map[string]any{"board": c.boardName(b), "place": c.SpotName(b.Place)}))
			kb.Add(c.T("mission.button.board", map[string]any{"board": c.boardName(b)}), AddrMissionBoard, b.Code)
		}
		if len(v.Boards) == 0 {
			lines = append(lines, c.T("mission.boards_none", nil))
		}
		kb.Add(c.T("mission.button.mine", nil), AddrMissions)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrMissionBoard}))
		return c.respond(body(lines...), kb.Build())
	}
	b := *v.Board
	lines := []string{c.T("mission.board_title", map[string]any{"board": c.boardName(b), "city": city})}
	if !v.Here {
		lines = append(lines, c.T("mission.board_elsewhere", map[string]any{"place": c.SpotName(b.Place)}))
	}
	var list []string
	for _, m := range v.Missions {
		line := c.T("mission.line", map[string]any{"mission": c.MissionName(m.Mission), "reward": c.rewardLine(m.Reward)})
		if why := c.blockedLine(m.Blocked, m.Wait, 0, 0); why != "" && m.Blocked != "level" && m.Blocked != "too_many" {
			line = body(line, "  "+why)
		}
		list = append(list, line)
		kb.Add(c.T("mission.button.view", map[string]any{"mission": c.MissionName(m.Mission)}), AddrMission, m.Mission.Code)
	}
	if len(list) == 0 {
		list = append(list, c.T("mission.board_empty", nil))
	}
	kb.Add(c.T("mission.button.mine", nil), AddrMissions)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMissionBoard, RefreshData: keyboards.Data(AddrMissionBoard, b.Code)}))
	return c.respond(paragraphs(body(lines...), body(list...)), kb.Build())
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

// Mission renders one mission.
func Mission(c Context, v MissionView) *presenter.Response {
	kb := keyboards.New()
	if v.Abandoning {
		no := strconv.FormatInt(v.No, 10)
		kb.Add(c.T("mission.button.abandon_confirm", nil), AddrMissionAbandon, no, MissionYes)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrMissions}))
		return c.respond(c.T("mission.abandon_confirm", map[string]any{"mission": c.MissionName(v.Mission)}), kb.Build()).MarkPrivate()
	}
	title := c.T("mission.title", map[string]any{"mission": c.MissionName(v.Mission)})
	lines := []string{c.missionBrief(v.Mission)}
	for _, o := range v.Objectives {
		lines = append(lines, c.objectiveLine(o, false))
	}
	facts := []string{c.rewardLine(v.Reward),
		c.T("mission.where", map[string]any{"board": c.boardName(v.Board), "place": c.SpotName(v.Board.Place)})}
	if v.MinLevel > 1 {
		facts = append(facts, c.T("mission.min_level", map[string]any{"level": FormatNumber(c, int64(v.MinLevel))}))
	}
	if len(v.Requires) > 0 {
		var names []string
		for _, r := range v.Requires {
			names = append(names, c.MissionName(r))
		}
		facts = append(facts, c.T("mission.requires", map[string]any{"missions": joinWith(c, names)}))
	}
	if v.TimeLimit > 0 {
		facts = append(facts, c.T("mission.time_limit", map[string]any{"duration": FormatDuration(c, v.TimeLimit)}))
	}
	if v.Repeatable {
		facts = append(facts, c.T("mission.repeatable", map[string]any{"duration": FormatDuration(c, v.Cooldown)}))
	}
	why := c.blockedLine(v.Blocked, v.Wait, v.MinLevel, v.Max)
	if v.Blocked == "" {
		kb.Add(c.T("mission.button.accept", nil), AddrMissionAccept, v.Mission.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMissionBoard, v.Board.Code)}))
	return c.respond(paragraphs(title, body(lines...), body(facts...), why), kb.Build())
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

// MissionsMine renders the player's missions.
func MissionsMine(c Context, v MissionsMineView) *presenter.Response {
	kb := keyboards.New()
	var notice string
	if n := v.Notice; n != nil {
		notice = c.T("mission.notice."+n.Kind, map[string]any{"mission": c.MissionName(n.Mission),
			"qty": FormatNumber(c, n.Qty), "cash": FormatMoney(c, n.Cash), "xp": FormatNumber(c, n.XP)})
		if n.Kind == MissionNoticeCompleted && n.Withheld > 0 {
			notice = body(notice, c.T("mission.withheld", map[string]any{"amount": FormatMoney(c, n.Withheld)}))
		}
	}
	var active []string
	for _, a := range v.Active {
		lines := []string{c.T("mission.mine_line", map[string]any{"mission": c.MissionName(a.Mission)})}
		for _, o := range a.Objectives {
			lines = append(lines, "  "+c.objectiveLine(o, true))
		}
		switch {
		case a.Status == application.MissionExpired:
			lines = append(lines, "  "+c.T("mission.expired", nil))
		case a.Left > 0:
			lines = append(lines, "  "+c.T("mission.left", map[string]any{"duration": FormatDuration(c, a.Left)}))
		}
		active = append(active, body(lines...))
		no := strconv.FormatInt(a.No, 10)
		var row []presenter.Button
		if a.Deliver && a.Status == application.MissionActive {
			if b, ok := keyboards.Button(c.T("mission.button.deliver", map[string]any{"mission": c.MissionName(a.Mission)}),
				AddrMissionDeliver, no); ok {
				row = append(row, b)
			}
		}
		if b, ok := keyboards.Button(c.T("mission.button.abandon", map[string]any{"mission": c.MissionName(a.Mission)}),
			AddrMissionAbandon, no); ok {
			row = append(row, b)
		}
		kb.Row(row...)
	}
	current := c.T("mission.mine_none", nil)
	if len(active) > 0 {
		current = paragraphs(active...)
	}
	var recent []string
	for _, r := range v.Recent {
		key := "mission.recent." + r.Status
		recent = append(recent, c.T(key, map[string]any{"mission": c.MissionName(r.Mission), "cash": FormatMoney(c, r.Cash)}))
	}
	var past string
	if len(recent) > 0 {
		past = body(append([]string{c.T("mission.recent_title", nil)}, recent...)...)
	}
	kb.Add(c.T("mission.button.boards", nil), AddrMissionBoard)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrMissions}))
	return c.respond(paragraphs(notice, c.T("mission.mine_title", map[string]any{"count": FormatNumber(c, int64(len(v.Active))),
		"max": FormatNumber(c, int64(v.Max))}), current, past), kb.Build()).MarkPrivate()
}

// MissionCompletedView is a mission completed, as its notice tells it.
type MissionCompletedView struct {
	Mission            Named
	Cash, Withheld, XP int64
	Items              []LootLine
}

// MissionCompletedNotice tells a player a mission is complete and paid.
func MissionCompletedNotice(c Context, v MissionCompletedView) *presenter.Response {
	lines := []string{c.T("mission.completed", map[string]any{"mission": c.MissionName(v.Mission)})}
	if v.Cash > 0 {
		lines = append(lines, c.T("mission.paid", map[string]any{"cash": FormatMoney(c, v.Cash)}))
	}
	if v.Withheld > 0 {
		lines = append(lines, c.T("mission.withheld", map[string]any{"amount": FormatMoney(c, v.Withheld)}))
	}
	if v.XP > 0 {
		lines = append(lines, c.T("crime.result.xp", map[string]any{"xp": FormatNumber(c, v.XP)}))
	}
	for _, it := range v.Items {
		lines = append(lines, c.T("mission.got_item", map[string]any{"item": c.ItemName(it.Item), "qty": FormatNumber(c, it.Qty)}))
	}
	kb := keyboards.New()
	kb.Add(c.T("mission.button.mine", nil), AddrMissions)
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
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

// MissionRefusal renders a refused mission request.
func MissionRefusal(c Context, v MissionRefusalView) *presenter.Response {
	text := c.T("mission.refused."+v.Kind, nil)
	if v.Kind == MissionRefusedBlocked {
		text = c.blockedLine(v.Blocked, v.Wait, v.Level, v.Max)
	}
	kb := keyboards.New()
	kb.Add(c.T("mission.button.mine", nil), AddrMissions)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMissionBoard}))
	return c.respond(text, kb.Build())
}
