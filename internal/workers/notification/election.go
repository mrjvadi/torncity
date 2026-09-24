package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Elections: a candidate's own result, privately; and in a city's linked
// groups, the public lines of an election opening, a candidacy, the vote
// opening and the count. None of them says who voted for whom: the events
// carry no ballot.
// A country's election is announced in no group yet (city_id is empty).

// electionEvent is the payload shape the elections handler writes.
type electionEvent struct {
	PlayerID        string `json:"player_id"`
	PlayerName      string `json:"player_name"`
	No              int64  `json:"no"`
	Office          string `json:"office"`
	PlaceKind       string `json:"place_kind"`
	PlaceCode       string `json:"place_code"`
	PlaceName       string `json:"place_name"`
	CityID          string `json:"city_id"`
	Votes           int64  `json:"votes"`
	Cast            int64  `json:"cast"`
	VotesCast       int64  `json:"votes_cast"`
	Elected         bool   `json:"elected"`
	Deposit         int64  `json:"deposit"`
	DepositReturned bool   `json:"deposit_returned"`
	// CandidacySeconds and VotingSeconds are how long candidates may stand,
	// and how long the vote lasts.
	CandidacySeconds int64 `json:"candidacy_seconds"`
	VotingSeconds    int64 `json:"voting_seconds"`
	// CandidateCount is how many stood.
	CandidateCount int `json:"candidate_count"`
	Candidates     []struct {
		PlayerName string `json:"player_name"`
		PlayerCode string `json:"player_code"`
		Elected    bool   `json:"elected"`
	} `json:"candidates"`
}

func decodeElection(env *envelope.Envelope, name string) (electionEvent, error) {
	var ev electionEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("election." + name + " payload is unreadable").WithCause(err)
	}
	if ev.Office == "" {
		return ev, apperrors.InvalidInput("election." + name + " names no office")
	}
	return ev, nil
}

func (e electionEvent) place() screens.GovPlace {
	return screens.GovPlace{Kind: e.PlaceKind, Code: e.PlaceCode, Name: e.PlaceName}
}

// renderElectionResult tells a candidate how their election went.
func renderElectionResult(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeElection(env, "result")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("election.result names no candidate")
	}
	view := screens.ElectionResultView{No: ev.No, Office: ev.Office, Place: ev.place(), Elected: ev.Elected,
		Votes: ev.Votes, Cast: ev.Cast, Deposit: ev.Deposit, DepositReturned: ev.DepositReturned}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.ElectionResultNotice(c, view)
	}}, nil
}

// electionOpenedAnnouncement: an election opened in a city.
func electionOpenedAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeElection(env, "opened")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	candidacy := time.Duration(ev.CandidacySeconds) * time.Second
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.ElectionOpenedAnnouncement(c, ev.Office, ev.place(), ev.No, candidacy)
	}}, nil
}

// electionStoodAnnouncement: a player stood in a city's election.
func electionStoodAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeElection(env, "stood")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	return &Announcement{CityID: ev.CityID, PlayerID: ev.PlayerID, Name: ev.PlayerName,
		Line: func(c screens.Context, name string) string {
			return screens.ElectionStoodAnnouncement(c, name, ev.Office, ev.place())
		}}, nil
}

// electionCountedAnnouncement: a city's election was counted.
func electionCountedAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeElection(env, "counted")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	var elected []screens.GovPlayer
	for _, c := range ev.Candidates {
		if c.Elected {
			elected = append(elected, screens.GovPlayer{Name: c.PlayerName, Code: c.PlayerCode})
		}
	}
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.ElectionCountedAnnouncement(c, ev.Office, ev.place(), elected, ev.VotesCast, ev.CandidateCount)
	}}, nil
}

// electionVotingAnnouncement: a city's election opened its vote.
func electionVotingAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeElection(env, "voting")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	voting := time.Duration(ev.VotingSeconds) * time.Second
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.ElectionVotingAnnouncement(c, ev.Office, ev.place(), ev.CandidateCount, voting)
	}}, nil
}
