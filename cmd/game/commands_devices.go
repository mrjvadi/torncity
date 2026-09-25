package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// Game clients (api/client-api.md): link codes and linked devices.
type deviceHandlers struct {
	devices *handlers.DevicesHandler
}

// bindDevices maps the game-client commands to their handlers.
func (h phaseHandlers) bindDevices() map[string]commandFunc {
	d := h.clients.devices
	return map[string]commandFunc{
		"device.link":   bare(d.Link),
		"device.list":   bare(d.List),
		"device.revoke": decoded(d.Revoke),
	}
}
