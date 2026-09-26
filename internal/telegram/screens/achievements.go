package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Achievements (docs/adr/0024-property-and-politics.md): what a player has
// earned and how far they are toward the rest; the notice of one earned; and
// the count on the home screen.

// AddrAchievements is the achievements screen.
const AddrAchievements = "achievement:list"

// AchievementName names an achievement.
func (c Context) AchievementName(n Named) string {
	return c.named("achievement."+n.Code+".name", n.Name)
}

// AchievementLine is one achievement and the player's progress.
type AchievementLine struct {
	Achievement Named
	Count, Done int64
	Reward      int64
	Earned      bool
	// Cash is what earning it paid.
	Cash int64
}

// AchievementsView is the player's achievements.
type AchievementsView struct {
	Lines []AchievementLine
}

// Achievements renders the player's achievements.
func Achievements(c Context, v AchievementsView) *presenter.Response {
	return c.withView(renderAchievements(c, v), ScreenAchievements, v)
}

func renderAchievements(c Context, v AchievementsView) *presenter.Response {
	kb := keyboards.New()
	var earned, open []string
	for _, l := range v.Lines {
		args := map[string]any{"achievement": c.AchievementName(l.Achievement), "done": FormatNumber(c, l.Done),
			"count": FormatNumber(c, l.Count), "reward": FormatMoney(c, l.Reward)}
		switch {
		case l.Earned:
			earned = append(earned, c.T("achievement.earned_line", args))
		case l.Reward > 0:
			open = append(open, c.T("achievement.open_line_reward", args))
		default:
			open = append(open, c.T("achievement.open_line", args))
		}
	}
	if len(earned) == 0 {
		earned = append(earned, c.T("achievement.none_yet", nil))
	}
	head := c.T("achievement.title", map[string]any{"earned": FormatNumber(c, int64(countEarned(v.Lines))),
		"all": FormatNumber(c, int64(len(v.Lines)))})
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrAchievements}))
	return c.respond(paragraphs(head, body(earned...), body(open...)), kb.Build())
}

func countEarned(lines []AchievementLine) int {
	n := 0
	for _, l := range lines {
		if l.Earned {
			n++
		}
	}
	return n
}

// AchievementNoticeView is an achievement just earned.
type AchievementNoticeView struct {
	Achievement    Named
	Cash, Withheld int64
}

// AchievementNotice tells a player they earned an achievement.
func AchievementNotice(c Context, v AchievementNoticeView) *presenter.Response {
	return c.withView(renderAchievementNotice(c, v), ScreenAchievementNotice, v)
}

func renderAchievementNotice(c Context, v AchievementNoticeView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("achievement.button.list", nil), AddrAchievements)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	lines := []string{c.T("achievement.notice", map[string]any{"achievement": c.AchievementName(v.Achievement)})}
	if v.Cash > 0 {
		lines = append(lines, c.T("achievement.notice_cash", map[string]any{"cash": FormatMoney(c, v.Cash)}))
	}
	if v.Withheld > 0 {
		lines = append(lines, c.T("achievement.notice_withheld", map[string]any{"amount": FormatMoney(c, v.Withheld)}))
	}
	return c.respond(body(lines...), kb.Build())
}

// achievementsLine is the count on the home screen.
func achievementsLine(c Context, n int) string {
	if n <= 0 {
		return ""
	}
	return c.T("profile.achievements", map[string]any{"count": FormatNumber(c, int64(n))})
}
