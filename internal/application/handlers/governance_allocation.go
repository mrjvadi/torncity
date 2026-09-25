package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file serves allocation levers — a city's budget
// (docs/adr/0024-property-and-politics.md): the editor that divides the
// whole among the lever's categories, its confirmation, and the change,
// which goes through application.SetAllocation or, when the council must
// confirm it, to a vote.
//
// The draft travels in the button's address, one base-36 character per
// category in the lever's order, each the share in steps of
// governance.allocation_step_bps. A draft is only a proposal: the shares are
// checked again by SetAllocation and by the database.

// allocationDigits are the characters of an encoded share.
const allocationDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// encodeAllocation writes shares as a draft. A share that is not a whole
// number of steps is rounded down.
func encodeAllocation(categories []string, shares map[string]int64, step int64) string {
	var b strings.Builder
	for _, c := range categories {
		n := shares[c] / step
		n = min(max(n, 0), int64(len(allocationDigits)-1))
		b.WriteByte(allocationDigits[n])
	}
	return b.String()
}

// decodeAllocation reads a draft back into shares; false for one that does
// not fit the categories.
func decodeAllocation(categories []string, raw string, step int64) (map[string]int64, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) != len(categories) {
		return nil, false
	}
	out := map[string]int64{}
	for i, c := range categories {
		n := strings.IndexByte(allocationDigits, raw[i])
		if n < 0 {
			return nil, false
		}
		if n > 0 {
			out[c] = int64(n) * step
		}
	}
	return out, true
}

// GovAllocRequest is the payload of gov.alloc, gov.allocok and gov.allocset:
// the lever, the place and the draft.
type GovAllocRequest struct {
	Lever string `json:"lever,omitempty"`
	Place string `json:"place,omitempty"`
	Draft string `json:"draft,omitempty"`
}

// allocation renders the editor with a draft: the one given, else the
// shares that will be in force.
func (h *GovernanceHandler) allocation(ctx context.Context, c screens.Context, t *leverTarget, raw string, now time.Time,
) (*presenter.Response, error) {
	step := h.steps.AllocationStep
	if step <= 0 {
		return nil, errors.Internal(errors.InvalidInput("no allocation step is configured"))
	}
	wait, err := h.nextChangeIn(ctx, t, now)
	if err != nil {
		return nil, err
	}
	current := t.value.Allocation
	if t.value.Pending != nil {
		current = t.value.Pending.Allocation
	}
	draft, ok := decodeAllocation(t.def.Categories, raw, step)
	if !ok {
		draft = current
	}
	place, lever := t.view(now)
	v := screens.AllocationEditView{Place: place, Lever: lever, NextChangeIn: wait,
		Draft: encodeAllocation(t.def.Categories, draft, step)}
	for _, code := range t.def.Categories {
		v.Total += draft[code]
	}
	for _, code := range t.def.Categories {
		line := screens.AllocationLine{Code: code, Share: draft[code]}
		if draft[code] >= step {
			down := copyShares(draft)
			down[code] -= step
			line.Down = encodeAllocation(t.def.Categories, down, step)
		}
		if v.Total+step <= application.AllocationWhole {
			up := copyShares(draft)
			up[code] += step
			line.Up = encodeAllocation(t.def.Categories, up, step)
		}
		v.Lines = append(v.Lines, line)
	}
	v.Changed = !application.SameAllocation(draft, t.value.Allocation)
	if h.content != nil {
		if b, ok := h.content.Current().Budget(); ok && b.Lever == t.def.Code {
			v.SpendShareBPS = b.SpendShareBPS
		}
	}
	return screens.AllocationEdit(c, v), nil
}

func copyShares(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Alloc handles gov.alloc: the editor with a draft.
func (h *GovernanceHandler) Alloc(ctx context.Context, meta envelope.Metadata, req GovAllocRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	now := h.now()
	t, err := h.target(ctx, p, GovLeverRequest{Lever: req.Lever, Place: req.Place})
	if err == nil && !t.def.IsAllocation() {
		err = application.ErrLeverKindUnsupported
	}
	if err != nil {
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	return h.allocation(ctx, c, t, req.Draft, now)
}

// AllocConfirm handles gov.allocok: the allocation spelled out before it is
// announced or put to a vote.
func (h *GovernanceHandler) AllocConfirm(ctx context.Context, meta envelope.Metadata, req GovAllocRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	now := h.now()
	t, err := h.target(ctx, p, GovLeverRequest{Lever: req.Lever, Place: req.Place})
	if err == nil && !t.def.IsAllocation() {
		err = application.ErrLeverKindUnsupported
	}
	var shares map[string]int64
	if err == nil {
		var ok bool
		if shares, ok = decodeAllocation(t.def.Categories, req.Draft, h.steps.AllocationStep); !ok {
			err = application.ErrInvalidAllocation
		}
	}
	if err == nil {
		err = application.CheckAllocation(t.def, shares)
	}
	if err == nil {
		var wait time.Duration
		if wait, err = h.nextChangeIn(ctx, t, now); err == nil && wait > 0 {
			err = application.ErrPolicyCooldown.WithDetail("available_at", now.Add(wait))
		}
	}
	if err != nil {
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	body, err := h.voteBy(ctx, t, application.ProposedValue{Allocation: shares})
	if err != nil {
		return nil, err
	}
	place, lever := t.view(now)
	return screens.AllocationConfirm(c, screens.AllocationConfirmView{Place: place, Lever: lever,
		Draft: encodeAllocation(t.def.Categories, shares, h.steps.AllocationStep), New: shares, VoteBy: body}), nil
}

// AllocSet handles gov.allocset: the allocation announced through
// SetAllocation, or put to the vote of the body that must confirm it, with
// the command's idempotency key. A redelivered press changes nothing.
func (h *GovernanceHandler) AllocSet(ctx context.Context, meta envelope.Metadata, req GovAllocRequest) (*presenter.Response, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, err
	}
	now := h.now()
	lang := meta.Language
	t := &leverTarget{}
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range levers {
		if l.Code == strings.TrimSpace(req.Lever) {
			t.def = l
		}
	}
	var (
		p      *application.Player
		change application.PolicyChange
		replay bool
		bill   *application.Proposal
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if p, err = tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID); err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if t.def.Code == "" {
			return application.ErrUnknownLever
		}
		if !t.def.IsAllocation() {
			return application.ErrLeverKindUnsupported
		}
		if t.place, err = h.dir.JurisdictionByCode(ctx, t.def.Jurisdiction, strings.TrimSpace(req.Place)); err != nil {
			return err
		}
		shares, ok := decodeAllocation(t.def.Categories, req.Draft, h.steps.AllocationStep)
		if !ok {
			return application.ErrInvalidAllocation
		}
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			replay = true
			return nil
		}
		v := application.ProposedValue{Allocation: shares}
		if h.legislature != nil {
			draft, vote, err := application.DraftPolicyVote(ctx, tx, p.ID, t.place.ID, t.def.Code, v, now)
			if err != nil {
				return err
			}
			if vote {
				opened, err := h.legislature.OpenBill(ctx, tx, meta, PolicyBill(draft, p.ID), now)
				bill = &opened
				return err
			}
		}
		if change, err = application.SetAllocation(ctx, tx, p.ID, t.place.ID, t.def.Code, shares, now); err != nil {
			return err
		}
		return appendPolicyChanged(ctx, tx, meta, t.place, change)
	})
	c := h.screen(meta, lang)
	if resp, ok, err := h.billOpened(ctx, meta, lang, bill, err); ok {
		return resp, err
	}
	if err != nil {
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	if replay {
		return h.lever(ctx, c, p, GovLeverRequest{Lever: t.def.Code, Place: t.place.Code})
	}
	place := govPlace(t.place)
	lever := screens.GovLever{Code: t.def.Code, Type: t.def.Type, HeldBy: t.def.HeldBy, Categories: t.def.Categories}
	return screens.PolicyAnnounced(c, screens.PolicyAnnouncedView{
		Place: place, Lever: lever,
		OldAllocation: change.OldAllocation, NewAllocation: change.Setting.Allocation,
		In: change.Setting.EffectiveAt.Sub(now),
	}), nil
}
