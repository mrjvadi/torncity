package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/mission"
)

// This file holds the content of missions (configs/content/missions.yml;
// docs/adr/0023-health-missions-factions.md): the boards a city posts
// missions on, each at a place, and the missions — what each asks for, what
// it gives, who may take it and how often. The rules are
// internal/domain/mission.

// ErrInvalidMissionContent means the missions content is unusable.
var ErrInvalidMissionContent = errors.New("content: invalid mission content")

// MissionBoardDef is a board a city posts missions on.
type MissionBoardDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback for mission_board.<code>.
	Name string `yaml:"name" json:"name"`
	// Place is where the board stands (places.yml): a mission is taken, and
	// goods are handed in, there.
	Place string `yaml:"place" json:"place"`
}

// ObjectiveDef is one objective (mission.Objective).
type ObjectiveDef struct {
	Kind   string `yaml:"kind" json:"kind"`
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	Count  int64  `yaml:"count" json:"count"`
}

// ItemQtyDef is units of a good.
type ItemQtyDef struct {
	Item string `yaml:"item" json:"item"`
	Qty  int64  `yaml:"qty" json:"qty"`
}

// MissionRewardDef is what completing a mission gives.
type MissionRewardDef struct {
	Cash  int64        `yaml:"cash,omitempty" json:"cash,omitempty"`
	XP    int64        `yaml:"xp,omitempty" json:"xp,omitempty"`
	Items []ItemQtyDef `yaml:"items,omitempty" json:"items,omitempty"`
}

// MissionDef is one mission.
type MissionDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback for mission.<code>.name.
	Name  string `yaml:"name" json:"name"`
	Board string `yaml:"board" json:"board"`
	// Cities narrows the cities whose board posts it; omitted, every city
	// with the board's place.
	Cities     []string       `yaml:"cities,omitempty" json:"cities,omitempty"`
	MinLevel   int            `yaml:"min_level,omitempty" json:"min_level,omitempty"`
	Requires   []string       `yaml:"requires,omitempty" json:"requires,omitempty"`
	Objectives []ObjectiveDef `yaml:"objectives" json:"objectives"`
	// Repeat is "once" (the default) or "repeat"; a repeatable mission is
	// taken again Cooldown (GAME time) after it was completed.
	Repeat    string           `yaml:"repeat,omitempty" json:"repeat,omitempty"`
	Cooldown  string           `yaml:"cooldown,omitempty" json:"cooldown,omitempty"`
	TimeLimit string           `yaml:"time_limit,omitempty" json:"time_limit,omitempty"`
	Reward    MissionRewardDef `yaml:"reward" json:"reward"`
}

// Repeat values.
const (
	RepeatOnce  = "once"
	RepeatAgain = "repeat"
)

// Mission is the domain's value; the pack has been validated.
func (d MissionDef) Mission() mission.Mission {
	cooldown, _ := optionalDuration(d.Cooldown)
	limit, _ := optionalDuration(d.TimeLimit)
	m := mission.Mission{Code: d.Code, Board: d.Board, MinLevel: d.MinLevel, Requires: append([]string(nil), d.Requires...),
		Repeatable: d.Repeat == RepeatAgain, Cooldown: cooldown, TimeLimit: limit,
		Reward: mission.Reward{Cash: d.Reward.Cash, XP: d.Reward.XP}}
	for _, o := range d.Objectives {
		m.Objectives = append(m.Objectives, mission.Objective{Kind: mission.Kind(o.Kind), Target: o.Target, Count: o.Count})
	}
	for _, it := range d.Reward.Items {
		m.Reward.Items = append(m.Reward.Items, mission.ItemReward{Item: it.Item, Qty: it.Qty})
	}
	return m
}

// missionContent is the snapshot's missions.
type missionContent struct {
	boards []MissionBoardDef
	defs   []MissionDef
	byCode map[string]MissionDef
}

func (s *Snapshot) buildMissions(p *Pack) {
	s.missions = missionContent{boards: append([]MissionBoardDef(nil), p.MissionBoards...),
		defs: append([]MissionDef(nil), p.Missions...), byCode: map[string]MissionDef{}}
	for _, m := range p.Missions {
		s.missions.byCode[m.Code] = m
	}
}

// MissionBoards lists the boards, in file order.
func (s *Snapshot) MissionBoards() []MissionBoardDef {
	return append([]MissionBoardDef(nil), s.missions.boards...)
}

// MissionBoard returns one board.
func (s *Snapshot) MissionBoard(code string) (MissionBoardDef, bool) {
	for _, b := range s.missions.boards {
		if b.Code == code {
			return b, true
		}
	}
	return MissionBoardDef{}, false
}

// Missions lists every mission, in file order.
func (s *Snapshot) Missions() []MissionDef { return append([]MissionDef(nil), s.missions.defs...) }

// MissionDef returns one mission.
func (s *Snapshot) MissionDef(code string) (MissionDef, bool) {
	d, ok := s.missions.byCode[code]
	return d, ok
}

// BoardMissions lists the missions a board posts in a city, in file order.
func (s *Snapshot) BoardMissions(board, cityCode string) []MissionDef {
	var out []MissionDef
	for _, m := range s.missions.defs {
		if m.Board == board && (len(m.Cities) == 0 || contains(m.Cities, cityCode)) {
			out = append(out, m)
		}
	}
	return out
}

// validateMissions checks missions.yml.
func (p *Pack) validateMissions(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidMissionContent, fmt.Sprintf(format, args...)))
	}
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	boards := map[string]bool{}
	for i, b := range p.MissionBoards {
		if !transportCodePattern.MatchString(b.Code) || boards[b.Code] {
			bad("mission_boards[%d] %q is not a code or repeated", i, b.Code)
		}
		boards[b.Code] = true
		if !places[b.Place] {
			bad("mission_boards[%d] %q: place %q is not a place of places.yml", i, b.Code, b.Place)
		}
		if b.Name == "" {
			bad("mission_boards[%d] %q has no name", i, b.Code)
		}
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	careers, categories := map[string]bool{}, map[string]bool{}
	for _, c := range p.Careers {
		careers[c.Code], categories[c.Category] = true, true
	}
	courses := map[string]bool{}
	for _, c := range p.Courses {
		courses[c.Code] = true
	}
	items, itemCats := map[string]bool{}, map[string]bool{}
	for _, it := range p.Items {
		items[it.Code], itemCats[it.Category] = true, true
	}
	crimes, crimeCats := map[string]bool{}, map[string]bool{}
	for _, c := range p.Crimes {
		crimes[c.Code] = true
	}
	for _, c := range p.CrimeCategories {
		crimeCats[c.Code] = true
	}
	codes := map[string]bool{}
	for _, m := range p.Missions {
		codes[m.Code] = true
	}
	seen := map[string]bool{}
	for i, m := range p.Missions {
		where := fmt.Sprintf("missions[%d] %q", i, m.Code)
		if !transportCodePattern.MatchString(m.Code) || seen[m.Code] {
			bad("%s is not a code or repeated", where)
		}
		seen[m.Code] = true
		if m.Name == "" {
			bad("%s has no name", where)
		}
		if !boards[m.Board] {
			bad("%s: board %q is not a mission board", where, m.Board)
		}
		for _, c := range m.Cities {
			if !cities[c] {
				bad("%s: city %q does not exist", where, c)
			}
		}
		switch m.Repeat {
		case "", RepeatOnce, RepeatAgain:
		default:
			bad("%s: repeat %q is not once or repeat", where, m.Repeat)
		}
		for _, raw := range []string{m.Cooldown, m.TimeLimit} {
			if _, err := optionalDuration(raw); err != nil {
				bad("%s: %q is not a duration", where, raw)
			}
		}
		if m.Repeat == RepeatAgain {
			if d, _ := optionalDuration(m.Cooldown); d < time.Minute {
				bad("%s: a repeatable mission needs a cooldown of at least a minute", where)
			}
		}
		for _, r := range m.Requires {
			if !codes[r] {
				bad("%s: requires %q, no such mission", where, r)
			}
		}
		for j, o := range m.Objectives {
			ok := o.Target == ""
			switch mission.Kind(o.Kind) {
			case mission.Travel:
				ok = ok || cities[o.Target]
			case mission.Work:
				ok = ok || careers[o.Target] || categories[o.Target]
			case mission.Course:
				ok = ok || courses[o.Target]
			case mission.Buy, mission.Sell, mission.Deliver:
				ok = ok || items[o.Target]
			case mission.Use:
				ok = ok || items[o.Target] || itemCats[o.Target]
			case mission.Crime:
				ok = ok || crimes[o.Target] || crimeCats[o.Target]
			}
			if !ok {
				bad("%s objectives[%d]: %s target %q is nothing of that kind", where, j, o.Kind, o.Target)
			}
		}
		for _, it := range m.Reward.Items {
			if !items[it.Item] {
				bad("%s: reward item %q does not exist", where, it.Item)
			}
		}
		if err := m.Mission().Validate(); err != nil {
			bad("%v", err)
		}
	}
}
