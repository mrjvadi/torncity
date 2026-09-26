package handlers

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// The life consumer (docs/adr/0025-life-and-legacy.md): the game's own
// events touch a life — a shift's stress, a promotion's joy, what a course
// teaches — write the life history, and move the rank of a player whose
// money moved. Each event touches each player once: its id goes into the
// life's inbox in the same transaction as everything it changes.

// LifeEventSubjects are the events the life consumer reads; cmd/game
// subscribes to each.
var LifeEventSubjects = []string{
	subjects.Event("player", "created"),
	subjects.Event("job", "hired"),
	subjects.Event("job", "promoted"),
	subjects.Event("job", "shift_worked"),
	subjects.Event("education", "completed"),
	subjects.Event("company", "founded"),
	subjects.Event("company", "closed"),
	subjects.Event("property", "bought"),
	subjects.Event("property", "sold"),
	subjects.Event("election", "result"),
	subjects.Event("governance", "appointed"),
	subjects.Event("governance", "dismissed"),
	subjects.Event("crime", "jailed"),
	subjects.Event("crime", "convicted"),
	subjects.Event("crime", "attempted"),
	subjects.Event("health", "hospitalised"),
	subjects.Event("war", "report"),
	subjects.Event("war", "city_struck"),
	subjects.Event("achievement", "awarded"),
	subjects.Event("market", "traded"),
}

// lifeEventPayload is what the life consumer reads off an event.
type lifeEventPayload struct {
	PlayerID   string `json:"player_id"`
	ThiefID    string `json:"thief_id"`
	OwnerID    string `json:"owner_id"`
	BuyerID    string `json:"buyer_id"`
	SellerID   string `json:"seller_id"`
	Career     string `json:"career"`
	Tier       int    `json:"tier"`
	CityID     string `json:"city_id"`
	Course     string `json:"course"`
	CourseName string `json:"course_name"`
	Certified  bool   `json:"certified"`
	Status     string `json:"status"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	TypeName   string `json:"type_name"`
	Office     string `json:"office"`
	PlaceKind  string `json:"place_kind"`
	PlaceCode  string `json:"place_code"`
	PlaceName  string `json:"place_name"`
	Elected    bool   `json:"elected"`
	Votes      int64  `json:"votes"`
	Result     string `json:"result"`
	Kind       string `json:"kind"`
	Price      int64  `json:"price"`
	Amount     int64  `json:"amount"`
	Item       string `json:"item"`
	Qty        int64  `json:"qty"`
	CityCode   string `json:"city_code"`
	CityName   string `json:"city_name"`
	Report     *struct {
		Ours      bool
		CalledOff bool
		Captured  bool
		CityCode  string
		City      string
		Kind      string
	} `json:"report"`
}

// lifeTouch is what an event does to one player's life.
type lifeTouch struct {
	playerID string
	// event is the kind of event (life.yml events) whose stress and
	// happiness apply, "" for none.
	event string
	// entry is the history entry it writes, nil for none; firstJob marks a
	// hiring that may be the player's first job.
	entry    *application.HistoryEntry
	firstJob bool
	// learned is a course finished (certified: with a certificate).
	learned, certified bool
	// money says the player's worth moved: their rank is judged again.
	money bool
	// cityID is where it happened, for the entry's place.
	cityID string
}

// entry starts a history entry of a kind.
func entry(kind string, public bool, d application.HistoryData) *application.HistoryEntry {
	return &application.HistoryEntry{Kind: kind, Public: public, Data: d}
}

// touches is what one event does, to whom.
func (h *LifeHandler) touches(snap *content.Snapshot, def content.LifeDef, subject string, ev lifeEventPayload) []lifeTouch {
	switch subject {
	case subjects.Event("player", "created"):
		return []lifeTouch{{playerID: ev.PlayerID, entry: entry(application.HistoryJoined, true, application.HistoryData{})}}
	case subjects.Event("job", "hired"), subjects.Event("job", "promoted"):
		kind, event := application.HistoryHired, ""
		if subject == subjects.Event("job", "promoted") {
			kind, event = application.HistoryPromoted, content.LifePromoted
		}
		d := application.HistoryData{Code: ev.Career}
		if cd, ok := snap.CareerDef(ev.Career); ok {
			ref := jobRef(cd, ev.Tier)
			d.Sub, d.Name = ref.Rank, ref.Title
		}
		return []lifeTouch{{playerID: ev.PlayerID, event: event, entry: entry(kind, true, d),
			firstJob: kind == application.HistoryHired, cityID: ev.CityID}}
	case subjects.Event("job", "shift_worked"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeShiftWorked, money: true}}
	case subjects.Event("education", "completed"):
		if ev.Status != "completed" {
			return nil
		}
		kind := application.HistoryCourse
		if ev.Certified {
			kind = application.HistoryCertificate
		}
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeCourseCompleted, learned: true,
			certified: ev.Certified, entry: entry(kind, true, application.HistoryData{Code: ev.Course, Name: ev.CourseName})}}
	case subjects.Event("company", "founded"):
		return []lifeTouch{{playerID: ev.PlayerID, money: true, cityID: ev.CityID,
			entry: entry(application.HistoryCompanyFounded, true, application.HistoryData{Code: ev.Type, Name: ev.Name,
				Sub: ev.Code})}}
	case subjects.Event("company", "closed"):
		return []lifeTouch{{playerID: ev.OwnerID, money: true, cityID: ev.CityID,
			entry: entry(application.HistoryCompanyClosed, true, application.HistoryData{Code: ev.Type, Name: ev.Name,
				Sub: ev.Code})}}
	case subjects.Event("property", "bought"):
		d := application.HistoryData{Code: ev.Type, Amount: ev.Price}
		if t, ok := snap.PropertyType(ev.Type); ok {
			d.Name = t.Name
		}
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifePropertyBought, money: true, cityID: ev.CityID,
			entry: entry(application.HistoryPropertyBought, true, d)}}
	case subjects.Event("property", "sold"):
		if ev.Kind != "sold" {
			return nil
		}
		return []lifeTouch{{playerID: ev.PlayerID, money: true, entry: entry(application.HistoryPropertySold, true,
			application.HistoryData{Code: ev.Type, Name: ev.TypeName, Amount: ev.Amount, PlaceKind: "city",
				PlaceCode: ev.CityCode, PlaceName: ev.CityName})}}
	case subjects.Event("election", "result"):
		kind, event := application.HistoryElectionLost, ""
		if ev.Elected {
			kind, event = application.HistoryElectionWon, content.LifeElectionWon
		}
		return []lifeTouch{{playerID: ev.PlayerID, event: event, entry: entry(kind, true, application.HistoryData{
			Code: ev.Office, PlaceKind: ev.PlaceKind, PlaceCode: ev.PlaceCode, PlaceName: ev.PlaceName, Number: ev.Votes})}}
	case subjects.Event("governance", "appointed"), subjects.Event("governance", "dismissed"):
		kind := application.HistoryOfficeTaken
		if subject == subjects.Event("governance", "dismissed") {
			kind = application.HistoryOfficeLost
		}
		return []lifeTouch{{playerID: ev.PlayerID, entry: entry(kind, true, application.HistoryData{Code: ev.Office,
			PlaceKind: ev.PlaceKind, PlaceCode: ev.PlaceCode, PlaceName: ev.PlaceName})}}
	case subjects.Event("crime", "jailed"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeJailed, cityID: ev.CityID,
			entry: entry(application.HistoryJailed, true, application.HistoryData{})}}
	case subjects.Event("crime", "convicted"):
		return []lifeTouch{{playerID: ev.ThiefID, money: true,
			entry: entry(application.HistoryConvicted, true, application.HistoryData{})}}
	case subjects.Event("crime", "attempted"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeCrimeAttempted, money: ev.Result == "succeeded"}}
	case subjects.Event("health", "hospitalised"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeHospitalised, cityID: ev.CityID,
			entry: entry(application.HistoryHospitalised, false, application.HistoryData{})}}
	case subjects.Event("war", "report"):
		if ev.Report == nil || !ev.Report.Ours || ev.Report.CalledOff {
			return nil
		}
		var n int64
		if ev.Report.Captured {
			n = 1
		}
		return []lifeTouch{{playerID: ev.PlayerID, entry: entry(application.HistoryWarCommand, true,
			application.HistoryData{Code: ev.Report.Kind, PlaceKind: "city", PlaceCode: ev.Report.CityCode,
				PlaceName: ev.Report.City, Number: n})}}
	case subjects.Event("war", "city_struck"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeWarStruck}}
	case subjects.Event("achievement", "awarded"):
		return []lifeTouch{{playerID: ev.PlayerID, event: content.LifeAchievementAwarded, money: true,
			entry: entry(application.HistoryAchievement, true, application.HistoryData{Code: ev.Code, Name: ev.Name})}}
	case subjects.Event("market", "traded"):
		value := ev.Price * ev.Qty
		var out []lifeTouch
		for _, who := range []string{ev.BuyerID, ev.SellerID} {
			t := lifeTouch{playerID: who, money: true, cityID: ev.CityID}
			if value >= def.History.BigTrade && ev.Qty > 0 && ev.Price > 0 {
				d := application.HistoryData{Code: ev.Item, Amount: value, Number: ev.Qty}
				if it, ok := snap.ItemDef(ev.Item); ok {
					d.Name = it.Name
				}
				t.entry = entry(application.HistoryBigTrade, true, d)
			}
			out = append(out, t)
		}
		return out
	}
	return nil
}

// OnEvent lets one event touch the lives it is about, each once. An error
// means "not yet": the event is redelivered.
func (h *LifeHandler) OnEvent(ctx context.Context, env *envelope.Envelope, subject string) error {
	snap := h.content.Current()
	def, ok := snap.Life()
	if !ok {
		return nil
	}
	var ev lifeEventPayload
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil // unreadable: it never will be
	}
	eventID := env.Metadata.MessageID() + ":" + subject
	for _, t := range h.touches(snap, def, subject, ev) {
		if t.playerID == "" {
			continue
		}
		if err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return h.touch(ctx, tx, snap, def, env.Metadata, subject, eventID, t)
		}); err != nil {
			return err
		}
	}
	return nil
}

// touch applies what an event does to one player's life, once.
func (h *LifeHandler) touch(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.LifeDef,
	meta envelope.Metadata, subject, eventID string, t lifeTouch,
) error {
	p, err := tx.Players().GetByID(ctx, t.playerID)
	if isSentinel(err, application.ErrPlayerNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	now := h.now()
	// The stats row, then the life row: the order every change to a life
	// locks them in. Only then the inbox, so a second delivery waits and
	// finds it marked.
	if _, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now)); err != nil {
		return err
	}
	if _, err := tx.Life().Ensure(ctx, lifeDefaults(def, p.ID, p.CreatedAt, now)); err != nil {
		return err
	}
	fresh, err := tx.Life().MarkEvent(ctx, eventID, p.ID, subject, now)
	if err != nil || !fresh {
		return err
	}
	effect := def.Event(t.event)
	mind := def.Mind()
	l, err := touchLife(ctx, tx, snap, h.scale, p, now, meta, h.hungerAlertCooldown, func(l *lifeNow) {
		if t.event != "" {
			l.needs = l.needs.Change(0, 0, effect.Stress)
			l.happiness += effect.Happiness
		}
		if t.learned {
			l.row.Intelligence = mind.Learn(l.row.Intelligence, t.certified)
		}
	})
	if err != nil || l == nil {
		return err
	}
	if t.entry != nil {
		e := *t.entry
		e.PlayerID, e.At, e.Source = p.ID, now, eventID
		if t.firstJob {
			firsts, err := tx.Life().CountHistory(ctx, p.ID, application.HistoryFirstJob)
			if err != nil {
				return err
			}
			hires, err := tx.Life().CountHistory(ctx, p.ID, application.HistoryHired)
			if err != nil {
				return err
			}
			if firsts+hires == 0 {
				e.Kind = application.HistoryFirstJob
			}
		}
		if t.cityID != "" && e.Data.PlaceCode == "" {
			if city, err := h.cities.ByID(ctx, t.cityID); err == nil {
				e.Data.PlaceKind, e.Data.PlaceCode, e.Data.PlaceName = "city", city.Code, city.Name
			} else if !isSentinel(err, application.ErrCityNotFound) {
				return err
			}
		}
		if _, err := tx.Life().AddHistory(ctx, e); err != nil {
			return err
		}
	}
	if t.money {
		worth, err := worthOf(ctx, tx, snap, h.cities, p.ID)
		if err != nil {
			return err
		}
		if err := judgeRank(ctx, tx, snap, meta, l.row, worth.Total(), eventID, now); err != nil {
			return err
		}
		return tx.Life().Save(ctx, *l.row)
	}
	return nil
}
