package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A player card with the player's Telegram profile photo
// (docs/adr/0025-life-and-legacy.md). The game names the photo — the file id
// the answering bot already knows, or whose photo it is — and the gateway,
// the only process that talks to Telegram, fetches what the bot does not know
// yet (getUserProfilePhotos), keeps it for that bot (a file id is valid only
// for the bot that received it), and sends the card as a photo with its text
// as the caption. With no photo to send — none set, hidden from bots, a
// caption too long — the card goes out as text, as it would anyway.

// photoKeeper keeps what one bot knows of a player's photo.
type photoKeeper interface {
	KeepPhoto(ctx context.Context, playerID, botID, fileID string, at time.Time) error
}

// renderPhoto sends a response with a photo, and reports whether it did. A
// false with no error means the response should go out as text.
func (g *gateway) renderPhoto(ctx context.Context, api *client.Client, botKey string, meta envelope.Metadata,
	resp *presenter.Response, log *slog.Logger,
) (bool, error) {
	photo := resp.Photo
	if photo == nil || utf8.RuneCountInString(resp.Text) > client.MaxCaptionRunes {
		return false, nil
	}
	group := groups.IsGroupChat(meta.ChatType, meta.TelegramChatID)
	if group && g.policy != nil && g.policy.IsPrivate(meta.Command, resp) {
		return false, nil
	}
	fileID := photo.FileID
	if fileID == "" {
		if photo.UserID <= 0 {
			return false, nil
		}
		id, err := api.LatestProfilePhoto(ctx, photo.UserID)
		if err != nil {
			// A photo the bot may not fetch is no reason to lose the card.
			if log != nil {
				log.Warn("cannot fetch a profile photo", slog.String("error", err.Error()))
			}
			return false, nil
		}
		if id == "" {
			return false, nil
		}
		fileID = id
		if g.photos != nil && photo.PlayerID != "" {
			if err := g.photos.KeepPhoto(ctx, photo.PlayerID, meta.BotID, fileID, time.Now().UTC()); err != nil && log != nil {
				log.Warn("cannot keep a profile photo", slog.String("error", err.Error()))
			}
		}
	}
	markup := inlineKeyboard(resp.Keyboard)
	if group {
		bot := groups.Bot{Username: g.botUsername(ctx, botKey, api)}
		markup = g.groupRenderer().PublicMarkup(bot, meta, resp.Keyboard)
	}
	if _, err := api.SendPhoto(ctx, meta.TelegramChatID, fileID, resp.Text, markup); err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Code == 400 {
			// A file id Telegram no longer takes: the text still goes out.
			return false, nil
		}
		return false, err
	}
	acknowledgeCallback(ctx, api, meta, resp)
	return true, nil
}
