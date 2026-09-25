package content

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/achievement"
)

// This file holds achievements (configs/content/achievements.yml;
// docs/adr/0024-property-and-politics.md): what each counts, how many, and
// the cash it pays once. The rules are internal/domain/achievement; the
// day's caps are configuration (achievements.*).

// ErrInvalidAchievementContent means achievements.yml is unusable.
var ErrInvalidAchievementContent = errors.New("content: invalid achievement content")

// AchievementDef is one achievement.
type AchievementDef struct {
	Code   string `yaml:"code" json:"code"`
	Name   string `yaml:"name" json:"name"`
	Event  string `yaml:"event" json:"event"`
	Count  int64  `yaml:"count" json:"count"`
	Reward int64  `yaml:"reward,omitempty" json:"reward,omitempty"`
}

// Achievement is the domain's value.
func (d AchievementDef) Achievement() achievement.Achievement {
	return achievement.Achievement{Code: d.Code, Event: d.Event, Count: d.Count, Reward: d.Reward}
}

// validateAchievements checks achievements.yml.
func (p *Pack) validateAchievements(problems *[]error) {
	seen := map[string]bool{}
	for i, a := range p.Achievements {
		where := fmt.Sprintf("achievements[%d] %q", i, a.Code)
		if !transportCodePattern.MatchString(a.Code) || seen[a.Code] {
			*problems = append(*problems, fmt.Errorf("%w: %s: code is not a code or repeated", ErrInvalidAchievementContent, where))
		}
		seen[a.Code] = true
		if a.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		if err := a.Achievement().Validate(); err != nil {
			*problems = append(*problems, fmt.Errorf("%w: %s: %v", ErrInvalidAchievementContent, where, err))
		}
	}
}

// Achievements lists the achievements, in file order.
func (s *Snapshot) Achievements() []AchievementDef {
	return append([]AchievementDef(nil), s.achievements...)
}

// AchievementDef finds one achievement by code.
func (s *Snapshot) AchievementDef(code string) (AchievementDef, bool) {
	for _, a := range s.achievements {
		if a.Code == code {
			return a, true
		}
	}
	return AchievementDef{}, false
}
