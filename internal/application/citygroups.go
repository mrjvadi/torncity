package application

import (
	"context"
	"time"
)

// A city's Telegram groups (migrations/0016).
//
// A city is played in one or more Telegram groups: its public square. The
// game posts the city's public news there — a player arrived, a player was
// jailed — and tells a player where a group-only command is played. For now an
// operator links an existing city to a group (`admin city link-group`); when
// players found their own cities, founding one will write the same row.

// CityGroup is one group a city is played in.
type CityGroup struct {
	CityID   string
	CityCode string
	CityName string
	// ChatID is the Telegram group or supergroup (negative).
	ChatID int64
	// BotID is the bot that serves the group: the one that posts there.
	BotID string
	// Language is the group's language, in which its lines are written.
	Language string
	LinkedBy string
	LinkedAt time.Time
}

// CityGroupRepository reads and keeps the links between cities and groups.
type CityGroupRepository interface {
	// ForCity returns the groups a city is played in, by city id or, when
	// cityID is empty, by city code. None is an empty slice.
	ForCity(ctx context.Context, cityID, cityCode string) ([]CityGroup, error)
	// ByChat returns the group's link, or nil when the chat is linked to no
	// city.
	ByChat(ctx context.Context, chatID int64) (*CityGroup, error)
}
