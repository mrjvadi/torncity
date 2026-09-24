package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// What the military and diplomacy handlers share
// (docs/adr/0022-military-and-diplomacy.md): which country a player belongs
// to, who may take a decision for a country, and the one refusal a sanction
// answers a blocked cross-border action with.

// The country levers of the armed forces (configs/content/governance.yml).
// They are read only through application.PolicyReader.
const (
	LeverRevenueShare  = "country.revenue_share"
	LeverDefenceBudget = "country.defence_budget"
	LeverArmsExports   = "country.arms_exports"
)

// countryPlace names a country for a screen.
func countryPlace(j application.Jurisdiction) screens.GovPlace {
	return screens.GovPlace{Kind: j.Kind, Code: j.Code, Name: j.Name}
}

// cityPlace names a city for a screen.
func cityPlace(c application.City) screens.GovPlace {
	return screens.GovPlace{Kind: "city", Code: c.Code, Name: c.Name}
}

// nationality is the country a player belongs to: the country of the city
// they live in, or — for one who lives nowhere yet — of the city they stand
// in; "" for neither.
func nationality(ctx context.Context, tx application.Tx, p *application.Player) (string, error) {
	byPlayer, err := tx.Diplomacy().CountriesOfPlayers(ctx, []string{p.ID})
	if err != nil {
		return "", err
	}
	if c := byPlayer[p.ID]; c != "" {
		return c, nil
	}
	if p.CityID == nil || *p.CityID == "" {
		return "", nil
	}
	return tx.Diplomacy().CountryOfCity(ctx, *p.CityID)
}

// countryFor resolves the country a request names by code, or the player's
// own. It returns nil when the request names none and the player belongs to
// none, and application.ErrJurisdictionNotFound for a code that names no
// country.
func countryFor(ctx context.Context, tx application.Tx, p *application.Player, code string) (*application.Jurisdiction, error) {
	if code = strings.ToLower(strings.TrimSpace(code)); code != "" {
		j, err := tx.Diplomacy().CountryByCode(ctx, code)
		if err != nil {
			return nil, err
		}
		return &j, nil
	}
	id, err := nationality(ctx, tx, p)
	if err != nil || id == "" {
		return nil, err
	}
	j, err := tx.Governance().Jurisdiction(ctx, id)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// actionOffice is the office that takes an action in the active content, ""
// when the content has no such action.
func actionOffice(snap *content.Snapshot, action string) string {
	a, ok := snap.Action(action)
	if !ok {
		return ""
	}
	return a.HeldBy
}

// mayAct answers whether the player may take an action for a country: its
// office's holder, or the deputy acting for it. It returns the seat acted
// from; a player who may not is (Office{}, false, nil).
func mayAct(ctx context.Context, tx application.Tx, snap *content.Snapshot, countryID, action string,
	p *application.Player,
) (application.Office, bool, error) {
	office := actionOffice(snap, action)
	if office == "" {
		return application.Office{}, false, nil
	}
	seat, err := application.Authorize(ctx, tx, countryID, office, p.ID)
	if stderrors.Is(err, application.ErrNotOfficeHolder) {
		return application.Office{}, false, nil
	}
	if err != nil {
		return application.Office{}, false, err
	}
	return seat, true, nil
}

// cleared reports whether the player holds, in the country, one of the
// offices military.yml clears to see its forces in full.
func cleared(ctx context.Context, tx application.Tx, snap *content.Snapshot, countryID string, p *application.Player) (bool, error) {
	held, err := tx.Governance().SeatsHeldBy(ctx, p.ID)
	if err != nil {
		return false, err
	}
	for _, s := range held {
		if s.JurisdictionID == countryID && hasCode(snap.MilitaryClearance(), s.OfficeCode) {
			return true, nil
		}
	}
	return false, nil
}

// officeLine is one office of a country and who holds it or acts for it, as
// the city hall screen shows it.
func officeLine(ctx context.Context, tx application.Tx, code, countryID string) (screens.GovOffice, error) {
	chain, err := tx.Governance().ActingChain(ctx, code, countryID)
	if err != nil {
		return screens.GovOffice{}, err
	}
	view := screens.GovOffice{Code: code, Seats: 1}
	if len(chain) > 0 {
		view.Seats = max(len(chain[0].Seats), 1)
	}
	acting := application.ActingForChain(chain)
	if acting == nil {
		return view, nil
	}
	var players []screens.GovPlayer
	for _, s := range acting.Holders {
		named, err := playerNamed(ctx, tx, s.HolderPlayerID)
		if err != nil {
			return view, err
		}
		players = append(players, named)
	}
	if acting.Deputy {
		view.ActingCode, view.Acting = acting.OfficeCode, players
	} else {
		view.Holders = players
	}
	return view, nil
}

// placeOf names a country by id for a screen; an unknown one is blank.
func placeOf(ctx context.Context, tx application.Tx, id string) (screens.GovPlace, error) {
	if id == "" {
		return screens.GovPlace{}, nil
	}
	j, err := tx.Governance().Jurisdiction(ctx, id)
	if err != nil {
		return screens.GovPlace{}, err
	}
	return countryPlace(j), nil
}

// sanctionBlocked turns a *SanctionedError into its screen's view, naming
// both countries; ok is false for any other error.
func sanctionBlocked(ctx context.Context, tx application.Tx, err error, back ...string) (screens.SanctionBlockedView, bool, error) {
	var s *application.SanctionedError
	if !stderrors.As(err, &s) {
		return screens.SanctionBlockedView{}, false, nil
	}
	imposer, ierr := placeOf(ctx, tx, s.Sanction.ImposerID)
	if ierr != nil {
		return screens.SanctionBlockedView{}, true, ierr
	}
	target, terr := placeOf(ctx, tx, s.Sanction.TargetID)
	if terr != nil {
		return screens.SanctionBlockedView{}, true, terr
	}
	return screens.SanctionBlockedView{Measure: string(s.Measure), Imposer: imposer, Target: target, Back: back}, true, nil
}

// sanctionRefusal carries a blocked cross-border action out of a unit of
// work, with the screen already named.
type sanctionRefusal struct{ view screens.SanctionBlockedView }

func (r *sanctionRefusal) Error() string { return "handlers: blocked by a sanction: " + r.view.Measure }

// checkSanctions runs THE sanctions check (application.CheckSanctions) and
// returns a *sanctionRefusal for a blocked action, ready for its screen.
func checkSanctions(ctx context.Context, tx application.Tx, err error, back ...string) error {
	if err == nil {
		return nil
	}
	view, ok, rerr := sanctionBlocked(ctx, tx, err, back...)
	if rerr != nil {
		return rerr
	}
	if !ok {
		return err
	}
	return &sanctionRefusal{view: view}
}

// asBlocked finds a blocked action's screen in err.
func asBlocked(err error) (screens.SanctionBlockedView, bool) {
	var r *sanctionRefusal
	if stderrors.As(err, &r) {
		return r.view, true
	}
	return screens.SanctionBlockedView{}, false
}

// appendDomainEvent writes an event of a domain to the outbox, in the
// caller's transaction.
func appendDomainEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, domain, name, aggregateID string,
	payload map[string]any,
) error {
	ev, err := events.New(domain+"."+name, domain, aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event(domain, name), Metadata: meta, Payload: ev.Payload,
	})
}

// cityIDsOf lists the ids of countries' cities, for an announcement in all
// their groups.
func cityIDsOf(ctx context.Context, tx application.Tx, countries ...string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, c := range countries {
		cities, err := tx.Diplomacy().CitiesOf(ctx, c)
		if err != nil {
			return nil, err
		}
		for _, city := range cities {
			if !seen[city.ID] {
				seen[city.ID] = true
				out = append(out, city.ID)
			}
		}
	}
	return out, nil
}

// playersSanctioned runs the sanctions check between two players, each of
// the country they live in, and returns a refusal ready for its screen when
// the measure blocks them.
func playersSanctioned(ctx context.Context, tx application.Tx, m diplomacy.Measure, a, b string, now time.Time,
	back ...string,
) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	countries, err := tx.Diplomacy().CountriesOfPlayers(ctx, []string{a, b})
	if err != nil {
		return err
	}
	return checkSanctions(ctx, tx, application.CheckSanctions(ctx, tx, m, countries[a], countries[b], now), back...)
}
