package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/life"
	"github.com/mrjvadi/torncity/internal/domain/property"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds what every feature reads of a character's life
// (docs/adr/0025-life-and-legacy.md): catching a life up to now, what a
// hard-pressed body and a low mood cost, what intelligence speeds up, what
// a player is worth and the rank that follows from it, and the life
// history. Nothing ticks: a life is caught up whenever it is looked at or
// something happens to it.

// lifeNow is a life caught up to an instant.
type lifeNow struct {
	row       *application.PlayerLife
	stats     *application.Stats
	needs     life.Needs
	happiness int
	factors   application.LifeFactors
	effects   life.Effects
}

// homeTypes are the kinds of property a player can live in.
func homeTypes(snap *content.Snapshot) []string {
	var out []string
	for _, t := range snap.PropertyTypes() {
		if t.Home {
			out = append(out, t.Code)
		}
	}
	return out
}

// lifeDefaults is a new life: needs where content starts them, born when
// the player joined.
func lifeDefaults(def content.LifeDef, playerID string, bornAt, now time.Time) application.PlayerLife {
	if bornAt.IsZero() || bornAt.After(now) {
		bornAt = now
	}
	s := def.Needs.Start
	return application.PlayerLife{PlayerID: playerID, BornAt: bornAt, Hunger: int64(s.Hunger) * life.Milli,
		Sleep: int64(s.Sleep) * life.Milli, Stress: int64(s.Stress) * life.Milli, NeedsAt: now, HappinessAt: now,
		Intelligence: def.Intelligence.Start, CreatedAt: now, UpdatedAt: now}
}

// touchLife catches a player's life up to now and writes it back: the needs
// drifted, happiness moved toward what the life gives it, energy caught up
// at the old pace and then the new pace set. change, when given, runs on the
// caught-up life before it is written (a meal, a night's sleep, a shift's
// stress). It returns nil when the content has no life or the transaction
// no life repository.
//
// meta and hungerAlertCooldown are for exactly one thing: the urgent "you
// are hungry" notice (life.hunger_low), fired here — where hunger is caught
// up for EVERY handler, lazily, whichever one happens to be looked at next —
// rather than by any one of them. meta is the caller's own inbound request,
// the same one every other event this call may append rides on
// (judgeRank does the same for life.rank_changed); hungerAlertCooldown is
// notifications.hunger_alert_cooldown, threaded in by whichever handler was
// built WithHungerAlert.
func touchLife(ctx context.Context, tx application.Tx, snap *content.Snapshot, scale gametime.Scale,
	p *application.Player, now time.Time, meta envelope.Metadata, hungerAlertCooldown time.Duration,
	change func(*lifeNow),
) (*lifeNow, error) {
	def, ok := snap.Life()
	if !ok || tx.Life() == nil || scale.Validate() != nil {
		return nil, nil
	}
	stats, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
	if err != nil {
		return nil, err
	}
	// Energy up to now at the pace it has run at, before the pace moves.
	caught, changed := regenerateEnergy(*stats, now)
	if changed {
		if err := tx.Stats().Save(ctx, caught); err != nil {
			return nil, err
		}
	}
	stats = &caught
	row, err := tx.Life().Ensure(ctx, lifeDefaults(def, p.ID, p.CreatedAt, now))
	if err != nil {
		return nil, err
	}
	factors, err := tx.Life().Factors(ctx, p.ID, homeTypes(snap))
	if err != nil {
		return nil, err
	}
	l := &lifeNow{row: row, stats: stats, factors: factors}
	l.needs = row.Needs().At(now, scale, def.Drift(), stats.Happiness)
	cond := def.Condition()

	// The drift alone — before change touches anything, a meal included —
	// is what "since you were last looked at, you went hungry" means. row
	// still holds the value as of the last touch, so this is the one place
	// that can tell a crossing from a level that was already high.
	if err := alertHunger(ctx, tx, meta, cond, row, l.needs.Hunger, now, hungerAlertCooldown); err != nil {
		return nil, err
	}

	pressing := len(cond.Effects(l.needs, stats.Happiness).Pressing)
	target := def.MoodRules().Target(life.Life{Home: factors.Home, Friends: factors.Friends, Faction: factors.Faction,
		Achievements: factors.Achievements}, pressing)
	var at time.Time
	l.happiness, at = def.MoodRules().Drift(stats.Happiness, target, row.HappinessAt, now, scale)
	row.HappinessAt = at
	if change != nil {
		change(l)
	}
	l.happiness = min(max(l.happiness, 0), life.MaxPoints)
	l.effects = cond.Effects(l.needs, l.happiness)
	row.SetNeeds(l.needs)
	row.UpdatedAt = now
	if err := tx.Life().Save(ctx, *row); err != nil {
		return nil, err
	}
	if l.happiness != stats.Happiness {
		stats.Happiness = l.happiness
		if err := tx.Stats().Save(ctx, *stats); err != nil {
			return nil, err
		}
	}
	if err := tx.Life().SetRegen(ctx, p.ID, l.effects.BodyBPS); err != nil {
		return nil, err
	}
	stats.RegenBPS = l.effects.BodyBPS
	return l, nil
}

// alertHunger appends life.hunger_low the first time hunger crosses the
// content-defined High threshold (life.yml needs.high) since it was last
// below it, subject to cooldown: a hunger sitting right at the line must not
// resend it on every command that happens to catch the life up. cooldown
// zero — a handler never built WithHungerAlert — disables the cooldown check
// but not the crossing itself, so every caller of touchLife still notices a
// genuine crossing; only cmd/game's real wiring sets it.
func alertHunger(ctx context.Context, tx application.Tx, meta envelope.Metadata, cond life.Condition,
	row *application.PlayerLife, hunger int64, now time.Time, cooldown time.Duration,
) error {
	threshold := int64(cond.High) * life.Milli
	if row.Hunger >= threshold || hunger < threshold {
		// Already pressing before this touch, or not pressing now: no
		// crossing happened just now.
		return nil
	}
	if cooldown > 0 && row.HungerAlertAt != nil && now.Sub(*row.HungerAlertAt) < cooldown {
		return nil
	}
	row.HungerAlertAt = &now
	return appendDomainEvent(ctx, tx, meta, "life", "hunger_low", row.PlayerID, map[string]any{"player_id": row.PlayerID})
}

// lifeEffects is what a player's condition does to what they are about to
// do, read without writing: neutral without a life.
func lifeEffects(ctx context.Context, tx application.Tx, snap *content.Snapshot, scale gametime.Scale, playerID string,
	happiness int, now time.Time,
) (life.Effects, error) {
	neutral := life.Effects{BodyBPS: 10000, XPBPS: 10000}
	def, ok := snap.Life()
	if !ok || tx.Life() == nil || scale.Validate() != nil {
		return neutral, nil
	}
	row, err := tx.Life().Get(ctx, playerID)
	if err != nil || row == nil {
		return neutral, err
	}
	return def.Condition().Effects(row.Needs().At(now, scale, def.Drift(), happiness), happiness), nil
}

// moodXP is experience as a player's mood lets it come: a low mood learns
// slower (life.yml mood.low).
func moodXP(snap *content.Snapshot, happiness int, xp int64) int64 {
	def, ok := snap.Life()
	if !ok || xp <= 0 {
		return xp
	}
	return life.Scale(xp, def.Condition().Effects(life.Needs{}, happiness).XPBPS)
}

// intelligence is a player's intelligence, and the rules of it; ok is
// false without a life.
func intelligence(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string) (int, life.Mind, bool, error) {
	def, ok := snap.Life()
	if !ok || tx.Life() == nil {
		return 0, life.Mind{}, false, nil
	}
	row, err := tx.Life().Get(ctx, playerID)
	if err != nil {
		return 0, life.Mind{}, false, err
	}
	iq := def.Intelligence.Start
	if row != nil {
		iq = row.Intelligence
	}
	return iq, def.Mind(), true, nil
}

// smarterSkillXP raises skill experience by a player's intelligence.
func smarterSkillXP(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string,
	awards []skillAward,
) ([]skillAward, error) {
	if snap == nil || len(awards) == 0 {
		return awards, nil
	}
	iq, mind, ok, err := intelligence(ctx, tx, snap, playerID)
	if err != nil || !ok {
		return awards, err
	}
	out := make([]skillAward, len(awards))
	for i, a := range awards {
		out[i] = skillAward{Skill: a.Skill, XP: mind.SkillXP(a.XP, iq)}
	}
	return out, nil
}

// netWorthPrices are what the content puts on a player's goods and
// property: goods at their reference prices, each kind of property at what
// its city asks for one more now. Only the kinds in cities is priced; nil
// prices every city's market.
func netWorthPrices(ctx context.Context, tx application.Tx, snap *content.Snapshot, cities application.CityRepository,
	owned []application.Property,
) (application.NetWorthPrices, error) {
	prices := application.NetWorthPrices{Items: map[string]int64{}, PropertyPrices: map[application.PropertyKey]int64{}}
	for _, it := range snap.Items() {
		prices.Items[it.Code] = it.BasePrice
	}
	// Gold at what the dealer would pay for it now (docs/adr/0026).
	if fin, ok := snap.Finance(); ok && tx.Finance() != nil {
		d, err := tx.Finance().Dealer(ctx, application.GoldDealer{Price: fin.Gold.StartPrice, Reserve: fin.Gold.Reserve,
			UpdatedAt: time.Now().UTC()}, false)
		if err != nil {
			return prices, err
		}
		_, prices.GoldBid = fin.GoldRules().Quote(d.Price)
	}
	def, ok := snap.Property()
	if !ok || cities == nil {
		return prices, nil
	}
	price := func(city *application.City, typeCode string) error {
		key := application.PropertyKey{CityID: city.ID, Type: typeCode}
		if _, done := prices.PropertyPrices[key]; done {
			return nil
		}
		t, ok := snap.PropertyType(typeCode)
		market, ok2 := snap.PropertyMarket(city.Code)
		if !ok || !ok2 {
			return nil
		}
		sold, err := tx.Property().Owned(ctx, city.ID, typeCode)
		if err != nil {
			return err
		}
		prices.PropertyPrices[key] = property.CityPrice(t.Type(), market.PriceBPS, sold, def.Demand())
		return nil
	}
	if owned != nil {
		for _, pr := range owned {
			city, err := cities.ByID(ctx, pr.CityID)
			if isSentinel(err, application.ErrCityNotFound) {
				continue
			}
			if err != nil {
				return prices, err
			}
			if err := price(city, pr.TypeCode); err != nil {
				return prices, err
			}
		}
		return prices, nil
	}
	all, err := cities.List(ctx)
	if err != nil {
		return prices, err
	}
	for i := range all {
		for _, t := range snap.PropertyTypes() {
			if err := price(&all[i], t.Code); err != nil {
				return prices, err
			}
		}
	}
	return prices, nil
}

// worthOf is what one player is worth now.
func worthOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, cities application.CityRepository,
	playerID string,
) (life.Worth, error) {
	owned, err := tx.Property().OfOwner(ctx, playerID)
	if err != nil {
		return life.Worth{}, err
	}
	if owned == nil {
		owned = []application.Property{}
	}
	prices, err := netWorthPrices(ctx, tx, snap, cities, owned)
	if err != nil {
		return life.Worth{}, err
	}
	rows, err := tx.Life().NetWorth(ctx, prices, playerID)
	if err != nil || len(rows) == 0 {
		return life.Worth{}, err
	}
	return rows[0].Worth, nil
}

// judgeRank sets the rank a life holds by what it is worth, with the
// ladder's band, and records a change: the history and a notice. It writes
// the row itself only through the caller's Save.
func judgeRank(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	row *application.PlayerLife, worth int64, source string, now time.Time,
) error {
	def, ok := snap.Life()
	if !ok {
		return nil
	}
	ladder := def.Ladder()
	next, dir := ladder.Next(row.Rank, worth)
	row.NetWorth, row.NetWorthAt = worth, &now
	if next == row.Rank {
		return nil
	}
	from := row.Rank
	row.Rank, row.RankSince = next, &now
	if from == "" || dir == 0 {
		// A first rank, or a rank the ladder no longer has, is taken
		// quietly: nothing rose or fell.
		return nil
	}
	kind := application.HistoryRankUp
	if dir < 0 {
		kind = application.HistoryRankDown
	}
	to, _ := def.Rank(next)
	was, _ := def.Rank(from)
	if _, err := tx.Life().AddHistory(ctx, application.HistoryEntry{PlayerID: row.PlayerID, Kind: kind, At: now,
		Public: true, Source: fmt.Sprintf("rank:%s:%s:%d", source, next, now.UnixMilli()),
		Data: application.HistoryData{Code: next, Name: to.Name, Sub: from, SubName: was.Name, Amount: worth}}); err != nil {
		return err
	}
	return appendDomainEvent(ctx, tx, meta, "life", "rank_changed", row.PlayerID, map[string]any{
		"player_id": row.PlayerID, "rank": next, "rank_name": to.Name, "rank_emoji": to.Emoji, "from": from,
		"from_name": was.Name, "up": dir > 0, "net_worth": worth})
}

// rankRef is a rank as screens show it.
func rankRef(def content.LifeDef, code string) *screens.RankRef {
	r, ok := def.Rank(code)
	if !ok {
		return nil
	}
	return &screens.RankRef{Code: r.Code, Name: r.Name, Emoji: r.Emoji}
}

// needsView is a life's needs as screens show them.
func needsView(l *lifeNow) *screens.NeedsView {
	if l == nil {
		return nil
	}
	return &screens.NeedsView{Hunger: life.Points(l.needs.Hunger), Sleep: life.Points(l.needs.Sleep),
		Stress: life.Points(l.needs.Stress), Happiness: l.happiness, BodyBPS: l.effects.BodyBPS, XPBPS: l.effects.XPBPS,
		Pressing: l.effects.Pressing}
}
