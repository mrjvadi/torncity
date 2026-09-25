package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// ProfileView is the player's own record as the profile screen shows it.
//
// It carries only what a player understands and can act on. The public code
// is on it for that reason — it is the one identifier a player can DO
// something with: give it to a friend, who finds them with /social <code>.
// The record's identifier, stored language and account status are
// deliberately absent:
// none of them means anything to a player, and a value that is on screen ends
// up in a screenshot and then in a support request as if it were a fact about
// them. A city travels as its content CODE, which the screen turns into a
// name in the player's language, plus its authored name as the fallback for a
// city nobody has translated yet; the code itself is never shown.
type ProfileView struct {
	Name string
	// Code is the player's public code (internal/shared/playercode). Empty
	// only for a record that has none, and then the line is left out.
	Code string
	// CityCode and City are the player's city: its content code and its
	// authored name. Both empty when the player is nowhere yet; an empty city
	// is simply not shown.
	CityCode string
	City     string
	// Place is where in the city the player stands, empty in a city
	// without places; Walk a walk under way instead, which the profile
	// shows with its time left and arrival.
	Place Named
	Walk  *WalkView

	Level int
	XP    int64
	// NextLevelXP is the XP total at which the next level is reached, from
	// the domain's curve. Zero means there is no next level.
	NextLevelXP int64

	Energy    int
	MaxEnergy int
	// EnergyFullIn is how long until energy is full again, zero when it
	// already is.
	EnergyFullIn time.Duration
	Health       int
	MaxHealth    int

	// Cash is the money the player carries and Bank their bank balance,
	// both in minor units. Money is a player's own business: a shared
	// profile (Context.Shared, a group) leaves both out.
	Cash int64
	Bank int64

	// Travelling says a journey is in progress. TravelTo and TravelRemaining
	// describe it; the profile then shows the journey instead of a city the
	// player is no longer standing in.
	Travelling      bool
	TravelToCode    string
	TravelTo        string
	TravelRemaining time.Duration

	// Work is the player's job and studies. Nil means the caller did not
	// look, and the profile says nothing about work either way; a non-nil
	// Work with no Job says the player has none, and the profile says so
	// and points at the openings.
	Work *ProfileWork

	// Jail is the sentence the player is serving, nil when free. The home
	// screen says so first, with the time left and the release time, and
	// offers the jail instead of what jail rules out.
	Jail *ProfileJail

	// Hospital is the stay the player is in hospital for, nil when well.
	// Like jail it is said first, and the home screen offers the hospital
	// instead of what a stay rules out (docs/adr/0023).
	Hospital *ProfileJail

	// Achievements is how many achievements the player has earned
	// (docs/adr/0024); none says nothing.
	Achievements int

	// A character's life (docs/adr/0025): the avatar shown before the
	// name (an emoji), the headline rank by net worth, the age and stage of
	// life, and the needs as bars. Each is left out when absent.
	Avatar string
	Rank   *RankRef
	Age    int
	Stage  Named
	Needs  *NeedsView
}

// ProfileJail is a sentence as the home screen shows it. A hospital stay is
// shown with the same facts.
type ProfileJail struct {
	// CityCode and City are where the player is held.
	CityCode string
	City     string
	// Remaining is how long until release; EndsAt is the release instant.
	Remaining time.Duration
	EndsAt    time.Time
}

// ProfileWork is what the profile shows of a player's job and studies: the
// two things a player checks most after their money.
type ProfileWork struct {
	// Job is the player's position, nil when they have none.
	Job *ProfileJob
	// Course is the course in progress, nil when not studying.
	Course *ProfileCourse
	// Certificates is how many certificates the player holds.
	Certificates int
}

// ProfileJob is the player's position as the profile shows it.
type ProfileJob struct {
	Job JobRef
	// CityCode and City are where the job is.
	CityCode string
	City     string
	// Pay is what a full-output shift pays now, minimum wage applied.
	Pay int64
	// ShiftEndsIn is how long the shift in progress has to run; zero when
	// no shift is running, and any positive value under a minute once its
	// time is up and the pay is on its way.
	ShiftEndsIn time.Duration
}

// ProfileCourse is the course in progress as the profile shows it.
type ProfileCourse struct {
	Course CourseRef
	// Remaining is how long until it finishes; while Paused, the time that
	// was left when it stopped.
	Remaining time.Duration
	// Paused says the course stands still because the player is in jail.
	Paused bool
}

// isNewPlayer reports whether this is someone who has not done anything yet,
// the one moment the welcome line earns its space.
func (v ProfileView) isNewPlayer() bool {
	if w := v.Work; w != nil && (w.Job != nil || w.Course != nil || w.Certificates > 0) {
		return false
	}
	return v.XP == 0 && v.Level <= 1 && !v.Travelling
}

// Profile renders the player's record. It is also the home screen: /start and
// every back button land here, so it carries the way to every other screen.
func Profile(c Context, v ProfileView) *presenter.Response {
	var welcome string
	if v.isNewPlayer() {
		welcome = c.T("profile.body", nil)
	}

	city := c.CityName(v.CityCode, v.City)
	travelTo := c.CityName(v.TravelToCode, v.TravelTo)

	var where string
	switch {
	case v.Travelling && travelTo != "":
		where = c.T("profile.travelling", map[string]any{
			"city":      travelTo,
			"remaining": FormatDuration(c, v.TravelRemaining),
		})
	case city != "":
		where = placeLines(c, city, v.Place, v.Walk)
	}

	var name string
	switch {
	case v.Name != "" && v.Avatar != "":
		name = c.T("life.card.name_avatar", map[string]any{"avatar": v.Avatar, "name": v.Name})
	case v.Name != "":
		name = c.T("profile.name", map[string]any{"name": v.Name})
	}

	// The code is always there; how a friend uses it is explained once, to
	// the new player, and not on every visit after.
	var code string
	if v.Code != "" {
		code = c.T("profile.code", map[string]any{"code": v.Code})
		if v.isNewPlayer() {
			code = body(code, c.T("profile.code_hint", map[string]any{"code": v.Code}))
		}
	}

	text := paragraphs(
		welcome,
		body(name, lifeLines(c, v.Rank, v.Age, v.Stage), where),
		jailLines(c, v.Jail),
		hospitalLines(c, v.Hospital),
		body(
			levelLine(c, v.Level, v.XP, v.NextLevelXP),
			energyLine(c, v.Energy, v.MaxEnergy, v.EnergyFullIn),
			c.T("profile.health", map[string]any{
				"health":     FormatNumber(c, int64(v.Health)),
				"max_health": FormatNumber(c, int64(v.MaxHealth)),
			}),
		),
		needsLines(c, v.Needs),
		workLines(c, v.Work),
		moneyLines(c, v.Cash, v.Bank),
		achievementsLine(c, v.Achievements),
		code,
	)

	kb := hubKeyboard(c, city != "", v.Travelling, v.Jail != nil || v.Hospital != nil, v.Work)
	if v.Hospital != nil && v.Jail == nil {
		kb = hospitalHub(c, v.Work)
	}
	return c.respond(text, kb.Build())
}

// hospitalLines says the player is in hospital: where, for how long, when
// they leave, and what a stay stops them doing.
func hospitalLines(c Context, h *ProfileJail) string {
	if h == nil {
		return ""
	}
	return body(
		c.T("profile.hospital", map[string]any{"city": c.CityName(h.CityCode, h.City),
			"remaining": FormatDuration(c, h.Remaining)}),
		clockLine(c, "health.discharge_at", h.EndsAt),
		c.T("profile.hospital_blocks", nil),
	)
}

// hospitalHub is the home screen's keyboard for a patient: the hospital
// first, where a treatment shortens the stay.
func hospitalHub(c Context, work *ProfileWork) *keyboards.Builder {
	kb := keyboards.New()
	hospital, _ := keyboards.Button(c.T("health.button.hospital", nil), AddrHospital)
	job, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	if work != nil && work.Job == nil {
		job, _ = keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	}
	kb.Row(hospital, job)
	study, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	bank, _ := keyboards.Button(c.T("button.bank", nil), AddrBank)
	kb.Row(study, bank)
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	kb.Row(skills, social)
	settings, _ := keyboards.Button(c.T("button.settings", nil), AddrSettings)
	refresh, _ := keyboards.Button(c.T("button.refresh", nil), AddrProfile)
	kb.Row(settings, refresh)
	return kb
}

// placeLines is where the player is in their city: the place they stand at,
// or the walk they are on — where to, how long is left and when they
// arrive — or the city alone when it has no places.
func placeLines(c Context, city string, here Named, walk *WalkView) string {
	switch {
	case walk != nil:
		return body(
			c.T("profile.city", map[string]any{"city": city}),
			c.T("profile.walking", map[string]any{
				"place":     c.SpotName(walk.To),
				"remaining": FormatDuration(c, walk.Remaining),
				"time":      FormatClock(c, walk.ArrivesAt),
			}),
		)
	case here.Code != "":
		return c.T("profile.place", map[string]any{"city": city, "place": c.SpotName(here)})
	}
	return c.T("profile.city", map[string]any{"city": city})
}

// jailLines says the player is in jail: where, for how long, and at what time
// they are free, and what jail stops them doing.
func jailLines(c Context, j *ProfileJail) string {
	if j == nil {
		return ""
	}
	args := map[string]any{"remaining": FormatDuration(c, j.Remaining)}
	key := "profile.jail"
	if city := c.CityName(j.CityCode, j.City); city != "" {
		key, args["city"] = "profile.jail_in", city
	}
	return body(
		c.T(key, args),
		clockLine(c, "crime.free_at", j.EndsAt),
		c.T("profile.jail_blocks", nil),
	)
}

// workLines shows the player's job and studies: the position, where and for
// what pay, a shift in progress, the course in progress and the certificates
// earned. A player with no job is told so, because the home screen is where
// a new player learns that work exists.
func workLines(c Context, w *ProfileWork) string {
	if w == nil {
		return ""
	}
	var lines []string
	if j := w.Job; j != nil {
		lines = append(lines, c.T("profile.job", map[string]any{
			"title": c.jobTitle(j.Job),
			"city":  c.CityName(j.CityCode, j.City),
			"pay":   FormatMoney(c, j.Pay),
		}))
		switch {
		case j.ShiftEndsIn >= arrivingThreshold:
			lines = append(lines, c.T("profile.shift_running", map[string]any{"remaining": FormatDuration(c, j.ShiftEndsIn)}))
		case j.ShiftEndsIn > 0:
			// Its time is up and the pay is on its way: a countdown stuck
			// at one second would look broken.
			lines = append(lines, c.T("profile.shift_ending", nil))
		}
	} else {
		lines = append(lines, c.T("profile.job_none", nil))
	}
	if cr := w.Course; cr != nil {
		key, args := "profile.course", map[string]any{"course": c.course(cr.Course)}
		switch {
		case cr.Paused:
			key = "profile.course_paused"
			args["remaining"] = FormatDuration(c, cr.Remaining)
		case cr.Remaining < arrivingThreshold:
			key = "profile.course_finishing"
		default:
			args["remaining"] = FormatDuration(c, cr.Remaining)
		}
		lines = append(lines, c.T(key, args))
	}
	if w.Certificates > 0 {
		lines = append(lines, c.T("profile.certificates", map[string]any{"count": w.Certificates}))
	}
	return body(lines...)
}

// moneyLines shows the two places a player's money lives: the cash they
// carry and their bank balance. A shared screen (a group) shows neither.
func moneyLines(c Context, cash, bank int64) string {
	if c.Shared {
		return ""
	}
	return body(
		c.T("profile.cash", map[string]any{"cash": FormatMoney(c, cash)}),
		c.T("profile.bank", map[string]any{"bank": FormatMoney(c, bank)}),
	)
}

// levelLine shows the level and how far the next one is. A player at the top
// of the curve sees the level alone: "0 XP to level 101" would be a lie.
func levelLine(c Context, level int, xp, nextLevelXP int64) string {
	if level < 1 {
		level = 1
	}
	if nextLevelXP <= xp {
		return c.T("profile.level_max", map[string]any{"level": level})
	}
	return c.T("profile.level", map[string]any{
		"level":      level,
		"xp_to_next": FormatNumber(c, nextLevelXP-xp),
		"next_level": level + 1,
	})
}

// energyLine shows energy, and when it is not full, how long until it is —
// which is the one thing a player short of energy wants to know.
func energyLine(c Context, energy, maxEnergy int, fullIn time.Duration) string {
	args := map[string]any{
		"energy":     FormatNumber(c, int64(energy)),
		"max_energy": FormatNumber(c, int64(maxEnergy)),
	}
	if energy < maxEnergy && fullIn > 0 {
		args["duration"] = FormatDuration(c, fullIn)
		return c.T("profile.energy_refilling", args)
	}
	return c.T("profile.energy", args)
}

// hubKeyboard is the navigation of the home screen.
//
// It offers only what the player can do right now: the journey instead of
// the map while travelling, and no map at all for a player who is not in a
// city yet, because every departure would be refused. A player with no job
// is sent straight to the openings rather than to an empty job screen. It
// has no back button, because it is the screen every back button leads to.
//
// The order is the order of use: going somewhere and working first, then
// study and money, then skills and friends, then the city, then settings.
func hubKeyboard(c Context, hasCity, travelling, jailed bool, work *ProfileWork) *keyboards.Builder {
	kb := keyboards.New()

	var place presenter.Button
	switch {
	case jailed:
		// Jail rules out travel and a shift; the jail — bail, the time
		// left — is what the player can act on.
		place, _ = keyboards.Button(c.T("crime.button.jail", nil), AddrCrimeJail)
	case travelling:
		place, _ = keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
	case hasCity:
		place, _ = keyboards.Button(c.T("button.map", nil), AddrMap)
	}
	job, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	if work != nil && work.Job == nil {
		job, _ = keyboards.Button(c.T("job.button.openings", nil), AddrJobList)
	}
	if place.Text != "" {
		kb.Row(place, job)
	} else {
		kb.Row(job)
	}

	study, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	bank, _ := keyboards.Button(c.T("button.bank", nil), AddrBank)
	kb.Row(study, bank)

	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	kb.Row(skills, social)

	// Stage E (docs/adr/0023): missions and the player's faction.
	missions, _ := keyboards.Button(c.T("mission.button.mine", nil), AddrMissions)
	factionBtn, _ := keyboards.Button(c.T("faction.button.mine", nil), AddrFactionMine)
	kb.Row(missions, factionBtn)

	// Stage G1 (docs/adr/0025): the player's life, and the leaderboards.
	lifeBtn, _ := keyboards.Button(c.T("life.button.open", nil), AddrLife)
	topBtn, _ := keyboards.Button(c.T("life.button.top", nil), AddrLifeTop)
	kb.Row(lifeBtn, topBtn)

	// Stage F (docs/adr/0024): the player's property and home, and their
	// achievements.
	homeBtn, _ := keyboards.Button(c.T("property.button.mine", nil), AddrPropertyMine)
	achBtn, _ := keyboards.Button(c.T("achievement.button.list", nil), AddrAchievements)
	kb.Row(homeBtn, achBtn)
	// The city's shops, a press from home (the bag has them too).
	shopsBtn, _ := keyboards.Button(c.T("shop.button.shops", nil), AddrShops)
	kb.Row(shopsBtn)

	if hasCity && !travelling && !jailed {
		city, _ := keyboards.Button(c.T("gov.button.city", nil), AddrGovCity)
		companies, _ := keyboards.Button(c.T("company.button.registry", nil), AddrCompanies)
		kb.Row(city, companies)
	}
	if c.Shared && !travelling && !jailed {
		// In a group the home screen is a public square: crime is what is
		// played here (configs/commands.yml), and the gateway keeps the
		// private-chat buttons above off the group's timeline.
		kb.Add(c.T("crime.button.hub", nil), AddrCrimeHub)
	}

	settings, _ := keyboards.Button(c.T("button.settings", nil), AddrSettings)
	refresh, _ := keyboards.Button(c.T("button.refresh", nil), AddrProfile)
	kb.Row(settings, refresh)
	return kb
}
