package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Village news: one short post in a village's Telegram group when something
// in the village finishes (a construction, a research, a teaching step), and
// when its head starts a construction or buys knowledge from Support.
//
// # Quiet by construction
//
// A village whose head queues several things finishes several things close
// together, and a group that read one line each would be a group that muted
// the bot. So an event is never posted where it is read: it is put on a
// per-village queue in Redis, and a tick that every notifier replica runs
// posts a village's whole queue as ONE message once its oldest item is
// announce.village_merge_window old, and no more often than one post per
// announce.village_min_gap. A lone event is a sentence; a burst is a short
// list (notices.VillageNews).
//
// # Many replicas, one post
//
// The queue is the only shared state. Putting an event on it is idempotent
// on the event's id (a redelivered event is queued once, even after it has
// been posted), and taking a village's batch off it is one atomic step that
// also takes the village's min-gap lock, so however many replicas tick, one
// of them gets the batch. A batch whose send fails is put back and the lock
// released, so the next tick retries; a crash between taking and sending
// loses that post, which is the cheaper failure for a news line than a
// duplicate.

// NewsItem is one thing to tell a village's group about, as an event yields it.
type NewsItem struct {
	SettlementID string
	// Kind is one of notices.News*.
	Kind string
	// Code and Name identify the building or knowledge item it is about
	// (Name is the authored name; the catalogue names it per language).
	Code, Name string
	// Percent is the literacy reached, for notices.NewsTaught.
	Percent int
	// Amount is what a donation gave, for notices.NewsDonated.
	Amount int64
}

// NewsBuilder turns one village event into news, or nil when it is not news.
type NewsBuilder func(ctx context.Context, deps Deps, env *envelope.Envelope) (*NewsItem, error)

// The queue's types live with the other ports (application/ports_news.go),
// so the Redis adapter does not depend on this worker.
type (
	QueuedNews = application.QueuedNews
	NewsBatch  = application.NewsBatch
	NewsQueue  = application.NewsQueue
)

// villageNewsBatchMax bounds the villages one tick posts for.
const villageNewsBatchMax = 50

// queueNews puts one village event on the queue.
func (w *Worker) queueNews(ctx context.Context, route Route, env *envelope.Envelope, now time.Time, log *slog.Logger) error {
	if w.cfg.News == nil || w.cfg.Groups == nil {
		return nil
	}
	item, err := route.News(ctx, w.cfg.Deps, env)
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeInternal {
			log.Error("cannot shape the village news", slog.String("error", err.Error()))
			return err
		}
		log.Warn("event is no village news", slog.String("error", err.Error()))
		return nil
	}
	if item == nil || item.SettlementID == "" {
		return nil
	}
	if err := w.cfg.News.Push(ctx, item.SettlementID, QueuedNews{
		EventID: env.Metadata.MessageID(), Kind: item.Kind, Code: item.Code, Name: item.Name, Percent: item.Percent, Amount: item.Amount, At: now,
	}); err != nil {
		log.Warn("cannot queue the village news", slog.String("error", err.Error()))
		return err
	}
	return nil
}

// FlushVillageNews posts every village batch that is due. Every replica runs
// it on a ticker (cmd/notifier); the queue makes sure a batch goes out once.
func (w *Worker) FlushVillageNews(ctx context.Context) {
	if w.cfg.News == nil || w.cfg.Groups == nil {
		return
	}
	now := w.cfg.Now()
	batches, err := w.cfg.News.ClaimDue(ctx, now, w.cfg.NewsMergeWindow, w.cfg.NewsMinGap, villageNewsBatchMax)
	if err != nil {
		w.cfg.Logger.Warn("cannot take the due village news", slog.String("error", err.Error()))
		return
	}
	for _, b := range batches {
		if err := w.postNews(ctx, now, b); err != nil {
			w.cfg.Logger.Warn("village news not posted; will retry",
				slog.String("settlement_id", b.SettlementID), slog.String("error", err.Error()))
			if rerr := w.cfg.News.Requeue(ctx, b); rerr != nil {
				w.cfg.Logger.Error("cannot put the village news back; it is lost",
					slog.String("settlement_id", b.SettlementID), slog.String("error", rerr.Error()))
			}
		}
	}
}

// postNews posts one batch in each of the village's groups.
func (w *Worker) postNews(ctx context.Context, now time.Time, b NewsBatch) error {
	groups, err := w.cfg.Groups.ForCity(ctx, b.SettlementID, "")
	if err != nil {
		return err
	}
	if len(groups) == 0 || len(b.Items) == 0 {
		return nil // a village nobody plays in a group has no one to tell
	}
	sort.SliceStable(b.Items, func(i, j int) bool { return b.Items[i].At.Before(b.Items[j].At) })
	for _, g := range groups {
		village := g.CityName
		if village == "" {
			if city, err := w.cfg.Deps.Cities.ByID(ctx, b.SettlementID); err == nil && city != nil {
				village = city.Name
			}
		}
		view := notices.VillageNewsView{Village: village}
		for _, q := range b.Items {
			view.Items = append(view.Items, notices.VillageNewsItem{
				Kind: q.Kind, Building: notices.Named{Code: q.Code, Name: q.Name},
				Knowledge: notices.Named{Code: q.Code, Name: q.Name}, Percent: q.Percent, Player: playerOf(q), Amount: q.Amount,
				Tier: tierOfNews(q),
			})
		}
		resp := notices.VillageNews(presentation.Ctx{Lang: g.Language}, view)
		if err := w.sendNews(ctx, now, g, resp); err != nil {
			return err
		}
	}
	return nil
}

// newsID is a request id for a post no event asked for.
func newsID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "vnews-" + hex.EncodeToString(b[:])
}

// sendNews hands a village post to the gateway for one group and waits for
// its receipt. A group the bot cannot post in any more is skipped, not
// retried: retrying will not change that.
func (w *Worker) sendNews(ctx context.Context, start time.Time, g application.CityGroup, resp *presenter.Response) error {
	ctx, cancel := context.WithDeadline(ctx, start.Add(w.cfg.SendBudget))
	defer cancel()
	id := newsID()
	meta := envelope.Metadata{
		RequestID: id, TraceID: id, EventID: id, Command: "notify.village_news", SchemaVersion: envelope.SchemaVersion,
		ReceivedAt: start, BotID: g.BotID, TelegramChatID: g.ChatID, ChatType: "supergroup", Language: g.Language,
	}
	env, err := envelope.New(meta, Notice{
		DeliverBy:    start.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin),
		Response:     *resp,
		Announcement: true,
		Keyboard:     true,
	})
	if err != nil {
		return err
	}
	receipt, err := w.cfg.Sender.Send(ctx, subjects.Notify("group"+strconv.FormatInt(-g.ChatID, 10)), env)
	if err != nil {
		return err
	}
	switch receipt.Outcome {
	case OutcomeDelivered:
		return nil
	case OutcomeUnreachable, OutcomeUnknownBot:
		w.cfg.Logger.Warn("the group cannot be posted in; the village news is dropped there",
			slog.Int64("chat_id", g.ChatID), slog.String("outcome", string(receipt.Outcome)))
		return nil
	}
	return fmt.Errorf("%w: group %d: %s: %s", errAnnouncementFailed, g.ChatID, receipt.Outcome, receipt.Detail)
}

// The news builders.

func newsFrom(kind string) NewsBuilder {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*NewsItem, error) {
		ev, err := decodeVillage(env, kind)
		if err != nil {
			return nil, err
		}
		item := &NewsItem{SettlementID: ev.SettlementID, Kind: kind, Name: ev.Name}
		switch kind {
		case notices.NewsBuilt, notices.NewsBuildStarted:
			item.Code = ev.TypeCode
		case notices.NewsResearched, notices.NewsBought:
			item.Code = ev.Code
		}
		return item, nil
	}
}

// newsTaught: literacy grew. A teaching step happens every few minutes for as
// long as a school stands, so only crossing a milestone (every
// announce.village_literacy_step_percent points) is news; the settlement
// channel still carries every step.
func newsTaught(_ context.Context, deps Deps, env *envelope.Envelope) (*NewsItem, error) {
	ev, err := decodeVillage(env, "literacy_advanced")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Share    int `json:"literacy_share_bps"`
		Previous int `json:"previous_bps"`
	}
	_ = json.Unmarshal(env.Payload, &raw)
	step := deps.LiteracyStepBPS
	if step <= 0 {
		step = 1000
	}
	if raw.Share/step == raw.Previous/step {
		return nil, nil
	}
	return &NewsItem{SettlementID: ev.SettlementID, Kind: notices.NewsTaught, Percent: raw.Share / 100}, nil
}

// playerOf is who a resident_joined item is about (its Name).
func playerOf(q QueuedNews) string {
	if q.Kind == notices.NewsResidentJoined || q.Kind == notices.NewsDonated || q.Kind == notices.NewsPromoted {
		return q.Name
	}
	return ""
}

// tierOfNews is the tier a promotion reached (its Code).
func tierOfNews(q QueuedNews) string {
	if q.Kind == notices.NewsPromoted {
		return q.Code
	}
	return ""
}

// newsPromoted: the settlement grew into the next tier. The item carries the
// tier reached (Code) and the head's name.
func newsPromoted(ctx context.Context, deps Deps, env *envelope.Envelope) (*NewsItem, error) {
	var ev struct {
		SettlementID string `json:"settlement_id"`
		To           string `json:"to"`
		HeadPlayerID string `json:"head_player_id"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("settlement.promoted payload is unreadable").WithCause(err)
	}
	if ev.SettlementID == "" || ev.To == "" {
		return nil, nil
	}
	name := ""
	if deps.Players != nil && ev.HeadPlayerID != "" {
		p, err := deps.Players.GetByID(ctx, ev.HeadPlayerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			return nil, err
		}
	}
	return &NewsItem{SettlementID: ev.SettlementID, Kind: notices.NewsPromoted, Code: ev.To, Name: name}, nil
}

// newsResidentJoined: a player made a village their home. The founder's own
// move at founding is not news (the founding announcement says it), and a
// move into a city that no group founded is not a village's news.
func newsResidentJoined(ctx context.Context, deps Deps, env *envelope.Envelope) (*NewsItem, error) {
	var ev struct {
		PlayerID string `json:"player_id"`
		ToCityID string `json:"to_city_id"`
		Via      string `json:"via"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("residence.changed payload is unreadable").WithCause(err)
	}
	if ev.ToCityID == "" || ev.PlayerID == "" || ev.Via != "join" {
		return nil, nil
	}
	if deps.Founded != nil {
		keep, err := deps.Founded.FoundedAmong(ctx, []string{ev.ToCityID})
		if err != nil {
			return nil, err
		}
		if len(keep) == 0 {
			return nil, nil
		}
	}
	name := ""
	if deps.Players != nil {
		p, err := deps.Players.GetByID(ctx, ev.PlayerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			return nil, err
		}
	}
	return &NewsItem{SettlementID: ev.ToCityID, Kind: notices.NewsResidentJoined, Name: name}, nil
}

// newsDonated: a resident gave to the village treasury. The gift is public
// news, with the giver's name and the amount, so the village can thank them.
func newsDonated(ctx context.Context, deps Deps, env *envelope.Envelope) (*NewsItem, error) {
	var ev struct {
		SettlementID string `json:"settlement_id"`
		PlayerID     string `json:"player_id"`
		Amount       int64  `json:"amount"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("settlement.donated payload is unreadable").WithCause(err)
	}
	if ev.SettlementID == "" || ev.Amount <= 0 {
		return nil, nil
	}
	name := ""
	if deps.Players != nil && ev.PlayerID != "" {
		p, err := deps.Players.GetByID(ctx, ev.PlayerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			return nil, err
		}
	}
	return &NewsItem{SettlementID: ev.SettlementID, Kind: notices.NewsDonated, Name: name, Amount: ev.Amount}, nil
}
