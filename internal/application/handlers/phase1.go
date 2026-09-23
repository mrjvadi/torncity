package handlers

import (
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// This file holds what the phase 1 handlers share: the ports they declare at
// the point of use, the conversions between a stored row and the domain value
// that owns its rules, and the two or three pieces of arithmetic that would
// otherwise be copied into five handlers and drift.
//
// # A note on where the phase 1 repositories come from
//
// Every phase 1 repository a handler WRITES through is reached via the Tx its
// unit of work hands it: tx.Stats(), tx.Travels(), tx.GameActions() and
// tx.Friendships() (tx.Skills() exists for the same reason, though no handler
// writes a skill yet). So a command's state change, its idempotency
// reservation and its outbox record commit together or not at all.
//
// This was not always so. These repositories used to be constructor arguments
// running on their own pooled connection, called inside the unit of work for
// ordering only. The failure that produced was concrete: TravelHandler.Complete
// committed the arrival on its own, a later step failed, the idempotency key
// rolled back — and the redelivery found no active journey, returned nil, and
// the player landed without the XP. On tx the arrival rolls back with the
// failed step, and the retry lands the journey and awards the XP once.
//
// What is still injected, and why:
//
//   - CityRepository and PlayerSearch. Both are read-only lookups — cities are
//     content only the loader writes, search reads public records — and no
//     write depends on them for correctness. See application.Tx.
//   - The map and skills screens keep their injected TravelRepository and
//     SkillRepository. They write nothing, so a transaction would add nothing
//     but its length; moving them onto tx is harmless whenever it is wanted.

// TravelPlanner works out the journey a player would make.
//
// It is declared here rather than taken as a travel.Planner value because a
// planner is built from content that reloads while the process runs: the
// composition layer can hand in something that reads the current snapshot per
// request, and travel.Planner itself satisfies this as it stands.
type TravelPlanner interface {
	Plan(from, to world.City, speed travel.Speed, now time.Time) (travel.Journey, travel.Cost, error)
}

// RouteNetwork answers which cities are connected, for the map screen.
// world.Routes satisfies it.
type RouteNetwork interface {
	DistanceBetween(from, to string) (int, error)
	Has(code string) bool
}

// DefaultPageSize is how many rows a paginated screen shows when a caller has
// no opinion. It is small because a Telegram message is read on a phone.
const DefaultPageSize = 5

// worldCity turns a stored city into the domain value that carries the rules.
func worldCity(c application.City) world.City {
	return world.City{
		ID:           c.ID,
		Code:         c.Code,
		Name:         c.Name,
		TaxRateBPS:   c.TaxRateBPS,
		CostOfLiving: c.CostOfLiving,
		Population:   c.Population,
	}
}

// domainStats lifts a stored row into the value the rules are written
// against.
func domainStats(row application.Stats) player.Stats {
	return player.Stats{
		Level:      row.Level,
		XP:         row.XP,
		Health:     row.Health,
		MaxHealth:  row.MaxHealth,
		Energy:     row.Energy,
		MaxEnergy:  row.MaxEnergy,
		Happiness:  row.Happiness,
		Stamina:    row.Stamina,
		Reputation: row.Reputation,
	}
}

// storedStats writes a domain value back onto the row it came from, keeping
// the columns the domain deliberately does not carry.
func storedStats(row application.Stats, s player.Stats) application.Stats {
	row.Level = s.Level
	row.XP = s.XP
	row.Health = s.Health
	row.MaxHealth = s.MaxHealth
	row.Energy = s.Energy
	row.MaxEnergy = s.MaxEnergy
	row.Happiness = s.Happiness
	row.Stamina = s.Stamina
	row.Reputation = s.Reputation
	return row
}

// defaultStats is the row a player starts with. The numbers come from
// player.NewStats, so the game's starting condition is stated once, in the
// domain, and this layer only decides which columns to put it in.
func defaultStats(playerID string, now time.Time) application.Stats {
	row := storedStats(application.Stats{PlayerID: playerID}, player.NewStats())
	row.UpdatedAt = now
	return row
}

// regenerateEnergy advances a stats row to now and reports whether anything
// changed.
//
// The stored timestamp is advanced by player.EnergyRegenConsumed(elapsed) and
// NOT by elapsed. The difference is the leftover part of the current tick:
// throwing it away every time a player opens a screen would make an attentive
// player regenerate measurably slower than an idle one, which is the exact
// bug the domain documents on that function.
func regenerateEnergy(row application.Stats, now time.Time) (application.Stats, bool) {
	if row.UpdatedAt.IsZero() {
		// A row with no timestamp cannot say how long it has been waiting.
		// Stamping it now costs the player nothing and stops the next read
		// from inventing hours of regeneration out of the zero time.
		row.UpdatedAt = now
		return row, true
	}
	elapsed := now.Sub(row.UpdatedAt)
	if elapsed <= 0 {
		return row, false
	}

	before := domainStats(row)
	after := before.RegenerateEnergy(elapsed)
	if after.Energy == before.Energy {
		return row, false
	}

	next := storedStats(row, after)
	next.UpdatedAt = row.UpdatedAt.Add(player.EnergyRegenConsumed(elapsed))
	return next, true
}

// energyFullIn reports how long until a caught-up stats row has full energy
// again, zero when it already has.
//
// row must already be regenerated to now, so its UpdatedAt marks the start of
// the tick in progress: the first of the remaining ticks is part-way done,
// and the answer is exact rather than rounded up to whole ticks.
func energyFullIn(row application.Stats, now time.Time) time.Duration {
	missing := row.MaxEnergy - row.Energy
	if missing <= 0 || player.EnergyRegenAmount <= 0 {
		return 0
	}
	ticks := (missing + player.EnergyRegenAmount - 1) / player.EnergyRegenAmount
	left := time.Duration(ticks)*player.EnergyRegenInterval - now.Sub(row.UpdatedAt)
	if left < 0 {
		return 0
	}
	return left
}

// skillProgress reports how far a skill is toward its next level, as a
// percentage of the span between the two thresholds.
//
// Both thresholds come from the domain's curve. Nothing here knows what the
// curve is, which is what stops a second copy of it appearing in a screen.
func skillProgress(level int, xp int64) (next int64, percent int) {
	if level >= player.MaxSkillLevel {
		return player.SkillXPForLevel(player.MaxSkillLevel), 100
	}
	current := player.SkillXPForLevel(level)
	next = player.SkillXPForLevel(level + 1)
	span := next - current
	if span <= 0 {
		return next, 0
	}
	done := xp - current
	switch {
	case done <= 0:
		return next, 0
	case done >= span:
		return next, 100
	}
	return next, int(done * 100 / span)
}

// pageWindow turns a page number into the slice bounds of a list that is
// already in memory.
//
// Pages are numbered from one, the way they are shown, and a page past the
// end returns an empty window rather than an error: a player pressing next on
// a list that shrank under them should see an empty page, not a failure.
func pageWindow(total, page, size int) (start, end, pages int) {
	if size < 1 {
		size = DefaultPageSize
	}
	if page < 1 {
		page = 1
	}
	pages = (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	start = (page - 1) * size
	if start > total {
		start = total
	}
	end = start + size
	if end > total {
		end = total
	}
	return start, end, pages
}

// isSentinel reports whether target appears in err's chain AS THAT VALUE.
//
// It exists because internal/shared/errors matches with errors.Is BY CODE, so
// errors.Is(err, application.ErrNoActiveTravel) is true for ErrCityNotFound,
// ErrSkillNotFound, ErrPlayerNotFound and every other NOT_FOUND error. A
// handler deciding whether a player is already travelling cannot be asking a
// question that broad, so the sentinels are matched by identity instead.
func isSentinel(err, target error) bool {
	for e := err; e != nil; e = stderrors.Unwrap(e) {
		if e == target {
			return true
		}
	}
	return false
}

// parsePage reads a page number off a callback argument.
//
// Every positional argument arrives as a string, because callback data is a
// string and internal/gateway/routing deliberately does not interpret one. A
// value that is not a page number is read as the first page rather than
// refused: a player cannot type callback data by accident, so a bad page is
// either a stale button or someone experimenting, and neither deserves an
// error screen.
func parsePage(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// editableMessageID returns the message a response may replace.
//
// Only a CALLBACK carries one. The message an inline button sits on was sent
// by the bot, so the bot may edit it; the message of a typed command belongs
// to the player, and no bot may edit a user's message. Answering a command by
// editing would therefore fail at the API, which is why this checks for the
// callback and not merely for a non-zero message id.
func editableMessageID(meta envelope.Metadata) int64 {
	if meta.CallbackQueryID == nil || *meta.CallbackQueryID == "" {
		return 0
	}
	return meta.TelegramMessageID
}
