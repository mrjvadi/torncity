package envelope

// ChatTypeClient is the chat type of a command sent from a game client
// through the client API (cmd/clientapi) rather than from a Telegram chat.
//
// Such a command carries the player's Telegram identity and the bot they
// linked the client through, so every handler serves it like a private chat;
// its response is addressed to the client API, which waits for it, and the
// gateway must not also send it to Telegram.
const ChatTypeClient = "client"

// FromClient reports whether the request came from a game client.
func (m Metadata) FromClient() bool { return m.ChatType == ChatTypeClient }
