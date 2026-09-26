package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// The notification inbox (migrations/0037_notification_inbox): what
// cmd/notifier stored instead of flooding a player with separate messages.
type inboxHandlers struct {
	inbox *handlers.InboxHandler
}

// bindInbox maps the inbox commands to their handler.
func (h phaseHandlers) bindInbox() map[string]commandFunc {
	ih := h.inbox.inbox
	return map[string]commandFunc{
		"inbox.show":     bare(ih.Show),
		"inbox.read_all": bare(ih.ReadAll),
		"inbox.category": decoded(ih.Category),
	}
}
