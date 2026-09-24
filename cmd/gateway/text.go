package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file is what a player can TYPE besides a slash-command: an alias of a
// command in their own language («دزدی»), and the answer to a question the bot
// asked (internal/gateway/input). It also holds the channel rule: which
// commands run in a group, which in the private chat (configs/commands.yml).

// typed is what a plain-text message turned out to be.
type typed struct {
	// command and payload are set when the message answered a waiting
	// input: the command is ready to publish as it stands.
	command string
	payload map[string]any
	// aliased is true when the message was an alias, rewritten in place to
	// the slash-command it stands for.
	aliased bool
	// done is true when the message has been answered here (a cancelled
	// input) and nothing else is to happen.
	done bool
}

// readTyped interprets a plain-text message before it is routed. It may
// rewrite update.Message.Text: an alias becomes the slash-command it stands
// for, so everything after this — the group claim, the router — sees an
// ordinary command.
func (g *gateway) readTyped(ctx context.Context, bot application.Bot, update *client.Update, meta envelope.Metadata, log *slog.Logger) typed {
	msg := update.Message
	if msg == nil || msg.From == nil || strings.TrimSpace(msg.Text) == "" {
		return typed{}
	}
	inGroup := groups.IsGroupChat(msg.Chat.Type, msg.Chat.ID)
	if inGroup && msg.From.IsBot {
		return typed{}
	}
	text := strings.TrimSpace(msg.Text)
	slash := strings.HasPrefix(text, "/")

	rewritten, isAlias := "", false
	if !slash {
		rewritten, isAlias = g.aliases.Rewrite(text, inGroup)
	}
	cancel := strings.EqualFold(text, routing.CancelText) || (isAlias && rewritten == routing.CancelText)

	if res, handled := g.takeInput(ctx, bot, msg, meta, inGroup, slash || isAlias, cancel, text, log); handled {
		return res
	}
	if isAlias && rewritten != routing.CancelText {
		replaced := *msg
		replaced.Text = rewritten
		update.Message = &replaced
		return typed{aliased: true}
	}
	return typed{}
}

// takeInput answers a waiting input, if the message is its answer. See
// internal/gateway/input for when it is: in the private chat, the next text
// that is not a command; in a group, only a reply to the question.
func (g *gateway) takeInput(ctx context.Context, bot application.Bot, msg *client.Message, meta envelope.Metadata,
	inGroup, commandLike, cancel bool, text string, log *slog.Logger,
) (typed, bool) {
	if g.inputs == nil {
		return typed{}, false
	}
	key := input.Key{BotID: bot.ID, ChatID: msg.Chat.ID, UserID: msg.From.ID}
	var prompt int64
	if inGroup {
		reply := msg.ReplyToMessage
		if reply == nil || reply.From == nil || !reply.From.IsBot || reply.MessageID == 0 {
			return typed{}, false
		}
		prompt = reply.MessageID
	}

	if commandLike && !cancel {
		// The player moved on to something else: the question is dropped
		// and the command goes its usual way.
		if !inGroup {
			if err := g.inputs.Drop(ctx, key); err != nil {
				log.Warn("cannot drop a waiting input", append(metaAttrs(meta), slog.String("error", err.Error()))...)
			}
		}
		return typed{}, false
	}

	value, err := g.inputs.Take(ctx, key, prompt)
	if err != nil {
		log.Warn("cannot read a waiting input", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return typed{}, false
	}
	if value == "" {
		return typed{}, false
	}
	if cancel {
		g.reply(ctx, bot, meta, screens.InputCancelled(g.screenContext(ctx, meta, log)), log)
		return typed{done: true}, true
	}
	pending, err := input.Decode(value)
	if err != nil {
		log.Warn("a waiting input is unreadable", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return typed{}, false
	}
	answer := input.CleanValue(text, g.cfg.Input.MaxLength)
	if answer == "" {
		return typed{}, false
	}
	return typed{command: pending.Command, payload: pending.Build(answer)}, true
}

// ask sends the question a button asked for ("ask:<command>[:<arg>...]") and
// records the waiting input. It publishes nothing; the answer will.
func (g *gateway) ask(ctx context.Context, bot application.Bot, meta envelope.Metadata, cq *client.CallbackQuery, log *slog.Logger) {
	command, args, _ := input.ParseAsk(cq.Data)
	spec, ok := g.policy.Input(command)
	inGroup := meta.InGroup()
	// A button may leave trailing arguments off (the origin group of a
	// payment, when it did not fit), never add any.
	if !ok || len(args) > len(spec.Args) || g.inputs == nil {
		log.Info("a button asked for input nothing waits for",
			append(metaAttrs(meta), slog.String("command", command))...)
		g.acknowledge(ctx, bot, meta)
		return
	}
	if !g.policy.Allowed(command, inGroup) {
		g.wrongChannel(ctx, bot, meta, command, log)
		return
	}

	key := input.Key{BotID: bot.ID, ChatID: meta.TelegramChatID, UserID: meta.TelegramUserID}
	armed, err := g.inputs.Arm(ctx, key, g.cfg.Input.Cooldown)
	if err != nil {
		log.Warn("cannot throttle an input question; asking anyway",
			append(metaAttrs(meta), slog.String("error", err.Error()))...)
		armed = true
	}
	if !armed {
		// Pressed again before the cooldown: the question is already there.
		g.acknowledge(ctx, bot, meta)
		return
	}

	c := g.screenContext(ctx, meta, log)
	mention := ""
	if inGroup && cq.From.Username != "" {
		mention = "@" + cq.From.Username
	}
	text := screens.InputPrompt(c, command, mention)
	markup := client.ForceReply{
		ForceReply:            true,
		InputFieldPlaceholder: screens.InputPlaceholder(c, command),
		// Selective targets the users @mentioned in the text; without a
		// mention, everyone in the group sees the reply box, and only this
		// player's reply is read.
		Selective: mention != "",
	}
	promptID, err := g.sendPrompt(ctx, bot, meta.TelegramChatID, text, markup)
	g.acknowledge(ctx, bot, meta)
	if err != nil {
		log.Warn("cannot ask for input", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}

	payload := make(map[string]string, len(args))
	for i, arg := range args {
		payload[spec.Args[i]] = arg
	}
	value, err := input.Encode(input.Pending{Command: command, Payload: payload, Field: spec.Field, Prompt: promptID})
	if err == nil {
		err = g.inputs.Put(ctx, key, value, g.cfg.Input.TTL)
	}
	if err != nil {
		log.Warn("cannot record a waiting input", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return
	}
	log.Info("asked for input", append(metaAttrs(meta), slog.String("command", command))...)
}

// sendPrompt sends a question with its ForceReply markup and returns its
// message id.
func (g *gateway) sendPrompt(ctx context.Context, bot application.Bot, chatID int64, text string, markup any) (int64, error) {
	if g.prompter != nil {
		return g.prompter(ctx, bot, chatID, text, markup)
	}
	api := g.clientFor(bot.BotKey)
	if api == nil {
		return 0, errors.New("gateway: no api client for the bot")
	}
	if g.limiter != nil {
		if err := g.limiter.Wait(ctx, bot.BotKey); err != nil {
			return 0, err
		}
	}
	sent, err := api.SendMessageWith(ctx, chatID, text, markup, client.SendOptions{})
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// acknowledge stops a pressed button's spinner.
func (g *gateway) acknowledge(ctx context.Context, bot application.Bot, meta envelope.Metadata) {
	if meta.CallbackQueryID == nil {
		return
	}
	if api := g.clientFor(bot.BotKey); api != nil {
		_ = api.AnswerCallbackQuery(ctx, *meta.CallbackQueryID, "")
	}
}

// wrongChannel answers a command sent where it does not run, instead of
// running it (configs/commands.yml). In a group, a private command gets a
// hint and a button that opens the private chat and runs it there — for a
// button press, the press opens the private chat at once; in the private
// chat, a group command gets a hint that it is done in a group.
func (g *gateway) wrongChannel(ctx context.Context, bot application.Bot, meta envelope.Metadata, command string, log *slog.Logger) {
	c := g.screenContext(ctx, meta, log)
	log.Info("command sent where it does not run",
		append(metaAttrs(meta), slog.String("command", command), slog.String("channel", string(g.policy.Channel(command))))...)

	if !meta.InGroup() {
		g.reply(ctx, bot, meta, screens.GroupOnly(c, g.homeGroupCity(ctx, meta, log)), log)
		return
	}

	link := groups.DeepLink(g.botUsername(ctx, bot.BotKey, g.clientFor(bot.BotKey)), groups.StartPayload(routing.Landing(command)))
	resp := screens.PrivateOnly(c, link)
	if meta.CallbackQueryID != nil {
		// A press: opening the private chat is the answer, and the group's
		// screen stays as it is. Without a link, a popup says where to go.
		answer := client.CallbackAnswer{CallbackQueryID: *meta.CallbackQueryID, URL: link}
		if link == "" {
			answer = client.CallbackAnswer{CallbackQueryID: *meta.CallbackQueryID, Text: resp.Text, ShowAlert: true}
		}
		if api := g.clientFor(bot.BotKey); api != nil {
			if err := api.AnswerCallback(ctx, answer); err != nil {
				log.Warn("cannot answer a press on a private command",
					append(metaAttrs(meta), slog.String("error", err.Error()))...)
			}
		}
		return
	}
	g.reply(ctx, bot, meta, resp, log)
}

// homeGroupCity is the city whose group a player plays crime in, for the hint
// in the private chat: the player's own city, when it has a group. Empty when
// that is not known.
func (g *gateway) homeGroupCity(ctx context.Context, meta envelope.Metadata, log *slog.Logger) string {
	if g.cityGroups == nil || g.players == nil {
		return ""
	}
	p, err := g.players.GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil || p == nil || p.CityID == nil || *p.CityID == "" {
		return ""
	}
	city, linked, err := g.cityGroups.CityWithGroup(ctx, *p.CityID)
	if err != nil {
		log.Warn("cannot read the player's city group", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		return ""
	}
	if !linked || city == nil {
		return ""
	}
	return screens.Context{Msgs: g.messages, Lang: meta.Language}.CityName(city.Code, city.Name)
}

// cityGroupReader is what the private-chat hint reads: whether the player's
// city has a group, and the city.
type cityGroupReader interface {
	CityWithGroup(ctx context.Context, cityID string) (*application.City, bool, error)
}

// screenContext is a screen context in the player's language: their stored
// choice where there is one, as every game screen.
func (g *gateway) screenContext(ctx context.Context, meta envelope.Metadata, log *slog.Logger) screens.Context {
	var p *application.Player
	if g.players != nil && meta.TelegramUserID != 0 {
		found, err := g.players.GetByTelegramUserID(ctx, meta.TelegramUserID)
		switch {
		case err == nil:
			p = found
		case !errors.Is(err, application.ErrPlayerNotFound):
			log.Warn("cannot read the player's language", append(metaAttrs(meta), slog.String("error", err.Error()))...)
		}
	}
	return screens.Context{Msgs: g.messages, Lang: handlers.RenderLanguage(meta, p), Shared: meta.InGroup()}
}

// reply delivers a response the gateway produced itself, as a new message.
func (g *gateway) reply(ctx context.Context, bot application.Bot, meta envelope.Metadata, resp *presenter.Response, log *slog.Logger) {
	deliver := g.deliver
	if deliver == nil {
		deliver = g.deliverViaFleet
	}
	deliver(ctx, bot, meta, resp, log)
}
