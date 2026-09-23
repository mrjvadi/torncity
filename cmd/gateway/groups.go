package main

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/commands"
	gwcontext "github.com/mrjvadi/torncity/internal/gateway/context"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// This file is the gateway's side of playing in groups: what is admitted from
// a group, and how a response is rendered into one. The rules themselves —
// what is private, how it is delivered, how buttons are bound to their owner —
// are internal/gateway/groups; here they are wired into the update pipeline
// and the one render path every response and notice goes through.

// groupState is the gateway's group machinery. Its zero value is usable: the
// renderer is built on first use from the configuration and the catalogue.
type groupState struct {
	once     sync.Once
	renderer *groups.Renderer

	// claims picks the one bot that answers an unaddressed command when
	// several of ours share a group. Nil answers every command, which is what
	// a gateway with no Redis (a test) should do.
	claims *groups.Claimer

	// usernames caches each bot's @username by bot key, for bots whose
	// registry row has none.
	usernames sync.Map
}

// groupRenderer returns the renderer, building it on first use.
func (g *gateway) groupRenderer() *groups.Renderer {
	g.group.once.Do(func() {
		var msgs groups.Translator
		if g.messages != nil {
			msgs = g.messages
		}
		g.group.renderer = groups.NewRenderer(msgs, groups.Settings{
			EphemeralReplyWindow:  g.cfg.Groups.EphemeralReplyWindow,
			EphemeralRefusalTTL:   g.cfg.Groups.EphemeralRefusalTTL,
			CallbackAlertMaxRunes: g.cfg.Groups.CallbackAlertMaxRunes,
		}, nil)
	})
	return g.group.renderer
}

// botUsername is the bot's @username, from the registry or, when the registry
// row has none, from getMe once. Empty when neither answers.
func (g *gateway) botUsername(ctx context.Context, botKey string, api *client.Client) string {
	if g.fleet != nil {
		if bot, ok := g.fleet.Get(botKey); ok && bot.Username != "" {
			return bot.Username
		}
	}
	if v, ok := g.group.usernames.Load(botKey); ok {
		return v.(string)
	}
	if api == nil {
		return ""
	}
	me, err := api.GetMe(ctx)
	if err != nil || me.Username == "" {
		return ""
	}
	g.group.usernames.Store(botKey, me.Username)
	return me.Username
}

// render is the one path every response and notice takes to Telegram.
//
// A private chat renders exactly as it always has. A group goes through
// internal/gateway/groups, which keeps private screens off the timeline. A
// notice addressed to a group — a bot link recorded before group play was
// understood — is delivered to the player's private chat instead, never to the
// room.
func (g *gateway) render(ctx context.Context, api *client.Client, botKey string, meta envelope.Metadata, resp *presenter.Response, priority lane, log *slog.Logger) error {
	if resp.Type == presenter.ActionAnswerCallback {
		return g.groupRenderer().Answer(ctx, api, meta, resp)
	}

	if priority == laneNotice && groups.IsGroupChat(meta.ChatType, meta.TelegramChatID) {
		if meta.TelegramUserID <= 0 {
			return groups.ErrNoReceiver
		}
		meta.TelegramChatID = meta.TelegramUserID
		meta.ChatType = chatTypePrivate
		return render(ctx, api, meta, resp)
	}

	if !groups.IsGroupChat(meta.ChatType, meta.TelegramChatID) {
		return render(ctx, api, meta, resp)
	}

	bot := groups.Bot{Key: botKey, Username: g.botUsername(ctx, botKey, api)}
	out, err := g.groupRenderer().Render(ctx, api, bot, meta, resp)
	if !out.CallbackAnswered {
		acknowledgeCallback(ctx, api, meta, resp)
	}
	if log != nil && (out.Route != "" || len(out.Notes) > 0) {
		log.Info("group response rendered",
			slog.String("route", out.Route), slog.String("notes", strings.Join(out.Notes, "; ")))
	}
	return err
}

// chatTypePrivate is Telegram's chat type for a one-to-one chat with the bot.
const chatTypePrivate = "private"

// admission is what the group gate decided about one update.
type admission struct {
	// proceed is false when the update must be dropped silently.
	proceed bool
	// mayHelp is false when a command the game does not serve must stay
	// unanswered: in a group, an unaddressed "/weather" is most likely
	// somebody else's bot's command.
	mayHelp bool
}

// admit applies the group rules to an update before it is routed. It may
// rewrite the update: a button's owner tag is taken off its data, and a deep
// link's /start payload becomes the command it replays.
//
//   - A button bound to another player is refused with a popup and goes no
//     further.
//   - In a group, a command addressed to another bot is ignored; one
//     addressed to this bot, or sent as an ephemeral command, is answered; an
//     unaddressed one is answered by exactly one of our bots (the first to
//     claim it) and, if the game does not serve it, by none.
//   - Messages from bots are ignored in groups.
func (g *gateway) admit(ctx context.Context, bot application.Bot, update *client.Update, meta envelope.Metadata, log *slog.Logger) admission {
	switch {
	case update.CallbackQuery != nil:
		cq := update.CallbackQuery
		owner, data, bound := groups.SplitOwner(cq.Data)
		if !bound {
			return admission{proceed: true, mayHelp: true}
		}
		if owner != cq.From.ID {
			g.refuseForeignPress(ctx, bot, cq.ID, meta, log)
			return admission{}
		}
		stripped := *cq
		stripped.Data = data
		update.CallbackQuery = &stripped
		return admission{proceed: true, mayHelp: true}

	case update.Message != nil:
		msg := update.Message
		if !groups.IsGroupChat(msg.Chat.Type, msg.Chat.ID) {
			if command, ok := groups.CommandFromStart(msg.Text); ok && commands.FromPlayerCommand(command) {
				// A deep link from a group: replay the command it names, as
				// if the player had typed it here.
				domain, action, _ := strings.Cut(command, ".")
				replay := *msg
				replay.Text = "/" + domain + " " + action
				update.Message = &replay
			}
			return admission{proceed: true, mayHelp: true}
		}

		if msg.From != nil && msg.From.IsBot {
			return admission{}
		}
		addressee, isCommand := groups.CommandAddressee(msg.Text)
		if !isCommand {
			// Plain chat. Routing reports it as not a command and, in a
			// group, nobody answers it; nothing is claimed for it either.
			return admission{proceed: true}
		}
		if addressee != "" {
			username := g.botUsername(ctx, bot.BotKey, g.clientFor(bot.BotKey))
			if username != "" && !groups.SameBot(addressee, username) {
				return admission{}
			}
			if username != "" {
				return admission{proceed: true, mayHelp: true}
			}
		}
		if msg.EphemeralMessageID != 0 {
			// Only the bot it names receives an ephemeral command.
			return admission{proceed: true, mayHelp: true}
		}
		won, err := g.group.claims.Claim(ctx, msg)
		if err != nil {
			log.Warn("cannot claim a group command; answering it anyway",
				append(metaAttrs(meta), slog.String("error", err.Error()))...)
		}
		if !won {
			log.Debug("another bot of ours answers this group command", metaAttrs(meta)...)
			return admission{}
		}
		return admission{proceed: true, mayHelp: false}
	}
	return admission{proceed: true, mayHelp: true}
}

// clientFor is the bot's API client, or nil when the fleet has none.
func (g *gateway) clientFor(botKey string) *client.Client {
	if g.fleet == nil {
		return nil
	}
	api, err := g.fleet.ClientFor(botKey)
	if err != nil {
		return nil
	}
	return api
}

// refuseForeignPress answers a press on somebody else's button with a popup
// only the presser sees. Nothing is published.
func (g *gateway) refuseForeignPress(ctx context.Context, bot application.Bot, callbackQueryID string, meta envelope.Metadata, log *slog.Logger) {
	api := g.clientFor(bot.BotKey)
	if api == nil {
		return
	}
	if err := g.limiter.Wait(ctx, bot.BotKey); err != nil {
		return
	}
	if err := g.groupRenderer().RefuseForeign(ctx, api, callbackQueryID, meta.Language); err != nil {
		log.Warn("cannot refuse a press on another player's button",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}
	log.Info("refused a press on another player's button", metaAttrs(meta)...)
}

// withReplyTarget names the player whose message the command replied to, when
// that person plays. The lookup never creates a player: replying to somebody
// is no reason to enrol them.
func (g *gateway) withReplyTarget(ctx context.Context, meta envelope.Metadata, log *slog.Logger) envelope.Metadata {
	if meta.ReplyToTelegramUserID == 0 || g.players == nil {
		return meta
	}
	p, err := g.players.GetByTelegramUserID(ctx, meta.ReplyToTelegramUserID)
	switch {
	case err == nil && p != nil:
		meta.ReplyToPlayerID = p.ID
	case err != nil && !errors.Is(err, application.ErrPlayerNotFound):
		log.Warn("cannot resolve the player a command replied to",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
	}
	return meta
}

// onMembership handles the bot being added to or removed from a group. On
// joining it says, once and publicly, how play in a group works; on leaving
// there is nobody left to tell.
func (g *gateway) onMembership(ctx context.Context, bot application.Bot, change *client.ChatMemberUpdated, log *slog.Logger) {
	if !groups.IsGroupChat(change.Chat.Type, change.Chat.ID) {
		return
	}
	attrs := []any{slog.Int64("chat_id", change.Chat.ID), slog.String("status", change.NewChatMember.Status)}
	switch {
	case groups.Left(change.OldChatMember.Status, change.NewChatMember.Status):
		log.Info("bot removed from a group", attrs...)
		return
	case !groups.Joined(change.OldChatMember.Status, change.NewChatMember.Status):
		log.Debug("bot's standing in a group changed", attrs...)
		return
	}
	log.Info("bot added to a group", attrs...)

	api := g.clientFor(bot.BotKey)
	if api == nil || g.messages == nil {
		return
	}
	lang := g.cfg.Player.DefaultLanguage
	if change.From.LanguageCode != "" {
		lang = gwcontext.NormalizeLanguage(change.From.LanguageCode, lang)
	}
	meta := envelope.Metadata{
		BotID:          bot.ID,
		TelegramChatID: change.Chat.ID,
		ChatType:       change.Chat.Type,
		Language:       lang,
	}
	resp := presenter.Message(g.messages.T(lang, groups.KeyWelcome, nil), nil).MarkPublic()

	ctx, cancel := context.WithTimeout(ctx, g.cfg.Gateway.ShutdownTimeout)
	defer cancel()
	if err := g.send(ctx, api, bot.BotKey, meta, resp, laneDirect, log); err != nil {
		log.Warn("cannot greet the group", append(attrs, slog.String("error", err.Error()))...)
	}
}

// menuCommandPattern is what the Bot API accepts as a command name.
var menuCommandPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// registerGroupMenu sets the bot's command menu for groups, once per language
// in the catalogue and once for every other language, with every command
// declared ephemeral: a player's use of it is seen by nobody but the bot, and
// the bot may answer it with a screen only that player sees. Best effort: a
// bot without the menu still answers typed commands.
func (g *gateway) registerGroupMenu(ctx context.Context, bot application.Bot, api *client.Client, log *slog.Logger) {
	if g.messages == nil || api == nil || len(g.cfg.Groups.Menu) == 0 {
		return
	}
	build := func(lang string) []client.BotCommand {
		out := make([]client.BotCommand, 0, len(g.cfg.Groups.Menu))
		for _, name := range g.cfg.Groups.Menu {
			if !menuCommandPattern.MatchString(name) {
				log.Warn("group menu command is not a valid command name; skipped", slog.String("command", name))
				continue
			}
			out = append(out, client.BotCommand{
				Command:     name,
				Description: g.messages.T(lang, groups.KeyMenuDescPrefix+name, nil),
				IsEphemeral: true,
			})
		}
		return out
	}
	scope := &client.BotCommandScope{Type: client.ScopeAllGroupChats}

	langs := append([]string{""}, g.messages.Languages()...)
	for _, lang := range langs {
		text := lang
		if lang == "" {
			text = g.messages.Default()
		}
		cmds := build(text)
		if len(cmds) == 0 {
			return
		}
		if err := g.limiter.Wait(ctx, bot.BotKey); err != nil {
			return
		}
		if err := api.SetMyCommands(ctx, cmds, scope, lang); err != nil {
			log.Warn("cannot register the group command menu",
				slog.String("language", lang), slog.String("error", err.Error()))
			return
		}
	}
	log.Info("group command menu registered", slog.Int("commands", len(g.cfg.Groups.Menu)))
}
