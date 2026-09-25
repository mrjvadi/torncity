package clientapi

import (
	"context"
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
	// Realtime says whether the realtime tokens can be had.
	Realtime bool `json:"realtime"`
}

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
	Players  Players
	Cities   Cities
	Content  *content.Registry
	Msgs     Catalogue
	Realtime bool
	Now      func() time.Time
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
