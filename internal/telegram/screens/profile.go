package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Profile renders the player's record. It is also the home screen: /start and
// every back button land here, so it carries the way to every other screen.
func Profile(c Context, v ProfileView) *presenter.Response {
	return c.withView(renderProfile(c, v), ScreenProfile, v)
}

func renderProfile(c Context, v ProfileView) *presenter.Response {
	var welcome string
	if v.IsNewPlayer() {
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
		if v.IsNewPlayer() {
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

	kb := hubKeyboard(c, city != "", v.Travelling, v.Jail != nil || v.Hospital != nil, v.Work, v.Village)
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
func hubKeyboard(c Context, hasCity, travelling, jailed bool, work *ProfileWork, village *Named) *keyboards.Builder {
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
	if c.Shared && !travelling && !jailed {
		// In a group the home screen is a public square, and crime is what
		// is played there (configs/commands.yml keeps the private-chat
		// buttons below off its timeline) — the group's main activity, right
		// under the row that already says where the player is and what they
		// do, not after every other section.
		kb.Add(c.T("crime.button.hub", nil), AddrCrimeHub)
	}

	// The daily loop: study for a promotion, bank the pay.
	study, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	bank, _ := keyboards.Button(c.T("button.bank", nil), AddrBank)
	kb.Row(study, bank)

	// What the player owns: their home, and the city's shops for the rest.
	homeBtn, _ := keyboards.Button(c.T("property.button.mine", nil), AddrPropertyMine)
	shopsBtn, _ := keyboards.Button(c.T("shop.button.shops", nil), AddrShops)
	kb.Row(homeBtn, shopsBtn)

	if hasCity && !travelling && !jailed {
		// The city's own institutions: its offices and policies, and the
		// companies it hosts.
		city, _ := keyboards.Button(c.T("gov.button.city", nil), AddrGovCity)
		if village != nil {
			// A resident's home is the village: its overview replaces the
			// city hall, which a village does not have.
			city, _ = keyboards.Button(c.T("village.home.profile_button", nil), AddrVillageHome)
		}
		companies, _ := keyboards.Button(c.T("company.button.registry", nil), AddrCompanies)
		kb.Row(city, companies)
	}

	// Other players: friends, the player's faction, and missions run with
	// either.
	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	factionBtn, _ := keyboards.Button(c.T("faction.button.mine", nil), AddrFactionMine)
	kb.Row(social, factionBtn)
	missions, _ := keyboards.Button(c.T("mission.button.mine", nil), AddrMissions)
	kb.Row(missions)

	// The player themself: skills, their life story, achievements and the
	// leaderboards.
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	lifeBtn, _ := keyboards.Button(c.T("life.button.open", nil), AddrLife)
	kb.Row(skills, lifeBtn)
	achBtn, _ := keyboards.Button(c.T("achievement.button.list", nil), AddrAchievements)
	topBtn, _ := keyboards.Button(c.T("life.button.top", nil), AddrLifeTop)
	kb.Row(achBtn, topBtn)

	settings, _ := keyboards.Button(c.T("button.settings", nil), AddrSettings)
	refresh, _ := keyboards.Button(c.T("button.refresh", nil), AddrProfile)
	kb.Row(settings, refresh)
	return kb
}
