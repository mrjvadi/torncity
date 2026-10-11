package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// Raids in the groups (docs/adr/0074): the defender's group reads the warning when the watch saw the raid coming (and only then), and
// both groups read when the window opens and how it ended. The lines are summaries, a few per raid.

// raidEvent is the payload of settlement.raid_* events.
type raidEvent struct {
	RaidID     string `json:"raid_id"`
	AttackerID string `json:"attacker_id"`
	DefenderID string `json:"defender_id"`
	By         string `json:"by"`
	LeadSecs   int64  `json:"lead_secs"`
	Band       string `json:"band"`
	Taken      bool   `json:"taken"`
	Cash       int64  `json:"cash"`
	Falls      int    `json:"falls"`
	Amount     int64  `json:"amount"`
}

func decodeRaid(env *envelope.Envelope, name string) (raidEvent, error) {
	var ev raidEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("settlement." + name + " payload is unreadable").WithCause(err)
	}
	if ev.DefenderID == "" || ev.AttackerID == "" {
		return ev, apperrors.InvalidInput("settlement." + name + " names no settlement")
	}
	return ev, nil
}

// raidLine announces a raid event in the groups of the settlements it names: toDefender only for the warning.
func raidLine(name string, toDefender bool, view func(ev raidEvent, other string) village.RaidNoticeView) Announcer {
	return func(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
		ev, err := decodeRaid(env, name)
		if err != nil {
			return nil, err
		}
		att, err := deps.Cities.ByID(ctx, ev.AttackerID)
		if err != nil {
			return nil, err
		}
		cities := []string{ev.DefenderID}
		if !toDefender {
			cities = append(cities, ev.AttackerID)
		}
		v := view(ev, att.Name)
		return &Announcement{CityIDs: cities, Notice: func(c presentation.Ctx, _ string) *presentation.Response { return village.RaidNotice(c, v) }}, nil
	}
}

var (
	raidDetectedAnnouncement = raidLine("raid_detected", true, func(ev raidEvent, other string) village.RaidNoticeView {
		return village.RaidNoticeView{Kind: village.RaidNoticeDetected, Other: other, Band: ev.Band, LeadSecs: ev.LeadSecs}
	})
	raidOpenedAnnouncement = raidLine("raid_opened", false, func(ev raidEvent, other string) village.RaidNoticeView {
		return village.RaidNoticeView{Kind: village.RaidNoticeOpened, Other: other}
	})
	raidResolvedAnnouncement = raidLine("raid_resolved", false, func(ev raidEvent, other string) village.RaidNoticeView {
		return village.RaidNoticeView{Kind: village.RaidNoticeResolved, Other: other, Taken: ev.Taken, Cash: ev.Cash, Falls: int64(ev.Falls)}
	})
	raidSettledAnnouncement = raidLine("raid_settled", false, func(ev raidEvent, other string) village.RaidNoticeView {
		return village.RaidNoticeView{Kind: village.RaidNoticeSettledK, Other: other, Amount: ev.Amount}
	})
)
