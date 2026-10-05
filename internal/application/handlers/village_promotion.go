package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// The promotion ladder village -> town -> city is retired (owner scope rule
// 2026-10-02, ADR 0044): a settlement grows by what it researches and builds,
// and nothing turns it into something else by itself. The two commands stay so an
// old client's button lands on a true screen: both answer the development readout
// (village_development.go).

// VillagePromoteRequest is the payload of settlement.promote (kept for old clients).
type VillagePromoteRequest struct {
	// Confirm was the second press of the retired act; it is ignored.
	Confirm string `json:"confirm,omitempty"`
}

// PromotionView handles settlement.promotion.view. It answers the development
// readout.
func (h *VillageHandler) PromotionView(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.DevelopmentView(ctx, meta)
}

// Promote handles settlement.promote. It promotes nothing, ever; it answers the
// development readout.
func (h *VillageHandler) Promote(ctx context.Context, meta envelope.Metadata, _ VillagePromoteRequest) (*presentation.Response, error) {
	return h.DevelopmentView(ctx, meta)
}
