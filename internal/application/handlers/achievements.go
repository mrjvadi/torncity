package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/achievement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// AchievementsHandler serves achievements (docs/adr/0024-property-and-
// politics.md): it moves a player's progress on the game's own events —
// consumed beside the commands, like missions — awards an achievement once
// its count is reached, exactly once per player, with its cash inside the
// day's caps, and shows the player what they have earned.
type AchievementsHandler struct {
	uow     application.UnitOfWork
	msgs    Translator
	content ContentSource
	rules   AchievementRules
	now     func() time.Time
}

// AchievementRules are the day's caps on achievement cash (config
// achievements.*).
type AchievementRules struct {
	PlayerDailyCap, EconomyDailyCap int64
}

// NewAchievementsHandler builds the handler.
func NewAchievementsHandler(uow application.UnitOfWork, msgs Translator, source ContentSource, rules AchievementRules,
	now func() time.Time,
) *AchievementsHandler {
	if source == nil || rules.PlayerDailyCap < 0 || rules.EconomyDailyCap < 0 {
		panic("handlers: NewAchievementsHandler requires content and caps")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &AchievementsHandler{uow: uow, msgs: msgs, content: source, rules: rules, now: now}
}

// AchievementEventSubjects are the events achievements count, and the kind
// each is: the consumer cmd/game subscribes to each.
var AchievementEventSubjects = map[string]string{
	subjects.Event("job", "shift_worked"):    achievement.ShiftWorked,
	subjects.Event("company", "founded"):     achievement.CompanyFounded,
	subjects.Event("election", "result"):     achievement.ElectionWon,
	subjects.Event("crime", "attempted"):     achievement.CrimeSucceeded,
	subjects.Event("property", "bought"):     achievement.PropertyBought,
	subjects.Event("travel", "completed"):    achievement.JourneyMade,
	subjects.Event("education", "completed"): achievement.CourseCompleted,
	subjects.Event("legislature", "decided"): achievement.LawPassed,
}

// achievementEvent is what an achievement reads off an event.
type achievementEvent struct {
	PlayerID string `json:"player_id"`
	Elected  bool   `json:"elected"`
	Result   string `json:"result"`
	Status   string `json:"status"`
}

// counts reports whether the event is one of its kind that counts.
func (e achievementEvent) counts(kind string) bool {
	switch kind {
	case achievement.ElectionWon:
		return e.Elected
	case achievement.CrimeSucceeded:
		return e.Result == "succeeded"
	case achievement.LawPassed:
		return e.Status == application.ProposalPassed
	}
	return true
}

// OnEvent moves the achievements of the player an event is about. It runs
// once per event and player: the event's id goes into the achievements'
// inbox in the same transaction as the progress and any award. An error
// means "not yet": the event is redelivered.
func (h *AchievementsHandler) OnEvent(ctx context.Context, env *envelope.Envelope, subject string) error {
	kind, ok := AchievementEventSubjects[subject]
	if !ok {
		return nil
	}
	var ev achievementEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil || ev.PlayerID == "" || !ev.counts(kind) {
		return nil // unreadable, or nothing to count: it never will be
	}
	snap := h.content.Current()
	var mine []application.PlayerAchievement
	defs := snap.Achievements()
	for _, d := range defs {
		if d.Event == kind {
			mine = append(mine, application.PlayerAchievement{Code: d.Code})
		}
	}
	if len(mine) == 0 {
		return nil
	}
	eventID := env.Metadata.MessageID() + ":" + subject
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		repo := tx.Achievements()
		if err := repo.Lock(ctx, ev.PlayerID); err != nil {
			return err
		}
		now := h.now()
		fresh, err := repo.MarkEvent(ctx, eventID, ev.PlayerID, subject, now)
		if err != nil || !fresh {
			return err
		}
		progress, err := repo.Progress(ctx, ev.PlayerID)
		if err != nil {
			return err
		}
		earned, err := repo.Earned(ctx, ev.PlayerID)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, e := range earned {
			have[e.Code] = true
		}
		for _, d := range defs {
			if d.Event != kind || have[d.Code] {
				continue
			}
			count := progress[d.Code] + 1
			if err := repo.SaveProgress(ctx, ev.PlayerID, d.Code, count, now); err != nil {
				return err
			}
			if !d.Achievement().Reached(count) {
				continue
			}
			if err := h.award(ctx, tx, env.Metadata, ev.PlayerID, d.Code, d.Name, d.Reward, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// award records an achievement earned, once, and pays its cash inside the
// day's caps.
func (h *AchievementsHandler) award(ctx context.Context, tx application.Tx, meta envelope.Metadata, playerID, code,
	name string, reward int64, now time.Time,
) error {
	repo := tx.Achievements()
	a := application.PlayerAchievement{PlayerID: playerID, Code: code, AwardedAt: now}
	if reward > 0 {
		if err := repo.LockDay(ctx); err != nil {
			return err
		}
		day := dayStart(now)
		mine, err := repo.PaidSince(ctx, playerID, day)
		if err != nil {
			return err
		}
		all, err := repo.PaidSince(ctx, "", day)
		if err != nil {
			return err
		}
		a.Cash, a.Withheld = achievement.CapCash(reward, h.rules.PlayerDailyCap-mine, h.rules.EconomyDailyCap-all)
	}
	fresh, err := repo.Award(ctx, a)
	if err != nil || !fresh {
		return err
	}
	if a.Cash > 0 {
		grant, _, err := tx.Ledger().RecordGrant(ctx, application.RewardGrant{PlayerID: playerID,
			Source: application.RewardAchievement, Amount: money.FromMinor(a.Cash),
			GrantedBy: "achievement:" + code, CreatedAt: now})
		if err != nil {
			return err
		}
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: grant.LedgerTransactionID,
			Reason: application.ReasonAchievementReward, ReferenceType: "reward_grants", ReferenceID: grant.ID,
			Entries: []application.LedgerEntry{{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-a.Cash)},
				{AccountID: cash.ID, Amount: money.FromMinor(a.Cash)}}, CreatedAt: now}); err != nil {
			return err
		}
	}
	return appendDomainEvent(ctx, tx, meta, "achievement", "awarded", playerID, map[string]any{
		"player_id": playerID, "code": code, "name": name, "cash": a.Cash, "withheld": a.Withheld})
}

// List handles achievement.list: what the player has earned and how far
// they are toward the rest.
func (h *AchievementsHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.AchievementsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		earned, err := tx.Achievements().Earned(ctx, p.ID)
		if err != nil {
			return err
		}
		progress, err := tx.Achievements().Progress(ctx, p.ID)
		if err != nil {
			return err
		}
		have := map[string]application.PlayerAchievement{}
		for _, e := range earned {
			have[e.Code] = e
		}
		for _, d := range snap.Achievements() {
			line := screens.AchievementLine{Achievement: named(d.Code, d.Name), Count: d.Count, Reward: d.Reward,
				Done: min(progress[d.Code], d.Count)}
			if e, ok := have[d.Code]; ok {
				line.Earned, line.Done, line.Cash = true, d.Count, e.Cash
			}
			view.Lines = append(view.Lines, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Achievements(screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta),
		Shared: meta.InGroup()}, view), nil
}

// EarnedCount is how many achievements a player has, for the profile.
func EarnedCount(ctx context.Context, tx application.Tx, playerID string) (int, error) {
	repo := tx.Achievements()
	if repo == nil {
		return 0, nil
	}
	earned, err := repo.Earned(ctx, playerID)
	return len(earned), err
}
