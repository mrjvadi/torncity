package handlers

import (
	"context"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Reverse engineering (docs/adr/0005-item-and-production-model.md §6): a
// company takes apart a piece of another company's design it holds. The
// piece is destroyed when the work starts; when the scheduled action runs,
// once, item.ReverseEngineer decides — by the engineer's skill against the
// archetype's difficulty, on a roll drawn from the job — whether a copy of
// the design is recovered. A copy is a degraded design (worse quality, more
// input) of the company's own; it never carries a technology, so a part the
// copier cannot make it must buy.

// reverseReference is the journal reference_type of a reverse engineering.
const reverseReference = "reverse_jobs"

// sample is a piece in the warehouse the company may take apart.
type sample struct {
	piece  application.Piece
	design *application.Design
	maker  *application.Company
	arch   item.Archetype
}

// samples lists the pieces of other companies' designs in a company's
// warehouse whose kind the company makes.
func (h *ProductionHandler) samples(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor) ([]sample, error) {
	_, pieces, err := tx.Items().OrgHoldings(ctx, application.CompanyOrg(f.c.ID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	var out []sample
	for _, p := range pieces {
		s, ok, err := h.asSample(ctx, tx, snap, f, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// asSample reports whether a piece is one the company may take apart.
func (h *ProductionHandler) asSample(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, p application.Piece,
) (sample, bool, error) {
	if p.DesignID == "" {
		return sample{}, false, nil
	}
	d, err := tx.Production().DesignByID(ctx, p.DesignID)
	if err != nil {
		return sample{}, false, err
	}
	if d.CompanyID == f.c.ID || !f.def.Makes(d.Archetype) {
		return sample{}, false, nil
	}
	a, ok := snap.Archetype(d.Archetype)
	if !ok || !a.Method.ProducesGoods() {
		return sample{}, false, nil
	}
	maker, err := tx.Companies().ByID(ctx, d.CompanyID)
	if err != nil {
		return sample{}, false, err
	}
	return sample{piece: p, design: d, maker: maker, arch: a}, true, nil
}

// line is a sample for a screen, with the company's chance on it.
func (s sample) line(snap *content.Snapshot, level int) screens.SampleLine {
	return screens.SampleLine{Serial: s.piece.Serial, Good: designGood(snap, *s.design), Maker: s.maker.Name,
		Quality: s.piece.Quality, ChanceBPS: item.ReverseChanceBPS(level, s.arch.ReverseDifficulty)}
}

// ReverseLab handles company.relab: the samples the company holds, its
// engineer, its reverse engineering running and done.
func (h *ProductionHandler) ReverseLab(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.reverseLabWith(ctx, meta, req, "", "")
}

func (h *ProductionHandler) reverseLabWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest, confirm, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ReverseLabView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		view = screens.ReverseLabView{Ref: companyRef(snap, *c), Time: h.scale.RealWait(h.rules.ReverseTime), Notice: notice}
		list, err := h.samples(ctx, tx, snap, f)
		if err != nil {
			return err
		}
		for _, s := range list {
			level, _, err := f.best(ctx, tx, s.arch.ReverseSkill)
			if err != nil {
				return err
			}
			if view.Skill == "" {
				view.Skill, view.Level = s.arch.ReverseSkill, level
			}
			line := s.line(snap, level)
			view.Samples = append(view.Samples, line)
			if confirm != "" && s.piece.Serial == confirm {
				view.Confirm = &line
			}
		}
		if view.Skill == "" {
			for _, a := range f.def.Produces {
				if arch, ok := snap.Archetype(a); ok {
					view.Skill = arch.ReverseSkill
					if view.Level, _, err = f.best(ctx, tx, arch.ReverseSkill); err != nil {
						return err
					}
					break
				}
			}
		}
		jobs, err := tx.Production().Reverses(ctx, c.ID, 10)
		if err != nil {
			return err
		}
		now := h.now()
		for _, j := range jobs {
			src, err := tx.Production().DesignByID(ctx, j.SourceDesignID)
			if err != nil {
				return err
			}
			line := screens.ReverseLine{No: j.No, Good: designGood(snap, *src), Status: j.Status, FinishAt: j.FinishAt,
				Left: countdownTo(j.FinishAt, now)}
			if j.ResultDesignID != "" {
				res, err := tx.Production().DesignByID(ctx, j.ResultDesignID)
				if err != nil {
					return err
				}
				line.Result, line.ResultNo = res.Name, res.No
			}
			view.Jobs = append(view.Jobs, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.ReverseLab(h.screen(meta, lang), view), nil
}

// Reverse handles company.reverse: taking a sample apart. Unconfirmed, it
// shows the chance and asks; confirmed, the sample is destroyed now and the
// work runs on the game clock.
func (h *ProductionHandler) Reverse(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	serial := strings.ToUpper(strings.TrimSpace(req.Serial))
	if !req.confirmed() {
		return h.reverseLabWith(ctx, meta, req, serial, "")
	}
	snap := h.content.Current()
	lang := meta.Language
	notice := ""
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		back := []string{screens.AddrReverseLab, c.Code}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		org := application.CompanyOrg(c.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		piece, err := tx.Items().PieceBySerial(ctx, serial)
		if isSentinel(err, application.ErrPieceNotFound) || (err == nil && (piece.Org != org || piece.Holding != application.HoldWarehouse)) {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		if err != nil {
			return err
		}
		s, ok, err := h.asSample(ctx, tx, snap, f, *piece)
		if err != nil {
			return err
		}
		if !ok {
			kind := screens.ProductionRefusedNotReversible
			if piece.DesignID != "" {
				if d, err := tx.Production().DesignByID(ctx, piece.DesignID); err == nil && d.CompanyID == c.ID {
					kind = screens.ProductionRefusedOwnDesign
				}
			}
			return refuseProduction(kind, c, snap).back(back...)
		}
		level, engineer, err := f.best(ctx, tx, s.arch.ReverseSkill)
		if err != nil {
			return err
		}
		now := h.now()
		id := h.ids.NewID()
		finish := now.Add(h.scale.RealWait(h.rules.ReverseTime))
		actionID, err := h.schedule(ctx, tx, application.ReverseActionType, reverseReference, id, c.ID, now, finish)
		if err != nil {
			return err
		}
		if _, err := tx.Production().StartReverse(ctx, application.ReverseJob{ID: id, CompanyID: c.ID, PieceID: piece.ID,
			SourceDesignID: s.design.ID, Item: piece.Item, EngineerID: engineer, Skill: s.arch.ReverseSkill, Level: level,
			ChanceBPS: int(item.ReverseChanceBPS(level, s.arch.ReverseDifficulty)), GameActionID: actionID, StartedBy: p.ID,
			StartedAt: now, FinishAt: finish}); err != nil {
			return err
		}
		// The sample is destroyed whether or not the work succeeds.
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: piece.Item, PieceID: piece.ID, Qty: 1,
			FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemReverseSample,
			ReferenceType: reverseReference, ReferenceID: id, At: now}); err != nil {
			return err
		}
		c2 := h.screen(meta, lang)
		notice = c2.T("production.reverse_started", map[string]any{"good": c2.GoodName(designGood(snap, *s.design)),
			"time": screens.FormatClock(c2, finish), "duration": screens.FormatDuration(c2, countdownTo(finish, now))})
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.reverseLabWith(ctx, meta, req, "", notice)
}

// Reversed handles company.reversed from the SCHEDULER: a reverse
// engineering finishing. Exactly once: the job row, locked, must still be
// running under the action that finishes it. A success adds a degraded copy
// of the design to the company's designs — and nothing to its technologies.
func (h *ProductionHandler) Reversed(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := productionPayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		j, err := tx.Production().Reverse(ctx, in.ID)
		if isSentinel(err, application.ErrReverseNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if j.Status != application.ReverseRunning || (req.ActionID != "" && j.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(j.FinishAt) {
			return internalf("a reverse engineering finished before its time")
		}
		c, err := tx.Companies().Lock(ctx, j.CompanyID)
		if err != nil {
			return err
		}
		src, err := tx.Production().DesignByID(ctx, j.SourceDesignID)
		if err != nil {
			return err
		}
		a, ok := snap.Archetype(src.Archetype)
		if !ok {
			return internalf("a design of an archetype the content does not have: " + src.Archetype)
		}
		out, err := item.ReverseEngineer(a, domainDesign(*src), item.Engineer{Skill: j.Skill, Level: j.Level}, rollFrom(j.ID, 0))
		if err != nil {
			return errors.Internal(err)
		}
		status, resultID := application.ReverseFailed, ""
		payload := map[string]any{"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode,
			"owner_id": c.OwnerID, "item": src.Item, "source": src.Name, "succeeded": out.Succeeded}
		if out.Succeeded && c.Active() {
			copyDesign, err := tx.Production().CreateDesign(ctx, application.Design{ID: h.ids.NewID(), CompanyID: c.ID,
				Item: src.Item, Archetype: src.Archetype, Name: src.Name, NameKey: company.NameKey(src.Name),
				Origin: string(item.OriginReverseEngineered), Status: application.DesignFinal, Fills: storedFills(out.Design.Fills),
				QualityLossBPS: out.Design.QualityLossBPS, OverheadBPS: out.Design.OverheadBPS, SourceDesignID: src.ID,
				CreatedBy: j.StartedBy, CreatedAt: now, FinalizedAt: &now})
			if err != nil {
				return err
			}
			status, resultID = application.ReverseSucceeded, copyDesign.ID
			payload["design"], payload["design_no"] = copyDesign.Name, copyDesign.No
		}
		if err := tx.Production().FinishReverse(ctx, j.ID, status, resultID, now); err != nil {
			return err
		}
		return appendCompanyEvent(ctx, tx, meta, "reversed", c.ID, payload)
	})
}
