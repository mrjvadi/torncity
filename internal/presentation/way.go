package presentation

import "time"

// Way is the walk to the place a screen's service is at, when the player
// stands elsewhere in the city: the place, and the real time the walk takes.
type Way struct {
	Place Named
	// Walk is the real time the walk takes.
	Walk time.Duration
}
