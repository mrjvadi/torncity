package screens

import (
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A character's life (docs/adr/0025-life-and-legacy.md): the needs, mood,
// age, intelligence and rank on «🧬 زندگی من»; the public player card with
// avatar and bio; the life history; sleeping at a hostel or on a bench; the
// leaderboards; and the notice of a rank that rose or fell.

// Callback addresses of life.
const (
	AddrLife        = "life:me"
	AddrLifeCard    = "life:card"
	AddrLifeHistory = "life:history"
	AddrLifeBio     = "life:bio"
	AddrLifeAvatar  = "life:avatar"
	AddrLifeSleep   = "life:sleep"
	AddrLifeTop     = "life:top"
)

// CommandLifeBio is the command a typed bio fills (configs/commands.yml,
// input).
const CommandLifeBio = "life.bio"

// RankRef is a rank of the ladder of wealth.
type RankRef struct {
	Code, Name, Emoji string
}

// RankName is a rank's display name. Its emoji heads the line the rank is
// the subject of (rankLine), never the middle of a sentence.
func (c Context) RankName(r RankRef) string { return c.named("life.rank."+r.Code, r.Name) }

// rankLine is «{emoji} رتبه: {rank}».
func (c Context) rankLine(r RankRef) string {
	return c.T("life.rank_line", map[string]any{"emoji": r.Emoji, "rank": c.RankName(r)})
}

// StageName is a stage of life's display name.
func (c Context) StageName(n Named) string { return c.named("life.stage."+n.Code, n.Name) }

// SpotName is a sleeping spot's display name.
func (c Context) SleepSpotName(n Named) string { return c.named("life.spot."+n.Code, n.Name) }

// NeedsView is the needs and the mood as screens show them: whole points
// out of 100, higher worse for the three needs, and what they cost.
type NeedsView struct {
	Hunger, Sleep, Stress int
	Happiness             int
	// BodyBPS and XPBPS are what the condition does (10000 = nothing).
	BodyBPS, XPBPS int
	// Pressing names the needs over the mark where they start to cost.
	Pressing []string
}

// meter is a bar of ten cells for a value out of 100.
func (c Context) meter(v int) string {
	v = min(max(v, 0), 100)
	full := (v + 5) / 10
	return strings.Repeat(c.T("life.bar_full", nil), full) + strings.Repeat(c.T("life.bar_empty", nil), 10-full)
}

// needLine is one need or the mood as a bar and its value.
func (c Context) needLine(key string, v int) string {
	return c.T(key, map[string]any{"bar": c.meter(v), "value": FormatNumber(c, int64(v))})
}

// needsLines are the needs and the mood as bars, and what pressing needs
// cost, with what to do about it.
func needsLines(c Context, n *NeedsView) string {
	if n == nil {
		return ""
	}
	lines := []string{
		c.needLine("life.need.hunger", n.Hunger),
		c.needLine("life.need.sleep", n.Sleep),
		c.needLine("life.need.stress", n.Stress),
		c.needLine("life.need.happiness", n.Happiness),
	}
	for _, p := range n.Pressing {
		lines = append(lines, c.T("life.pressing."+p, nil))
	}
	if n.BodyBPS < 10000 {
		lines = append(lines, c.T("life.body_cost", map[string]any{"percent": FormatNumber(c, int64((10000-n.BodyBPS)/100))}))
	}
	if n.XPBPS < 10000 {
		lines = append(lines, c.T("life.mood_cost", map[string]any{"percent": FormatNumber(c, int64((10000-n.XPBPS)/100))}))
	}
	return body(lines...)
}

// WorthView is what a player is worth, part by part.
type WorthView struct {
	Cash, Bank, Escrow, Equity, Property, Goods, Debts int64
	// Savings, Gold and Loans are finance's (docs/adr/0026).
	Savings, Gold, Loans int64
	Total                int64
}

// SleepSpotLine is a place anyone may sleep at, as the life screen offers it.
type SleepSpotLine struct {
	Spot  Named
	Place Named
	Price int64
	// Rest and Relief are the points of sleep need and stress it takes away.
	Rest, Relief int
	// Way is the walk there, nil when the player is there.
	Way *Way
}

// Notices the life screen may open with.
const (
	LifeNoticeSlept   = "slept"
	LifeNoticeBio     = "bio"
	LifeNoticeBioGone = "bio_gone"
	LifeNoticeAvatar  = "avatar"
)

// LifeView is «🧬 زندگی من».
type LifeView struct {
	Needs NeedsView
	Age   int
	Stage Named
	// Intelligence out of IntelligenceMax, and what it speeds up: CourseBPS
	// off a course's time, SkillBPS onto skill experience.
	Intelligence, IntelligenceMax int
	CourseBPS, SkillBPS           int
	Rank                          *RankRef
	// Next is the next rank up and what it takes more; nil at the top.
	Next     *RankRef
	NextNeed int64
	// Worth is what the player is worth: private, left out of a group.
	Worth WorthView
	// Spots are where the player may sleep in their city; SleepIn how long
	// until they may sleep at one again, zero now. Home says they have a
	// home to rest at.
	Spots   []SleepSpotLine
	SleepIn time.Duration
	Home    bool
	// Notice, with NoticeArgs, is what just happened.
	Notice     string
	NoticeArgs map[string]any
}

// Life renders «🧬 زندگی من».
func Life(c Context, v LifeView) *presenter.Response {
	return c.withView(renderLife(c, v), ScreenLife, v)
}

func renderLife(c Context, v LifeView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("life.notice."+v.Notice, v.NoticeArgs)
	}
	iq := map[string]any{"iq": FormatNumber(c, int64(v.Intelligence)), "max": FormatNumber(c, int64(v.IntelligenceMax)),
		"course": PercentFromBPS(c, v.CourseBPS), "skill": PercentFromBPS(c, v.SkillBPS)}
	iqKey := "life.intelligence"
	if v.CourseBPS > 0 || v.SkillBPS > 0 {
		iqKey = "life.intelligence_perks"
	}
	head := body(
		c.T("life.title", nil),
		c.T("life.age", map[string]any{"age": FormatNumber(c, int64(v.Age)), "stage": c.StageName(v.Stage)}),
		c.T(iqKey, iq),
	)
	var rank []string
	if v.Rank != nil {
		rank = append(rank, c.rankLine(*v.Rank))
	}
	if !c.Shared {
		w := v.Worth
		var parts []string
		for _, p := range []struct {
			key string
			v   int64
		}{{"cash", w.Cash + w.Escrow}, {"bank", w.Bank}, {"savings", w.Savings}, {"equity", w.Equity}, {"gold", w.Gold},
			{"property", w.Property}, {"goods", w.Goods}} {
			if p.v != 0 {
				parts = append(parts, c.T("life.worth_part."+p.key, map[string]any{"amount": FormatMoney(c, p.v)}))
			}
		}
		rank = append(rank, c.T("life.worth", map[string]any{"total": FormatMoney(c, w.Total)}))
		if len(parts) > 0 {
			rank = append(rank, strings.Join(parts, c.T("life.worth_sep", nil)))
		}
		if w.Debts > 0 {
			rank = append(rank, c.T("life.worth_debts", map[string]any{"debts": FormatMoney(c, w.Debts)}))
		}
		if w.Loans > 0 {
			rank = append(rank, c.T("life.worth_loans", map[string]any{"loans": FormatMoney(c, w.Loans)}))
		}
		if v.Next != nil {
			rank = append(rank, c.T("life.next_rank", map[string]any{"rank": c.RankName(*v.Next),
				"need": FormatMoney(c, v.NextNeed)}))
		}
	}
	var sleep []string
	if len(v.Spots) > 0 || v.Home {
		sleep = append(sleep, c.T("life.sleep_title", nil))
		if v.Home {
			sleep = append(sleep, c.T("life.sleep_home", nil))
		}
		for _, s := range v.Spots {
			key, args := "life.spot_line", map[string]any{"spot": c.SleepSpotName(s.Spot), "place": c.SpotName(s.Place),
				"price": FormatMoney(c, s.Price), "rest": FormatNumber(c, int64(s.Rest))}
			if s.Price == 0 {
				key = "life.spot_line_free"
			}
			sleep = append(sleep, c.T(key, args))
		}
		if v.SleepIn > 0 {
			sleep = append(sleep, c.T("life.sleep_wait", map[string]any{"wait": FormatDuration(c, v.SleepIn)}))
		}
	}
	text := paragraphs(notice, head, needsLines(c, &v.Needs), body(rank...), body(sleep...))

	kb := keyboards.New()
	if v.SleepIn <= 0 {
		for _, s := range v.Spots {
			label := c.T("life.button.sleep", map[string]any{"spot": c.SleepSpotName(s.Spot)})
			if s.Way != nil {
				if btn, ok := goThenButton(c.T("life.button.sleep_walk", map[string]any{"spot": c.SleepSpotName(s.Spot),
					"walk": FormatDuration(c, s.Way.Walk)}), s.Way.Place.Code, "life.me"); ok {
					kb.Row(btn)
				}
				continue
			}
			kb.Add(label, AddrLifeSleep, s.Spot.Code)
		}
	}
	if v.Home {
		kb.Add(c.T("life.button.home_rest", nil), AddrPropertyRest)
	}
	history, _ := keyboards.Button(c.T("life.button.history", nil), AddrLifeHistory)
	card, _ := keyboards.Button(c.T("life.button.card", nil), AddrLifeCard)
	kb.Row(history, card)
	kb.Add(c.T("life.button.top", nil), AddrLifeTop)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrLife}))
	return c.respond(text, kb.Build())
}

// AvatarRef is how a player is shown: an avatar's emoji, or their photo.
type AvatarRef struct {
	Code  string
	Emoji string
	Photo bool
}

// CardView is a player's public card.
type CardView struct {
	Name, Code string
	Avatar     AvatarRef
	Bio        string
	Rank       *RankRef
	Age        int
	Stage      Named
	Level      int
	// Achievements earned, and entries on the public timeline.
	Achievements int
	Entries      int
	JoinedAt     time.Time
	// Self is the player's own card: it offers the bio and the avatar.
	Self bool
	// Photo is the Telegram photo to show it with, nil for none.
	Photo  *presenter.Photo
	Notice string
}

// cardName is a player's name with their avatar in front of it.
func (c Context) cardName(v CardView) string {
	name := c.playerName(v.Name)
	if v.Avatar.Emoji != "" {
		return c.T("life.card.name_avatar", map[string]any{"avatar": v.Avatar.Emoji, "name": name})
	}
	return c.T("life.card.name", map[string]any{"name": name})
}

// Card renders a player's card.
func Card(c Context, v CardView) *presenter.Response {
	return c.withView(renderCard(c, v), ScreenCard, v)
}

func renderCard(c Context, v CardView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("life.notice."+v.Notice, nil)
	}
	lines := []string{c.cardName(v)}
	if v.Bio != "" {
		lines = append(lines, c.T("life.card.bio", map[string]any{"bio": v.Bio}))
	} else if v.Self {
		lines = append(lines, c.T("life.card.no_bio", nil))
	}
	facts := []string{}
	if v.Rank != nil {
		facts = append(facts, c.rankLine(*v.Rank))
	}
	facts = append(facts,
		c.T("life.card.age_level", map[string]any{"age": FormatNumber(c, int64(v.Age)), "stage": c.StageName(v.Stage),
			"level": FormatNumber(c, int64(max(v.Level, 1)))}),
		c.T("life.card.record", map[string]any{"achievements": FormatNumber(c, int64(v.Achievements)),
			"entries": FormatNumber(c, int64(v.Entries))}),
	)
	if !v.JoinedAt.IsZero() {
		facts = append(facts, c.T("life.card.joined", map[string]any{"date": FormatDate(c, v.JoinedAt)}))
	}
	if v.Code != "" {
		facts = append(facts, c.T("profile.code", map[string]any{"code": v.Code}))
	}
	text := paragraphs(notice, body(lines...), body(facts...))

	kb := keyboards.New()
	if v.Self {
		kb.Add(c.T("life.button.my_history", nil), AddrLifeHistory)
		bio, _ := keyboards.Button(c.T("life.button.bio", nil), AddrAsk, CommandLifeBio)
		avatar, _ := keyboards.Button(c.T("life.button.avatar", nil), AddrLifeAvatar)
		kb.Row(bio, avatar)
		if v.Bio != "" {
			kb.Add(c.T("life.button.bio_clear", nil), AddrLifeBio, "yes")
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrLife, RefreshData: AddrLifeCard}))
	} else {
		kb.Add(c.T("life.button.their_history", map[string]any{"player": c.playerName(v.Name)}), AddrLifeHistory, v.Code)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: keyboards.Data(AddrLifeCard, v.Code)}))
	}
	resp := c.respond(text, kb.Build())
	if v.Photo != nil {
		// A photo goes out as a new message with the card as its caption.
		resp = presenter.Message(text, kb.Build())
		resp.Photo = v.Photo
	}
	return resp
}

// HistoryLine is one entry of a timeline.
type HistoryLine struct {
	Kind string
	At   time.Time
	// Code and Name are what it is about; Sub and SubName a second code
	// (a job's rank, the rank fallen from); Place where; Amount and Number
	// its figures.
	Code, Name   string
	Sub, SubName string
	PlaceKind    string
	Place        Named
	Amount       int64
	Number       int64
	Backfilled   bool
	Private      bool
}

// HistoryView is a page of a life history.
type HistoryView struct {
	Name  string
	Code  string
	Self  bool
	Lines []HistoryLine
	Page  int
	Pages int
	Total int
}

// historyWhat is what an entry is about, in words.
func (c Context) historyWhat(l HistoryLine) string {
	switch l.Kind {
	case "first_job", "hired", "promoted":
		return c.TierTitle(l.Code, l.Sub, l.Name)
	case "course", "certificate":
		return c.CourseName(l.Code, l.Name)
	case "property_bought", "property_sold":
		return c.PropertyTypeName(Named{Code: l.Code, Name: l.Name})
	case "election_won", "election_lost", "office_taken", "office_lost":
		return c.OfficeName(l.Code)
	case "achievement":
		return c.AchievementName(Named{Code: l.Code, Name: l.Name})
	case "rank_up", "rank_down":
		return c.named("life.rank."+l.Code, l.Name)
	case "big_trade":
		return c.ItemName(Named{Code: l.Code, Name: l.Name})
	}
	return l.Name
}

// historyPlace is where an entry happened.
func (c Context) historyPlace(l HistoryLine) string {
	if l.PlaceKind != "" && l.PlaceKind != "city" {
		return c.PlaceName(GovPlace{Kind: l.PlaceKind, Code: l.Place.Code, Name: l.Place.Name})
	}
	return c.CityName(l.Place.Code, l.Place.Name)
}

// historyLine is one entry in words.
func (c Context) historyLine(l HistoryLine) string {
	args := map[string]any{"date": FormatDate(c, l.At), "what": c.historyWhat(l), "place": c.historyPlace(l),
		"amount": FormatMoney(c, l.Amount), "number": FormatNumber(c, l.Number)}
	key := "life.history." + l.Kind
	if l.Place.Code == "" && l.Place.Name == "" {
		if alt := key + "_nowhere"; c.T(alt, nil) != alt {
			key = alt
		}
	}
	line := c.T(key, args)
	if l.Backfilled {
		line = c.T("life.history.backfilled", map[string]any{"line": line})
	}
	if l.Private {
		line = c.T("life.history.private", map[string]any{"line": line})
	}
	return line
}

// History renders a page of a life history.
func History(c Context, v HistoryView) *presenter.Response {
	return c.withView(renderHistory(c, v), ScreenHistory, v)
}

func renderHistory(c Context, v HistoryView) *presenter.Response {
	title := c.T("life.history_title_mine", nil)
	if !v.Self {
		title = c.T("life.history_title", map[string]any{"player": c.playerName(v.Name)})
	}
	var lines []string
	for _, l := range v.Lines {
		lines = append(lines, c.historyLine(l))
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("life.history_empty", nil))
	}
	head := title
	if v.Pages > 1 {
		head = body(title, c.T("life.history_page", map[string]any{"page": FormatNumber(c, int64(v.Page)),
			"pages": FormatNumber(c, int64(v.Pages))}))
	}
	var hint string
	if v.Self {
		hint = c.T("life.history_hint", nil)
	}
	kb := keyboards.New()
	back := AddrLife
	if !v.Self {
		back = keyboards.Data(AddrLifeCard, v.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{Prefix: keyboards.Data(AddrLifeHistory, v.Code), Page: v.Page, HasPrev: v.Page > 1,
		HasNext: v.Page < v.Pages, BackData: back}))
	return c.respond(paragraphs(head, body(lines...), hint), kb.Build())
}

// AvatarsView is the choice of avatar.
type AvatarsView struct {
	Current AvatarRef
	Avatars []AvatarChoice
}

// AvatarChoice is one avatar to choose.
type AvatarChoice struct {
	Code, Name, Emoji string
}

// Avatars renders the choice of avatar.
func Avatars(c Context, v AvatarsView) *presenter.Response {
	return c.withView(renderAvatars(c, v), ScreenAvatars, v)
}

func renderAvatars(c Context, v AvatarsView) *presenter.Response {
	current := c.T("life.avatar_none", nil)
	switch {
	case v.Current.Photo:
		current = c.T("life.avatar_photo", nil)
	case v.Current.Emoji != "":
		current = v.Current.Emoji
	}
	text := body(c.T("life.avatar_title", nil), c.T("life.avatar_current", map[string]any{"avatar": current}),
		c.T("life.avatar_hint", nil))
	kb := keyboards.New()
	var row []presenter.Button
	for _, a := range v.Avatars {
		btn, ok := keyboards.Button(c.T("life.button.avatar_choice", map[string]any{"emoji": a.Emoji,
			"name": c.named("life.avatar."+a.Code, a.Name)}), AddrLifeAvatar, a.Code)
		if ok {
			row = append(row, btn)
		}
	}
	kb.Grid(3, row...)
	photo, _ := keyboards.Button(c.T("life.button.avatar_photo", nil), AddrLifeAvatar, "photo")
	none, _ := keyboards.Button(c.T("life.button.avatar_none", nil), AddrLifeAvatar, "none")
	kb.Row(photo, none)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLifeCard, RefreshData: AddrLifeAvatar}))
	return c.respond(text, kb.Build())
}

// SleepPayView is a night at a paid spot, to pay for.
type SleepPayView struct {
	Spot    Named
	Rest    int
	Relief  int
	Payment PaymentChoice
}

// SleepPay renders the price of a night and how to pay it.
func SleepPay(c Context, v SleepPayView) *presenter.Response {
	return c.withView(renderSleepPay(c, v), ScreenSleepPay, v)
}

func renderSleepPay(c Context, v SleepPayView) *presenter.Response {
	text := body(c.T("life.sleep_pay", map[string]any{"spot": c.SleepSpotName(v.Spot),
		"price": FormatMoney(c, v.Payment.Amount), "rest": FormatNumber(c, int64(v.Rest))}), c.paymentNote(v.Payment))
	kb := keyboards.New()
	c.paymentButtons(kb, v.Payment, func(method string) []string { return []string{AddrLifeSleep, v.Spot.Code, method} })
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLife}))
	return c.respond(text, kb.Build())
}

// Refusals of life.
const (
	LifeRefusedBioLength  = "bio_length"
	LifeRefusedBioLink    = "bio_link"
	LifeRefusedBioBlocked = "bio_blocked"
	LifeRefusedBioChars   = "bio_chars"
	LifeRefusedTooSoon    = "too_soon"
	LifeRefusedNoSpot     = "no_spot"
	LifeRefusedNoAvatar   = "no_avatar"
	LifeRefusedNoPlayer   = "no_player"
	LifeRefusedNoCity     = "no_city"
	LifeRefusedRested     = "rested"
)

// LifeRefusalView is a refusal of life.
type LifeRefusalView struct {
	Kind string
	Wait time.Duration
	Min  int
	Max  int
}

// LifeRefusal renders a refusal of life.
func LifeRefusal(c Context, v LifeRefusalView) *presenter.Response {
	return c.withView(renderLifeRefusal(c, v), ScreenLifeRefusal, v)
}

func renderLifeRefusal(c Context, v LifeRefusalView) *presenter.Response {
	text := c.T("life.refused."+v.Kind, map[string]any{"wait": FormatDuration(c, v.Wait),
		"min": FormatNumber(c, int64(v.Min)), "max": FormatNumber(c, int64(v.Max))})
	kb := keyboards.New()
	back := AddrLife
	switch v.Kind {
	case LifeRefusedBioLength, LifeRefusedBioLink, LifeRefusedBioBlocked, LifeRefusedBioChars:
		kb.Add(c.T("life.button.bio_again", nil), AddrAsk, CommandLifeBio)
		back = AddrLifeCard
	case LifeRefusedNoAvatar:
		back = AddrLifeAvatar
	case LifeRefusedNoPlayer:
		kb.Add(c.T("button.find_player", nil), AddrSearch)
		back = AddrHome
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(text, kb.Build())
}

// BoardLine is one line of a leaderboard.
type BoardLine struct {
	Position int
	Code     string
	Name     string
	// Tag is what the line is tagged with: a rank, a city, a career;
	// TagCode and TagKind say which, for its name.
	Tag, TagName string
	City         Named
	Value        int64
	Extra        int64
	Extra2       int64
	Mine         bool
}

// BoardView is one leaderboard.
type BoardView struct {
	Board string
	Lines []BoardLine
	// At is when it was refreshed; zero before the first refresh.
	At time.Time
	// Ranks names the ranks the richest board tags players with.
	Ranks map[string]RankRef
}

// boardLine is one line of a board in words.
func (c Context) boardLine(board string, l BoardLine) string {
	name := c.playerName(l.Name)
	args := map[string]any{"pos": FormatNumber(c, int64(l.Position)), "name": name,
		"value": FormatMoney(c, l.Value), "number": FormatNumber(c, l.Value), "extra": FormatNumber(c, l.Extra),
		"city": c.CityName(l.City.Code, l.City.Name), "tag": l.TagName}
	switch board {
	case "companies":
		args["name"] = l.Name
		args["tag"] = c.CompanyTypeName(Named{Code: l.Tag, Name: l.TagName})
	case "cities":
		args["name"] = c.CityName(l.Code, l.Name)
		args["extra2"] = FormatNumber(c, l.Extra2)
	case "investors":
		args["extra"] = FormatMoney(c, l.Extra)
	case "workers":
		if l.Tag != "" {
			args["tag"] = c.CareerName(l.Tag, l.TagName)
		} else {
			args["tag"] = c.T("life.board.no_job", nil)
		}
	}
	line := c.T("life.board."+board+"_line", args)
	if l.Mine {
		line = c.T("life.board.mine", map[string]any{"line": line})
	}
	return line
}

// Leaderboard renders one board, and the way to the others.
func Leaderboard(c Context, v BoardView) *presenter.Response {
	return c.withView(renderLeaderboard(c, v), ScreenLeaderboard, v)
}

func renderLeaderboard(c Context, v BoardView) *presenter.Response {
	title := c.T("life.board."+v.Board+"_title", nil)
	var lines []string
	for _, l := range v.Lines {
		if v.Board == "richest" {
			if r, ok := v.Ranks[l.Tag]; ok {
				l.TagName = c.RankName(r)
			}
		}
		lines = append(lines, c.boardLine(v.Board, l))
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("life.board.empty", nil))
	}
	var when string
	if !v.At.IsZero() {
		when = c.T("life.board.refreshed", map[string]any{"date": FormatDate(c, v.At), "time": FormatClock(c, v.At)})
	}
	kb := keyboards.New()
	var tabs []presenter.Button
	for _, b := range []string{"richest", "companies", "cities", "workers", "investors"} {
		if b == v.Board {
			continue
		}
		if btn, ok := keyboards.Button(c.T("life.board."+b+"_tab", nil), AddrLifeTop, b); ok {
			tabs = append(tabs, btn)
		}
	}
	kb.Grid(2, tabs...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: keyboards.Data(AddrLifeTop, v.Board)}))
	return c.respond(paragraphs(title, body(lines...), when, c.T("life.board."+v.Board+"_hint", nil)), kb.Build())
}

// RankNoticeView is a rank that rose or fell.
type RankNoticeView struct {
	Rank  RankRef
	From  RankRef
	Up    bool
	Worth int64
}

// RankNotice tells a player their rank rose or fell.
func RankNotice(c Context, v RankNoticeView) *presenter.Response {
	return c.withView(renderRankNotice(c, v), ScreenRankNotice, v)
}

func renderRankNotice(c Context, v RankNoticeView) *presenter.Response {
	key := "life.rank_down_notice"
	if v.Up {
		key = "life.rank_up_notice"
	}
	text := c.T(key, map[string]any{"emoji": v.Rank.Emoji, "rank": c.RankName(v.Rank), "from": c.RankName(v.From),
		"worth": FormatMoney(c, v.Worth)})
	kb := keyboards.New()
	kb.Add(c.T("life.button.open", nil), AddrLife)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// lifeLines are the profile's rank, age and needs.
func lifeLines(c Context, rank *RankRef, age int, stage Named) string {
	var lines []string
	if rank != nil {
		lines = append(lines, c.rankLine(*rank))
	}
	if age > 0 {
		lines = append(lines, c.T("life.age", map[string]any{"age": FormatNumber(c, int64(age)), "stage": c.StageName(stage)}))
	}
	return body(lines...)
}
