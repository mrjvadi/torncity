package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The founding form (docs/adr/0028-world-and-settlements.md section 3): three
// commands.
//
//	settlement.found         in the group: check who may found, open a draft,
//	                         answer with the button to the form.
//	settlement.found.draft   from the client: the form's facts (state, the
//	                         generated name, the emblem catalogue, the limits).
//	settlement.found.submit  from the client: check the form and, unless it
//	                         is only a check, found the village.
//
// Everything a draft holds is in the database; nothing waits in memory, so
// any replica answers any of the three. The draft's row is locked for the
// duration of a submit, so two submits of one draft (a double tap, a retry, a
// second replica) serialise, and the second finds the village already there
// and answers from it.

// FoundingConfig is the founding form's tuning (config settlement.founding_*):
// how long a draft waits, and the numeric bounds of the form. The lists —
// shapes, colours, icons, banned and reserved words — are content.
type FoundingConfig struct {
	DraftTTL time.Duration
	// Bounds carries only the numeric limits of a wsettle.FormRules; the
	// content's lists are added to it for each request.
	Bounds wsettle.FormRules
}

// FoundDraftRequest is the payload of settlement.found.draft. Without a
// draft id the player's own open draft is meant.
type FoundDraftRequest struct {
	Draft string `json:"draft"`
}

// FoundSubmitRequest is the payload of settlement.found.submit. Check
// ("1" or "true") validates the form without founding anything.
type FoundSubmitRequest struct {
	Draft          string `json:"draft"`
	Name           string `json:"name"`
	Motto          string `json:"motto"`
	CurrencyName   string `json:"currency_name"`
	CurrencyCode   string `json:"currency_code"`
	CurrencySymbol string `json:"currency_symbol"`
	Shape          string `json:"shape"`
	ColorA         string `json:"color_a"`
	ColorB         string `json:"color_b"`
	Icon           string `json:"icon"`
	Check          string `json:"check"`
}

func (r FoundSubmitRequest) checkOnly() bool {
	v := strings.ToLower(strings.TrimSpace(r.Check))
	return v == "1" || v == "true" || v == "yes"
}

// rules is the form's rule set for the current content, and its catalogue.
func (h *SettlementsHandler) rules() (wsettle.FormRules, content.FoundingDef, bool) {
	def, ok := h.content.Current().Founding()
	if !ok {
		return wsettle.FormRules{}, def, false
	}
	r := h.founding.Bounds
	choices := func(defs []content.FoundingChoiceDef) []wsettle.Choice {
		out := make([]wsettle.Choice, len(defs))
		for i, d := range defs {
			out[i] = wsettle.Choice{Code: d.Code, Emoji: d.Emoji}
		}
		return out
	}
	r.Shapes, r.Icons, r.Palette = choices(def.Shapes), choices(def.Icons), choices(def.Palette)
	r.BannedWords, r.ReservedNames, r.ReservedCodes = def.BannedWords, def.ReservedNames, def.ReservedCurrencyCodes
	return r, def, true
}

func limitsOf(r wsettle.FormRules) screens.FoundingLimitsView {
	return screens.FoundingLimitsView{
		NameMin: r.NameMin, NameMax: r.NameMax, MottoMax: r.MottoMax,
		CurrencyNameMin: r.CurrencyNameMin, CurrencyNameMax: r.CurrencyNameMax,
		CurrencyCodeLen: r.CurrencyCodeLen, CurrencySymbolMax: r.CurrencySymbolMax,
	}
}

// shownFounder is a player's name as the group's message shows it.
func (h *SettlementsHandler) shownFounder(c screens.Context, name string) string {
	if name == "" || strings.HasPrefix(name, "player-") {
		return c.T("social.unknown_player", nil)
	}
	return name
}

// normalDraftID accepts a draft id with or without its dashes (the Mini App
// start parameter carries it without).
func normalDraftID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) == 32 {
		return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	return id
}

// Found handles «ساخت روستا»: it checks who may found exactly as before and
// opens a draft, answering with the button to the form. It is idempotent
// under at-least-once delivery and under a group asking twice: one open draft
// per group, and a second ask shows the same one.
func (h *SettlementsHandler) Found(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	if !meta.InGroup() {
		return screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "group_only"}), nil
	}
	chatID := meta.TelegramChatID

	worldRow, world, err := h.worlds.Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		return screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "no_world"}), nil
	}
	if err != nil {
		return nil, err
	}
	if _, _, ok := h.rules(); !ok {
		return screens.FoundingRefusal(c, screens.FoundingRefusalView{Kind: screens.FoundingNoContent}), nil
	}

	now := h.now()
	var reply *presenter.Response
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		reply = nil
		if existing, err := tx.Settlements().ByFoundingGroup(ctx, chatID); err == nil {
			reply = screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "already", Name: existing.Name})
			return nil
		} else if !stderrors.Is(err, application.ErrCityNotFound) {
			return err
		}

		draft, err := tx.Settlements().OpenDraftOfChat(ctx, chatID, now)
		switch {
		case err == nil:
		case stderrors.Is(err, application.ErrFoundingDraftNotFound):
			cells, err := tx.Settlements().ExistingForWorld(ctx, worldRow.ID)
			if err != nil {
				return err
			}
			existing := make([]wsettle.ExistingSettlement, len(cells))
			for i, e := range cells {
				existing[i] = wsettle.ExistingSettlement{CellID: e.WorldCellID, TierWeight: tierWeight(e.Tier)}
			}
			n, err := tx.Worlds().ReserveSpawnNumber(ctx)
			if err != nil {
				return err
			}
			cand, err := wsettle.FindSpawn(world, existing, n, h.spawnParams)
			if err != nil {
				return fmt.Errorf("handlers: founding settlement: %w", err)
			}
			name := wsettle.GenerateName(world, cand.CellID, n)
			suggested := name.Latin
			if lang == "fa" && name.Persian != "" {
				suggested = name.Persian
			}
			if suggested == "" {
				suggested = h.ids.NewID()[:8]
			}
			draft, _, err = tx.Settlements().CreateDraft(ctx, application.FoundingDraft{
				ID: h.ids.NewID(), ChatID: chatID, BotID: meta.BotID, FounderPlayerID: p.ID, Language: lang,
				SuggestedName: suggested, SuggestedNameLatin: name.Latin, ExpiresAt: now.Add(h.founding.DraftTTL),
			}, now)
			if err != nil {
				return err
			}
		default:
			return err
		}

		remaining := draft.ExpiresAt.Sub(now)
		minutes := int((remaining + time.Minute - 1) / time.Minute)
		if minutes < 1 {
			minutes = 1
		}
		reply = screens.FoundDraft(c, screens.FoundDraftView{
			Founder: h.shownFounder(c, draft.FounderName), Pending: draft.FounderPlayerID != p.ID,
			Minutes: minutes, DraftID: draft.ID,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// defaultEmblem picks the emblem the form starts from, from the draft's id, so
// a reopened form shows the same one.
func defaultEmblem(seed string, r wsettle.FormRules) screens.FoundingEmblemView {
	f := fnv.New64a()
	_, _ = f.Write([]byte(seed))
	x := f.Sum64()
	pick := func(list []wsettle.Choice, shift uint) int {
		if len(list) == 0 {
			return -1
		}
		return int((x >> shift) % uint64(len(list)))
	}
	out := screens.FoundingEmblemView{}
	if i := pick(r.Shapes, 0); i >= 0 {
		out.Shape = r.Shapes[i].Code
	}
	if i := pick(r.Icons, 8); i >= 0 {
		out.Icon = r.Icons[i].Code
	}
	if a := pick(r.Palette, 16); a >= 0 {
		out.ColorA = r.Palette[a].Code
		out.ColorB = r.Palette[(a+1+int((x>>24)%uint64(max(len(r.Palette)-1, 1))))%len(r.Palette)].Code
	}
	return out
}

func choiceViews(c screens.Context, prefix string, list []content.FoundingChoiceDef) []screens.FoundingChoiceView {
	out := make([]screens.FoundingChoiceView, len(list))
	for i, d := range list {
		out[i] = screens.FoundingChoiceView{Code: d.Code, Name: c.FoundingChoiceName(prefix, d.Code), Emoji: d.Emoji, Hex: d.Hex}
	}
	return out
}

// formView is the form's facts for a client.
func (h *SettlementsHandler) formView(c screens.Context, rules wsettle.FormRules, def content.FoundingDef,
	d application.FoundingDraft, state, settlementID, settlementName string,
) screens.FoundingFormView {
	return screens.FoundingFormView{
		State: state, Draft: d.ID, ExpiresAt: d.ExpiresAt, Founder: h.shownFounder(c, d.FounderName),
		SuggestedName: d.SuggestedName, DefaultEmblem: defaultEmblem(d.ID, rules), Limits: limitsOf(rules),
		Shapes:          choiceViews(c, "shape", def.Shapes),
		Palette:         choiceViews(c, "color", def.Palette),
		Icons:           choiceViews(c, "icon", def.Icons),
		NeutralCurrency: neutralCurrency, SettlementID: settlementID, SettlementName: settlementName,
	}
}

// neutralCurrency is the money a village uses until it declares a country
// (ADR 0029 section 8).
const neutralCurrency = "SUP"

// FoundDraft handles settlement.found.draft: the form's facts, for whoever
// opens the link. Only the founder may submit it; anyone else reads it.
func (h *SettlementsHandler) FoundDraft(ctx context.Context, meta envelope.Metadata, req FoundDraftRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	rules, def, ok := h.rules()
	if !ok {
		return screens.FoundingRefusal(c, screens.FoundingRefusalView{Kind: screens.FoundingNoContent}), nil
	}
	now := h.now()
	var reply *presenter.Response
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var (
			d   application.FoundingDraft
			err error
		)
		if strings.TrimSpace(req.Draft) != "" {
			d, err = tx.Settlements().DraftByID(ctx, normalDraftID(req.Draft), now)
		} else {
			d, err = tx.Settlements().OpenDraftOfPlayer(ctx, p.ID, now)
		}
		if stderrors.Is(err, application.ErrFoundingDraftNotFound) {
			reply = screens.FoundingRefusal(c, screens.FoundingRefusalView{Kind: screens.FoundingNoDraft, Limits: limitsOf(rules)})
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case d.Status == application.DraftSubmitted:
			s, err := tx.Settlements().ByID(ctx, d.SettlementID)
			if err != nil {
				return err
			}
			reply = screens.FoundingForm(c, h.formView(c, rules, def, d, screens.FoundingDone, s.CityID, s.Name))
		case d.Expired(now):
			reply = screens.FoundingForm(c, h.formView(c, rules, def, d, screens.FoundingExpired, "", ""))
		case d.FounderPlayerID == p.ID:
			reply = screens.FoundingForm(c, h.formView(c, rules, def, d, screens.FoundingMine, "", ""))
		default:
			reply = screens.FoundingForm(c, h.formView(c, rules, def, d, screens.FoundingOther, "", ""))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// submitOutcome is what one attempt of a submit ended with.
type submitOutcome struct {
	refusal *screens.FoundingRefusalView
	group   *screens.SettlementRefusalView
	checked *screens.FoundingCheckedView
	// founded is set on success and for a repeated submit; cell is where.
	founded *application.FoundedSettlement
	cell    int32
	founder string
}

// Submit handles settlement.found.submit. It is idempotent: a second submit
// of a draft that already became a village answers from that village.
func (h *SettlementsHandler) Submit(ctx context.Context, meta envelope.Metadata, req FoundSubmitRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	rules, _, ok := h.rules()
	if !ok {
		return screens.FoundingRefusal(c, screens.FoundingRefusalView{Kind: screens.FoundingNoContent}), nil
	}
	worldRow, world, err := h.worlds.Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		return screens.FoundingRefusal(c, screens.FoundingRefusalView{Kind: screens.FoundingNoWorld, Limits: limitsOf(rules)}), nil
	}
	if err != nil {
		return nil, err
	}

	form := wsettle.Form{
		Name: req.Name, Motto: req.Motto, CurrencyName: req.CurrencyName, CurrencyCode: req.CurrencyCode,
		CurrencySymbol: req.CurrencySymbol,
		Emblem:         wsettle.Emblem{Shape: req.Shape, ColorA: req.ColorA, ColorB: req.ColorB, Icon: req.Icon},
	}
	invalid := func(problems ...wsettle.Problem) *screens.FoundingRefusalView {
		v := &screens.FoundingRefusalView{Kind: screens.FoundingInvalid, Limits: limitsOf(rules)}
		for _, x := range problems {
			v.Problems = append(v.Problems, screens.FoundingProblem{Field: x.Field, Code: x.Code})
		}
		return v
	}

	var out submitOutcome
	for attempt := 0; attempt < maxSpawnAttempts; attempt++ {
		out = submitOutcome{}
		err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			out = submitOutcome{}
			return h.submitInTx(ctx, tx, meta, p, lang, req, form, rules, worldRow, world, invalid, &out)
		})
		switch {
		case err == nil:
		case stderrors.Is(err, application.ErrSpawnCellTaken):
			continue
		case stderrors.Is(err, application.ErrFoundingNameTaken):
			out = submitOutcome{refusal: invalid(wsettle.Problem{Field: wsettle.FieldName, Code: wsettle.ProblemNameTaken})}
		case stderrors.Is(err, application.ErrFoundingCurrencyCodeTaken):
			out = submitOutcome{refusal: invalid(wsettle.Problem{Field: wsettle.FieldCurrencyCode, Code: wsettle.ProblemCurrencyCodeTaken})}
		case stderrors.Is(err, application.ErrFoundingCurrencyNameTaken):
			out = submitOutcome{refusal: invalid(wsettle.Problem{Field: wsettle.FieldCurrencyName, Code: wsettle.ProblemCurrencyNameTaken})}
		case stderrors.Is(err, application.ErrGroupAlreadyFounded):
			// Another route founded this group's village between the check and
			// the write: read it back as a repeated submit.
			out = submitOutcome{}
			if rerr := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
				d, err := tx.Settlements().DraftByID(ctx, normalDraftID(req.Draft), h.now())
				if err != nil {
					return err
				}
				s, err := tx.Settlements().ByFoundingGroup(ctx, d.ChatID)
				if err != nil {
					return err
				}
				out.group = &screens.SettlementRefusalView{Kind: "already", Name: s.Name}
				return nil
			}); rerr != nil {
				return nil, rerr
			}
		default:
			return nil, err
		}
		break
	}
	switch {
	case out.refusal != nil:
		return screens.FoundingRefusal(c, *out.refusal), nil
	case out.group != nil:
		return screens.SettlementRefusal(c, *out.group), nil
	case out.checked != nil:
		return screens.FoundingChecked(c, *out.checked), nil
	case out.founded != nil:
		return screens.SettlementFounded(c, h.foundedView(c, world, *out.founded, rules, out.founder, out.cell)), nil
	}
	return nil, fmt.Errorf("handlers: founding settlement: no eligible spot after %d attempts", maxSpawnAttempts)
}

// foundedView is the announcement's facts.
func (h *SettlementsHandler) foundedView(c screens.Context, world *worldgen.World, s application.FoundedSettlement,
	rules wsettle.FormRules, founder string, cell int32,
) screens.SettlementFoundedView {
	feature := wsettle.NearbyFeature(world, cell)
	featureName := feature.Latin
	if c.Lang == "fa" && feature.Persian != "" {
		featureName = feature.Persian
	}
	codes := make([]string, len(s.Buildings))
	for i, b := range s.Buildings {
		codes[i] = b.TypeCode
	}
	e := wsettle.Emblem{Shape: s.Emblem.Shape, ColorA: s.Emblem.ColorA, ColorB: s.Emblem.ColorB, Icon: s.Emblem.Icon}
	return screens.SettlementFoundedView{
		Name: s.Name, SettlementID: s.CityID, BiomeCode: world.BiomeCode(cell), NearbyFeature: featureName,
		Buildings: codes, ProtectedUntil: s.ProtectedUntil, Founder: founder,
		Emblem:     screens.FoundingEmblemView{Shape: e.Shape, ColorA: e.ColorA, ColorB: e.ColorB, Icon: e.Icon},
		EmblemText: rules.EmblemText(e), Motto: s.Motto,
		CurrencyName: s.Currency.Name, CurrencyCode: s.Currency.Code, CurrencySign: s.Currency.Symbol,
	}
}

// submitInTx is one attempt of a submit, inside its transaction. A refusal or
// a check is reported through out with a nil error (committing what the
// attempt wrote, an expired draft's status); a race that must roll back is
// returned as an error for Submit to interpret.
func (h *SettlementsHandler) submitInTx(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	p *application.Player, lang string, req FoundSubmitRequest, form wsettle.Form, rules wsettle.FormRules,
	worldRow application.World, world *worldgen.World, invalid func(...wsettle.Problem) *screens.FoundingRefusalView,
	out *submitOutcome,
) error {
	now := h.now()
	refuse := func(kind string) {
		out.refusal = &screens.FoundingRefusalView{Kind: kind, Limits: limitsOf(rules)}
	}

	draft, err := tx.Settlements().DraftByID(ctx, normalDraftID(req.Draft), now)
	if stderrors.Is(err, application.ErrFoundingDraftNotFound) {
		refuse(screens.FoundingNoDraft)
		return nil
	}
	if err != nil {
		return err
	}
	if draft.FounderPlayerID != p.ID {
		refuse(screens.FoundingNotFounder)
		return nil
	}
	out.founder = p.DisplayName

	switch {
	case draft.Status == application.DraftSubmitted:
		// A repeated submit: the village is there; answer from it.
		s, err := tx.Settlements().ByFoundingGroup(ctx, draft.ChatID)
		if err != nil {
			return err
		}
		out.founded, out.cell = &s, s.WorldCellID
		return nil
	case draft.Expired(now):
		refuse(screens.FoundingExpiredRef)
		return nil
	}

	if existing, err := tx.Settlements().ByFoundingGroup(ctx, draft.ChatID); err == nil {
		out.group = &screens.SettlementRefusalView{Kind: "already", Name: existing.Name}
		return nil
	} else if !stderrors.Is(err, application.ErrCityNotFound) {
		return err
	}

	clean, problems := rules.Check(form)
	nameKey, currencyKey := wsettle.NameKey(clean.Name), wsettle.NameKey(clean.CurrencyName)
	if !hasField(problems, wsettle.FieldName) {
		taken, err := tx.Settlements().FoundingNameTaken(ctx, nameKey)
		if err != nil {
			return err
		}
		if taken {
			problems = append(problems, wsettle.Problem{Field: wsettle.FieldName, Code: wsettle.ProblemNameTaken})
		}
	}
	codeOK, nameOK := !hasField(problems, wsettle.FieldCurrencyCode), !hasField(problems, wsettle.FieldCurrencyName)
	if codeOK || nameOK {
		codeTaken, nameTaken, err := tx.Settlements().CurrencyTaken(ctx, clean.CurrencyCode, currencyKey)
		if err != nil {
			return err
		}
		if codeOK && codeTaken {
			problems = append(problems, wsettle.Problem{Field: wsettle.FieldCurrencyCode, Code: wsettle.ProblemCurrencyCodeTaken})
		}
		if nameOK && nameTaken {
			problems = append(problems, wsettle.Problem{Field: wsettle.FieldCurrencyName, Code: wsettle.ProblemCurrencyNameTaken})
		}
	}
	if len(problems) > 0 {
		out.refusal = invalid(problems...)
		return nil
	}
	if req.checkOnly() {
		out.checked = &screens.FoundingCheckedView{Name: clean.Name, Currency: clean.CurrencyCode,
			EmblemText: rules.EmblemText(clean.Emblem)}
		return nil
	}

	cells, err := tx.Settlements().ExistingForWorld(ctx, worldRow.ID)
	if err != nil {
		return err
	}
	existing := make([]wsettle.ExistingSettlement, len(cells))
	for i, e := range cells {
		existing[i] = wsettle.ExistingSettlement{CellID: e.WorldCellID, TierWeight: tierWeight(e.Tier)}
	}
	n, err := tx.Worlds().ReserveSpawnNumber(ctx)
	if err != nil {
		return err
	}
	cand, err := wsettle.FindSpawn(world, existing, n, h.spawnParams)
	if err != nil {
		return fmt.Errorf("handlers: founding settlement: %w", err)
	}
	// The kit is laid on the grid the village really has: slid from the cell
	// centre by the site search, so it stands on land.
	gridLat, gridLon := wsettle.GridCentre(world, cand.LatDeg, cand.LonDeg, cand.ShiftX, cand.ShiftY)
	kit := wsettle.PlaceFoundingKit(world, gridLat, gridLon, h.villageGridLots)
	buildings := make([]application.SettlementBuilding, len(kit))
	for i, b := range kit {
		buildings[i] = application.SettlementBuilding{TypeCode: b.TypeCode, LotX: b.LotX, LotY: b.LotY}
	}

	f := application.Founding{
		WorldID: worldRow.ID, WorldCellID: cand.CellID, LatDeg: cand.LatDeg, LonDeg: cand.LonDeg,
		GridShiftX: cand.ShiftX, GridShiftY: cand.ShiftY,
		Tier: "village", Code: settlementCode(h.ids), Name: clean.Name,
		CountryCode: application.DefaultFoundingCountryCode, FounderPlayerID: p.ID,
		Emblem:  application.EmblemCodes{Shape: clean.Emblem.Shape, ColorA: clean.Emblem.ColorA, ColorB: clean.Emblem.ColorB, Icon: clean.Emblem.Icon},
		Motto:   clean.Motto,
		NameKey: nameKey,
		Currency: application.VillageCurrency{Code: clean.CurrencyCode, Name: clean.CurrencyName,
			Symbol: clean.CurrencySymbol},
		CurrencyNameKey:      currencyKey,
		FoundedByGroupChatID: draft.ChatID, FoundedByBotID: draft.BotID, GroupLanguage: draft.Language,
		FoundedAt: now, ProtectedUntil: now.Add(h.protectionWindow), Buildings: buildings,
	}
	founded, err := tx.Settlements().Found(ctx, f)
	if err != nil {
		return err // ErrSpawnCellTaken retries; the others become a refusal
	}

	if _, _, err := application.FoundOffice(ctx, tx, "village_head", founded.JurisdictionID, 1, p.ID, now); err != nil {
		return err
	}
	// The founder lives in the village they just founded, from this very
	// transaction: the head of a village is its first resident (everyone else
	// joins with settlement.join). Their home moves off Support; their
	// property, company and job there keep working (residence is not location).
	if _, err := moveResidence(ctx, tx, meta, p.ID, founded.CityID, now, "founding"); err != nil {
		return err
	}
	if err := h.appendFoundedEvent(ctx, tx, meta, founded, draft, p.DisplayName, world.BiomeCode(cand.CellID),
		wsettle.NearbyFeature(world, cand.CellID), rules.EmblemText(clean.Emblem), cand.LatDeg, cand.LonDeg); err != nil {
		return err
	}
	if err := h.grantFoundingKit(ctx, tx, founded.CityID, world.BiomeCode(cand.CellID), now); err != nil {
		return err
	}
	if err := h.grantTreasury(ctx, tx, founded.CityID, now); err != nil {
		return err
	}
	if err := tx.Settlements().MarkDraftSubmitted(ctx, draft.ID, founded.CityID, now); err != nil {
		return err
	}
	out.founded, out.cell = &founded, cand.CellID
	return nil
}

func hasField(problems []wsettle.Problem, field string) bool {
	for _, p := range problems {
		if p.Field == field {
			return true
		}
	}
	return false
}

// appendFoundedEvent writes settlement.founded to the outbox (ADR 0028
// section 9.2), in the same transaction as the founding itself. It carries
// what the notifier needs to announce the village in the group that founded it
// — the group, its bot and language, the founder, the emblem and the currency —
// so the announcement is written from the event alone.
func (h *SettlementsHandler) appendFoundedEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	out application.FoundedSettlement, draft application.FoundingDraft, founder, biome string, feature wsettle.Name,
	emblemText string, latDeg, lonDeg float64,
) error {
	codes := make([]string, len(out.Buildings))
	for i, b := range out.Buildings {
		codes[i] = b.TypeCode
	}
	ev, err := events.New("settlement.founded", "settlement", out.CityID, map[string]any{
		"settlement_id":   out.CityID,
		"code":            out.Code,
		"name":            out.Name,
		"tier":            out.Tier,
		"jurisdiction_id": out.JurisdictionID,
		"world_cell_id":   out.WorldCellID,
		"lat_deg":         latDeg,
		"lon_deg":         lonDeg,
		"founded_at":      out.FoundedAt,
		"protected_until": out.ProtectedUntil,
		"chat_id":         draft.ChatID,
		"bot_id":          draft.BotID,
		"language":        draft.Language,
		"founder_id":      draft.FounderPlayerID,
		"founder_name":    founder,
		"emblem": map[string]string{"shape": out.Emblem.Shape, "color_a": out.Emblem.ColorA,
			"color_b": out.Emblem.ColorB, "icon": out.Emblem.Icon, "text": emblemText},
		"motto": out.Motto,
		"currency": map[string]string{"code": out.Currency.Code, "name": out.Currency.Name,
			"symbol": out.Currency.Symbol},
		"biome_code":     biome,
		"feature_latin":  feature.Latin,
		"feature_fa":     feature.Persian,
		"building_codes": codes,
	})
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("settlement", "founded"), Metadata: meta, Payload: ev.Payload,
	})
}
