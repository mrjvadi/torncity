package clientapi

import (
	"context"
	"errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Bootstrap is what a client loads once after signing in: who the player
// is, where, and the names it needs to draw codes the views carry.
type Bootstrap struct {
	Player         BootstrapPlayer `json:"player"`
	ContentVersion int             `json:"content_version"`
	Languages      []NamedCode     `json:"languages"`
	// Cities are every city, Places the places of the player's city, each
	// by code with its name in the player's language.
	Cities     []NamedCode `json:"cities"`
	Places     []NamedCode `json:"places"`
	ServerTime string      `json:"server_time"`
	// Settlement is the player's own settlement, absent when they belong to
	// none (api/client-api.md, "The world").
	Settlement *BootstrapSettlement `json:"settlement,omitempty"`
	// Realtime says whether the realtime tokens can be had.
	Realtime bool `json:"realtime"`
	// Location is the place the player stands in now: Support or a founded
	// village, which is not always their own settlement (a traveller stands
	// in another group's village). Absent for a player who is nowhere.
	Location *BootstrapLocation `json:"location,omitempty"`
	// Features are the optional parts of the contract this server serves.
	Features BootstrapFeatures `json:"features"`
}

// BootstrapFeatures says which optional parts of the contract are on.
type BootstrapFeatures struct {
	// Updates: client state sync (GET /state, GET /updates, the "updates"
	// publication and a command's "updates"; contract 1.4, docs/adr/0034).
	// A client keeps its store from these instead of polling.
	Updates bool `json:"updates"`
}

// Kinds of BootstrapLocation.
const (
	// LocationCity is a content city (Support): code names it, there is no
	// settlement to fetch.
	LocationCity = "city"
	// LocationSettlement is a founded village or grown settlement:
	// settlement_id names it for the layout and the roster.
	LocationSettlement = "settlement"
)

// BootstrapLocation is where the player stands. Standing is not living: the
// player's own settlement is bootstrap.settlement, and Home says whether it
// is this place.
type BootstrapLocation struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
	Name string `json:"name"`
	// Centre is where the place stands on the world: the middle of a
	// village's cell, a content city's configured spot.
	Centre *Place `json:"centre,omitempty"`
	// Home says the player lives here (their residence).
	Home bool `json:"home"`

	// The rest is set for a settlement only.
	SettlementID string        `json:"settlement_id,omitempty"`
	Tier         string        `json:"tier,omitempty"`
	WorldCell    *int32        `json:"world_cell,omitempty"`
	GridLots     int           `json:"grid_lots,omitempty"`
	LayoutPath   string        `json:"layout_path,omitempty"`
	Emblem       *EmblemView   `json:"emblem,omitempty"`
	Motto        string        `json:"motto,omitempty"`
	Currency     *CurrencyView `json:"currency,omitempty"`
}

// Spot is a content city's place on the world (config travel.city_locations).
type Spot struct{ Lat, Lon float64 }

// BootstrapPlayer is the signed-in player.
type BootstrapPlayer struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Lang     string `json:"lang"`
	CityCode string `json:"city_code,omitempty"`
	City     string `json:"city,omitempty"`
}

// NamedCode is a content code with its display name.
type NamedCode struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Cities reads a city by id.
type Cities interface {
	ByID(ctx context.Context, id string) (*application.City, error)
}

// Catalogue is the message catalogue, as the bootstrap reads it.
type Catalogue interface {
	screens.Translator
	Languages() []string
}

// World assembles the bootstrap and answers where a player is.
type World struct {
	Players Players
	Cities  Cities
	// CityCodes and Companies draw the city map (worldmap.go).
	CityCodes CityDirectory
	Companies CompanyDirectory
	// Villages finds the player's settlement for the bootstrap; nil leaves
	// it out.
	Villages *VillageService
	// CitySpots are the content cities' places on the world by code, for the
	// location of a player standing in one (config travel.city_locations).
	CitySpots map[string]Spot
	Content   *content.Registry
	Msgs      Catalogue
	Realtime  bool
	Now       func() time.Time
}

// CityCode is the code of the city the player is in, empty when none.
func (w *World) CityCode(ctx context.Context, playerID string) (string, error) {
	p, err := w.Players.GetByID(ctx, playerID)
	if err != nil {
		return "", err
	}
	return w.cityOf(ctx, p)
}

func (w *World) cityOf(ctx context.Context, p *application.Player) (string, error) {
	if p.CityID == nil || *p.CityID == "" {
		return "", nil
	}
	c, err := w.Cities.ByID(ctx, *p.CityID)
	if apperrors.CodeOf(err) == apperrors.CodeNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return c.Code, nil
}

// location is the place the player stands in: a founded settlement when the
// city is one, else the content city. mine is the player's own settlement,
// to tell whether they live here.
func (w *World) location(ctx context.Context, c screens.Context, cityID, cityCode string, mine *BootstrapSettlement) (*BootstrapLocation, error) {
	if w.Villages != nil {
		s, err := w.Villages.Settlements.ByID(ctx, cityID)
		switch {
		case err == nil:
			cell := s.WorldCellID
			out := &BootstrapLocation{Kind: LocationSettlement, Code: s.Code, Name: s.Name, SettlementID: s.CityID,
				Tier: s.Tier, WorldCell: &cell, GridLots: w.Villages.gridLots(s.Tier, s.GridGrowth),
				LayoutPath: "/api/v1/settlements/" + s.CityID + "/layout",
				Home:       mine != nil && mine.ID == s.CityID && mine.Resident, Motto: s.Motto}
			if _, wg, err := w.Villages.World.active(ctx); err == nil {
				out.Centre = centreOf(wg, s.WorldCellID)
			}
			if e := s.Emblem; e.Shape != "" {
				out.Emblem = &EmblemView{Shape: e.Shape, ColorA: e.ColorA, ColorB: e.ColorB, Icon: e.Icon}
			}
			if s.Currency.Code != "" {
				out.Currency = &CurrencyView{Code: s.Currency.Code, Name: s.Currency.Name, Symbol: s.Currency.Symbol}
			}
			return out, nil
		case !errors.Is(err, application.ErrCityNotFound):
			return nil, err
		}
	}
	city, err := w.Cities.ByID(ctx, cityID)
	if apperrors.CodeOf(err) == apperrors.CodeNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &BootstrapLocation{Kind: LocationCity, Code: city.Code, Name: c.CityName(city.Code, city.Name)}
	if spot, ok := w.CitySpots[cityCode]; ok {
		out.Centre = &Place{Lat: spot.Lat, Lon: spot.Lon}
	}
	return out, nil
}

// Bootstrap builds the bootstrap for the principal.
func (w *World) Bootstrap(ctx context.Context, pr Principal) (Bootstrap, error) {
	p, err := w.Players.GetByID(ctx, pr.PlayerID)
	if err != nil {
		return Bootstrap{}, err
	}
	cityCode, err := w.cityOf(ctx, p)
	if err != nil {
		return Bootstrap{}, err
	}
	c := screens.Context{Msgs: w.Msgs, Lang: pr.Lang}
	snap := w.Content.Current()
	out := Bootstrap{
		Player:         BootstrapPlayer{ID: p.ID, Code: p.PublicCode, Name: p.DisplayName, Lang: pr.Lang, CityCode: cityCode},
		ContentVersion: snap.Version(), Languages: []NamedCode{}, Cities: []NamedCode{}, Places: []NamedCode{},
		ServerTime: w.Now().UTC().Format(time.RFC3339), Realtime: w.Realtime,
	}
	if w.Villages != nil {
		if out.Settlement, err = w.Villages.Mine(ctx, p.ID); err != nil {
			return Bootstrap{}, err
		}
	}
	if p.CityID != nil && *p.CityID != "" {
		if out.Location, err = w.location(ctx, c, *p.CityID, cityCode, out.Settlement); err != nil {
			return Bootstrap{}, err
		}
	}
	for _, lang := range w.Msgs.Languages() {
		out.Languages = append(out.Languages, NamedCode{Code: lang, Name: screens.LanguageName(c, lang)})
	}
	for _, city := range snap.Cities() {
		name := c.CityName(city.Code, city.Name)
		out.Cities = append(out.Cities, NamedCode{Code: city.Code, Name: name})
		if city.Code == cityCode {
			out.Player.City = name
		}
	}
	if cityCode != "" {
		for _, pl := range snap.CityMap(cityCode).Places {
			authored := pl.Code
			if def, ok := snap.PlaceDef(pl.Code); ok && def.Name != "" {
				authored = def.Name
			}
			out.Places = append(out.Places, NamedCode{Code: pl.Code,
				Name: c.SpotName(screens.Named{Code: pl.Code, Name: authored})})
		}
	}
	return out, nil
}
