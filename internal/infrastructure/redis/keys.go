// Package redis holds the coordination adapters: update deduplication, the
// per-player lock and the per-bot polling lease.
//
// Nothing here is a source of truth. PostgreSQL owns state; Redis only answers
// "has this already happened" and "who currently holds this" fast enough that
// a Telegram update does not queue behind a database round trip. Every value
// written here therefore carries a TTL: if this process dies holding a lock,
// the system must recover by itself rather than wait for an operator.
package redis

import "strconv"

// Key namespaces. Every key this package writes begins with one of these, so
// a Redis instance shared with anything else stays legible and an operator can
// scan or flush one concern without touching the others.
const (
	dedupPrefix    = "gateway:dedup:"
	lockPrefix     = "lock:player:"
	botLeasePrefix = "gateway:bot-lease:"
)

// dedupKey names the marker for one Telegram update on one bot.
//
// The bot id is part of the key, not just the update id. Telegram numbers
// updates per bot, so two bots in the fleet routinely hand out the same
// update_id for two entirely unrelated messages; a key on update_id alone
// would make the second bot's update vanish.
func dedupKey(botID string, updateID int64) string {
	return dedupPrefix + botID + ":" + strconv.FormatInt(updateID, 10)
}

// playerLockKey names the mutual-exclusion key for one player.
func playerLockKey(playerID string) string {
	return lockPrefix + playerID
}

// botLeaseKey names the "I am the instance polling this bot" key.
//
// Keyed on bot_key (bot01, bot02, …) rather than on the bot's uuid, because
// that is the identifier an operator reads in configuration and in logs when
// they need to work out which instance owns which bot.
func botLeaseKey(botKey string) string {
	return botLeasePrefix + botKey
}
