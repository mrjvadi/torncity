package client

// The types below model only what phase 0 needs: identify the sender, read a
// text command or a callback, and answer it. The Bot API has hundreds of
// fields; modelling them now would be guessing at requirements. Add a field
// when a feature needs it, not before.
//
// No type here carries a token, and none ever should: these structs are
// candidates for inclusion in a NATS payload, which ADR 0002 forbids the token
// from entering.

// User is a Telegram user or bot.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`

	// CanJoinGroups and CanReadAllGroupMessages are returned only by getMe.
	// The second is "True, if privacy mode is disabled for the bot": a bot
	// with it false sees in a group only commands, replies to its own
	// messages and mentions — unless it is an administrator there.
	CanJoinGroups           bool `json:"can_join_groups,omitempty"`
	CanReadAllGroupMessages bool `json:"can_read_all_group_messages,omitempty"`
}

// ForceReply is the reply markup that opens the reply box on the user's
// side, as if they had chosen the bot's message and tapped Reply. Selective
// limits it to the users @mentioned in the text (or the sender of the message
// it answers).
type ForceReply struct {
	ForceReply            bool   `json:"force_reply"`
	InputFieldPlaceholder string `json:"input_field_placeholder,omitempty"`
	Selective             bool   `json:"selective,omitempty"`
}

// Chat is where a message happened. Type is "private", "group", "supergroup"
// or "channel"; the gateway routes on it.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

// Message is one message. Only the fields the router and the presenter need
// are present.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`

	// ReplyToMessage is the message this one answers, when the player used
	// Telegram's reply. In a group it is how a player points at another
	// player ("pay them"), so its From is what the gateway reads.
	ReplyToMessage *Message `json:"reply_to_message,omitempty"`
}

// ChatMember is one member's standing in a chat. Only the status is read:
// "creator", "administrator", "member", "restricted", "left" or "kicked".
type ChatMember struct {
	Status string `json:"status"`
	User   User   `json:"user"`
}

// ChatMemberUpdated reports a change of one member's standing. As the
// my_chat_member update it describes the bot itself: added to a group,
// promoted, or removed from it.
type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	From          User       `json:"from"`
	Date          int64      `json:"date"`
	OldChatMember ChatMember `json:"old_chat_member"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

// CallbackQuery is an inline-keyboard press. Message is nil when the original
// message is too old for Telegram to still attach it.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is one item from getUpdates. Exactly one of the optional fields is
// set for the update types phase 0 handles; an update of any other type
// arrives with all of them nil and is skipped by the router.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	EditedMessage *Message       `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`

	// MyChatMember is the bot's own membership changing in a chat: it was
	// added to a group, or removed from one.
	MyChatMember *ChatMemberUpdated `json:"my_chat_member,omitempty"`
}
