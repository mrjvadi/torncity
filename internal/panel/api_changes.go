package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/operator"
)

// The changes. Each checks its input before anything is tried, then calls
// the Backend with the operator as actor. The wrapper (mutation) has
// already required the reason, the key and the CSRF token.

var (
	languageCode = regexp.MustCompile(`^[a-z]{2,3}$`)
	codeLike     = regexp.MustCompile(`^[A-Za-z0-9_.#-]{1,64}$`)
)

// maxMessage is what one Telegram message may carry, as the command line.
const maxMessage = 3000

func pathCode(r *http.Request) (string, error) {
	c := strings.TrimSpace(r.PathValue("code"))
	if !codeLike.MatchString(c) {
		return "", bad("that code is not valid")
	}
	return c, nil
}

func (s *Server) linkGroup(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	city, err := pathCode(r)
	if err != nil {
		return 0, nil, err
	}
	var g GroupChange
	if err := strict(body, &g); err != nil {
		return 0, nil, err
	}
	g.City, g.Bot = city, strings.TrimSpace(g.Bot)
	if g.Language == "" {
		g.Language = "fa"
	}
	switch {
	case g.ChatID >= 0:
		return 0, nil, bad("a group's chat id is negative")
	case g.Bot == "":
		return 0, nil, bad("name the bot that serves the group")
	case !languageCode.MatchString(g.Language):
		return 0, nil, bad("the language is not a language code")
	}
	if err := s.backend.LinkGroup(ctx, g, a); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"linked": g.ChatID, "city": city}, nil
}

func (s *Server) unlinkGroup(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	city, err := pathCode(r)
	if err != nil {
		return 0, nil, err
	}
	var g struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := strict(body, &g); err != nil {
		return 0, nil, err
	}
	if g.ChatID >= 0 {
		return 0, nil, bad("a group's chat id is negative")
	}
	if err := s.backend.UnlinkGroup(ctx, GroupChange{City: city, ChatID: g.ChatID}, a); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"unlinked": g.ChatID, "city": city}, nil
}

func (s *Server) grantDefence(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	return s.defence(ctx, r, body, a, true)
}

func (s *Server) revokeDefence(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	return s.defence(ctx, r, body, a, false)
}

func (s *Server) defence(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor, grant bool) (int, any, error) {
	company, err := pathCode(r)
	if err != nil {
		return 0, nil, err
	}
	if err := strict(body, &struct{}{}); err != nil {
		return 0, nil, err
	}
	var no int64
	if grant {
		no, err = s.backend.GrantDefence(ctx, company, a)
	} else {
		no, err = s.backend.RevokeDefence(ctx, company, a)
	}
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"licence_no": no, "company": company, "granted": grant}, nil
}

func (s *Server) seatChange(appoint bool) change {
	return func(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
		var c SeatChange
		if err := strict(body, &c); err != nil {
			return 0, nil, err
		}
		c.Office, c.Code, c.Player = strings.TrimSpace(c.Office), strings.TrimSpace(c.Code), strings.TrimSpace(c.Player)
		if c.Seat == 0 {
			c.Seat = 1
		}
		switch {
		case !codeLike.MatchString(c.Office):
			return 0, nil, bad("name the office")
		case c.Kind != "city" && c.Kind != "country", !codeLike.MatchString(c.Code):
			return 0, nil, bad("name the place: a city or a country, and its code")
		case c.Seat < 1 || c.Seat > 1000:
			return 0, nil, bad("the seat is a number from 1")
		case appoint && !codeLike.MatchString(c.Player):
			return 0, nil, bad("name the player by their code")
		}
		seat, err := s.backend.ChangeSeat(ctx, c, appoint, a)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, seat, nil
	}
}

func (s *Server) openElection(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	var e struct {
		Office string `json:"office"`
		Kind   string `json:"kind"`
		Code   string `json:"code"`
	}
	if err := strict(body, &e); err != nil {
		return 0, nil, err
	}
	if !codeLike.MatchString(e.Office) || (e.Kind != "city" && e.Kind != "country") || !codeLike.MatchString(e.Code) {
		return 0, nil, bad("name the office and the place")
	}
	opened, err := s.backend.OpenElection(ctx, e.Office, e.Kind, e.Code, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, opened, nil
}

func (s *Server) grant(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	var g struct {
		Player string `json:"player"`
		Amount int64  `json:"amount"`
	}
	if err := strict(body, &g); err != nil {
		return 0, nil, err
	}
	switch {
	case !codeLike.MatchString(strings.TrimSpace(g.Player)):
		return 0, nil, bad("name the player by their code")
	case g.Amount <= 0:
		return 0, nil, bad("the amount must be above zero")
	}
	out, err := s.backend.Grant(ctx, strings.TrimSpace(g.Player), g.Amount, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"grant": out.ID, "player": out.Label, "amount": out.Amount, "granted_by": out.GrantedBy}, nil
}

func (s *Server) loadContent(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	var c struct {
		// Confirm must repeat the checksum the operator was shown, so a
		// load acts on the files that were reviewed and nothing else.
		Confirm string `json:"confirm_checksum"`
	}
	if err := strict(body, &c); err != nil {
		return 0, nil, err
	}
	status, err := s.backend.Content(ctx)
	if err != nil && statusOf(err, 0) != http.StatusNotFound {
		return 0, nil, err
	}
	switch {
	case status.LocalError != "":
		return 0, nil, bad("the content files do not validate: %s", status.LocalError)
	case c.Confirm == "" || c.Confirm != status.LocalChecksum:
		return 0, nil, bad("the files changed since they were shown; review them again")
	}
	loaded, err := s.backend.LoadContent(ctx, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, loaded, nil
}

func (s *Server) clearFlag(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	no, err := number(r, "no")
	if err != nil {
		return 0, nil, err
	}
	if err := strict(body, &struct{}{}); err != nil {
		return 0, nil, err
	}
	if err := s.backend.ClearFlag(ctx, no, a); err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"cleared": no}, nil
}

func (s *Server) settle(release bool) change {
	return func(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
		no, err := number(r, "no")
		if err != nil {
			return 0, nil, err
		}
		if err := strict(body, &struct{}{}); err != nil {
			return 0, nil, err
		}
		out, err := s.backend.SettleHold(ctx, no, release, a)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, out, nil
	}
}

func message(body json.RawMessage, allowOnly bool) (Message, error) {
	var m Message
	if err := strict(body, &m); err != nil {
		return m, err
	}
	m.Text, m.TextEN, m.Only = strings.TrimSpace(m.Text), strings.TrimSpace(m.TextEN), strings.TrimSpace(m.Only)
	switch {
	case m.Text == "":
		return m, bad("the text is required")
	case utf8.RuneCountInString(m.Text) > maxMessage || utf8.RuneCountInString(m.TextEN) > maxMessage:
		return m, bad("the text is longer than a Telegram message allows")
	case !allowOnly && m.Only != "":
		return m, bad("an announcement goes to every group")
	case m.Only != "" && !codeLike.MatchString(m.Only):
		return m, bad("name the preview's player by their code")
	}
	return m, nil
}

func (s *Server) announce(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	m, err := message(body, false)
	if err != nil {
		return 0, nil, err
	}
	out, err := s.backend.Announce(ctx, m, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}

func (s *Server) broadcast(ctx context.Context, _ *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	m, err := message(body, true)
	if err != nil {
		return 0, nil, err
	}
	n, err := s.backend.Broadcast(ctx, m, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]any{"players": n, "only": m.Only}, nil
}

// setSwitch flips one operator switch (migrations/0041_runtime_switches).
// Confirm must repeat the switch's own key, the way loadContent's Confirm
// repeats a checksum: the operator types the name of the thing they are
// about to change, so a hasty click on the wrong row cannot flip it.
func (s *Server) setSwitch(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		return 0, nil, bad("name the switch")
	}
	var c struct {
		Value   string `json:"value"`
		Confirm string `json:"confirm"`
	}
	if err := strict(body, &c); err != nil {
		return 0, nil, err
	}
	if c.Confirm != key {
		return 0, nil, bad("type %q to confirm this change", key)
	}
	if strings.TrimSpace(c.Value) == "" {
		return 0, nil, bad("a value is required")
	}
	st, err := s.backend.SetSwitch(ctx, key, strings.TrimSpace(c.Value), a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, st, nil
}
