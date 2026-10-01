package main

import (
	"context"

	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/telegram/render"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// legacyText is the compatibility rendering of a neutral answer
// (docs/adr/0037): what Telegram would show for the screen, for a web client
// deployed before it draws the screen from the view. It is wired here, in the
// process's composition root, and nowhere inside internal/clientapi, so the
// client API package itself never reaches for Telegram's wording; it is
// switched on per screen by client.legacy_text_screens and deleted with the
// last screen of the migration.
func legacyText(msgs screens.Translator) clientapi.LegacyText {
	return func(_ context.Context, resp *presentation.Response) (clientapi.LegacyRendering, error) {
		// A game client is served like a private chat: nothing is shared.
		out, err := render.Render(msgs, render.Delivery{}, resp)
		if err != nil {
			return clientapi.LegacyRendering{}, err
		}
		lr := clientapi.LegacyRendering{Text: out.Text, Labels: map[string]string{}}
		if out.Keyboard != nil {
			for _, row := range out.Keyboard.Rows {
				for _, b := range row {
					if b.CallbackData != "" {
						lr.Labels[b.CallbackData] = b.Text
					}
				}
			}
		}
		return lr, nil
	}
}
