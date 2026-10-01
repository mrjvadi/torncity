package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// The readers of a projection: each reads one kind of the player's current
// state, on the projection's transaction, as neutral data. They only read:
// nothing is caught up (energy, needs) by projecting; a value is projected
// as stored, with what a client needs to count its regen.

// read is every entity of the kinds asked.
func (s *StateSync) read(ctx context.Context, q querier, playerID string, kinds statesync.KindSet,
	heldNotices map[string]json.RawMessage,
) ([]statesync.Entity, error) {
	var out []statesync.Entity
	add := func(kind, id string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("postgres: statesync: encoding %s/%s: %w", kind, id, err)
		}
		out = append(out, statesync.Entity{Kind: kind, ID: id, Data: raw})
		return nil
	}
	exists, err := s.readPlayer(ctx, q, playerID, kinds, add)
	if err != nil || !exists {
		// A player that does not exist has no state to project.
		return nil, err
	}
	steps := []struct {
		kind string
		fn   func(context.Context, querier, string, func(string, string, any) error) error
	}{
		{statesync.KindVitals, s.readVitals},
		{statesync.KindWallet, s.readWallets},
		{statesync.KindInventory, s.readInventory},
		{statesync.KindSkill, s.readSkills},
		{statesync.KindTimedAction, s.readTimedActions},
		{statesync.KindLocation, s.readLocation},
		{statesync.KindRelations, s.readRelations},
	}
	for _, st := range steps {
		if !kinds.Has(st.kind) {
			continue
		}
		if err := st.fn(ctx, q, playerID, add); err != nil {
			return nil, err
		}
	}
	if kinds.Has(statesync.KindInbox) || kinds.Has(statesync.KindNotice) {
		if err := s.readNotices(ctx, q, playerID, kinds, heldNotices, add); err != nil {
			return nil, err
		}
	}
	if kinds.Has(statesync.KindResidence) || kinds.Has(statesync.KindSettlement) {
		if err := s.readSettlements(ctx, q, playerID, kinds, add); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// placeholderName is the name a record gets when none was on hand
// (handlers.fallbackDisplayName): an identifier, never shown as a name.
func placeholderName(telegramUserID int64) string {
	return "player-" + strconv.FormatInt(telegramUserID, 10)
}

func (s *StateSync) readPlayer(ctx context.Context, q querier, playerID string, kinds statesync.KindSet,
	add func(string, string, any) error,
) (bool, error) {
	var (
		d     statesync.PlayerData
		tg    int64
		level *int
		xp    *int64
		rank  *string
	)
	err := q.QueryRow(ctx, `
SELECT p.display_name, p.public_code, p.language, p.status, p.telegram_user_id, s.level, s.xp, l.rank
  FROM players p
  LEFT JOIN player_stats s ON s.player_id = p.id
  LEFT JOIN player_life l ON l.player_id = p.id
 WHERE p.id = $1::uuid`, playerID).Scan(&d.Name, &d.Code, &d.Lang, &d.Status, &tg, &level, &xp, &rank)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres: statesync: reading player %s: %w", playerID, err)
	}
	if !kinds.Has(statesync.KindPlayer) {
		return true, nil
	}
	if d.Name == placeholderName(tg) {
		d.Name = ""
	}
	d.Level = 1
	if level != nil {
		d.Level = *level
	}
	if xp != nil {
		d.XP = *xp
	}
	if rank != nil {
		d.Rank = *rank
	}
	if d.Level < player.MaxLevel {
		d.NextLevelXP = player.XPForLevel(d.Level + 1)
	}
	return true, add(statesync.KindPlayer, playerID, d)
}

func (s *StateSync) readVitals(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	var (
		energy, maxEnergy, health, maxHealth, regenBPS *int
		statsAt                                        *time.Time
		nerve                                          *int
		nerveAt                                        *time.Time
	)
	err := q.QueryRow(ctx, `
SELECT s.energy, s.max_energy, s.health, s.max_health, s.regen_bps, s.updated_at, c.nerve, c.nerve_updated_at
  FROM players p
  LEFT JOIN player_stats s ON s.player_id = p.id
  LEFT JOIN criminal_profiles c ON c.player_id = p.id
 WHERE p.id = $1::uuid`, playerID).Scan(&energy, &maxEnergy, &health, &maxHealth, &regenBPS, &statsAt, &nerve, &nerveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the vitals of %s: %w", playerID, err)
	}
	if energy == nil {
		// No stats row yet: the player has not played; their first command
		// writes it and the next projection carries it.
		return nil
	}
	utc := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	bps := 10000
	if regenBPS != nil && *regenBPS > 0 && *regenBPS <= 10000 {
		bps = *regenBPS
	}
	d := statesync.VitalsData{
		Energy: statesync.Meter{Value: *energy, Max: *maxEnergy, AsOf: utc(statsAt), Regen: &statesync.Regen{
			Amount: s.Rules.EnergyRegenAmount, EverySeconds: int64(s.Rules.EnergyRegenInterval / time.Second), BPS: bps}},
		Health: statesync.Meter{Value: *health, Max: *maxHealth, AsOf: utc(statsAt)},
		Nerve: statesync.Meter{Value: s.Rules.NerveMax, Max: s.Rules.NerveMax, Regen: &statesync.Regen{
			Amount: s.Rules.NerveRegenAmount, EverySeconds: int64(s.Rules.NerveRegenInterval / time.Second), BPS: 10000}},
	}
	if nerve != nil {
		d.Nerve.Value = min(max(*nerve, 0), s.Rules.NerveMax)
		d.Nerve.AsOf = utc(nerveAt)
	}
	return add(statesync.KindVitals, playerID, d)
}

func (s *StateSync) readWallets(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	rows, err := q.Query(ctx, `
SELECT a.currency, a.kind, SUM(a.balance), COALESCE(bool_or(c.is_premium), false)
  FROM accounts a LEFT JOIN currencies c ON c.code = a.currency
 WHERE a.owner_id = $1::uuid AND a.kind IN ('player_cash', 'player_bank')
 GROUP BY a.currency, a.kind`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the wallets of %s: %w", playerID, err)
	}
	defer rows.Close()
	wallets := map[string]*statesync.WalletData{}
	for rows.Next() {
		var (
			cur, kind string
			bal       int64
			premium   bool
		)
		if err := rows.Scan(&cur, &kind, &bal, &premium); err != nil {
			return err
		}
		w := wallets[cur]
		if w == nil {
			w = &statesync.WalletData{Currency: cur, Premium: premium}
			wallets[cur] = w
		}
		if kind == string(application.AccountPlayerCash) {
			w.Cash = bal
		} else {
			w.Bank = bal
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, cur := range sortedKeys(wallets) {
		if err := add(statesync.KindWallet, cur, wallets[cur]); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateSync) readInventory(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	items := map[string]*statesync.InventoryData{}
	get := func(code string) *statesync.InventoryData {
		it := items[code]
		if it == nil {
			it = &statesync.InventoryData{Item: code, Holdings: map[string]int64{}, Pieces: []statesync.PieceData{}}
			items[code] = it
		}
		return it
	}
	rows, err := q.Query(ctx, `SELECT item_code, holding, quantity FROM item_stacks WHERE player_id = $1::uuid AND quantity > 0`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the stacks of %s: %w", playerID, err)
	}
	for rows.Next() {
		var (
			code, holding string
			qty           int64
		)
		if err := rows.Scan(&code, &holding, &qty); err != nil {
			rows.Close()
			return err
		}
		it := get(code)
		it.Qty += qty
		it.Holdings[holding] += qty
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = q.Query(ctx, `SELECT id::text, item_code, holding, quality, uses_left FROM item_pieces
 WHERE owner_id = $1::uuid AND gone_at IS NULL ORDER BY id`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the pieces of %s: %w", playerID, err)
	}
	for rows.Next() {
		var (
			p    statesync.PieceData
			code string
		)
		if err := rows.Scan(&p.ID, &code, &p.Holding, &p.Quality, &p.UsesLeft); err != nil {
			rows.Close()
			return err
		}
		it := get(code)
		it.Qty++
		it.Holdings[p.Holding]++
		it.Pieces = append(it.Pieces, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, code := range sortedKeys(items) {
		if err := add(statesync.KindInventory, code, items[code]); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateSync) readSkills(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	rows, err := q.Query(ctx, `SELECT skill_code, level, xp FROM player_skills WHERE player_id = $1::uuid ORDER BY skill_code`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the skills of %s: %w", playerID, err)
	}
	var list []statesync.SkillData
	for rows.Next() {
		var d statesync.SkillData
		if err := rows.Scan(&d.Skill, &d.Level, &d.XP); err != nil {
			rows.Close()
			return err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range list {
		if err := add(statesync.KindSkill, d.Skill, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateSync) readTimedActions(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	rows, err := q.Query(ctx, `
SELECT id::text, action_type, COALESCE(reference_type, ''), COALESCE(reference_id::text, ''), status, started_at, finish_at
  FROM game_actions
 WHERE actor_id = $1::uuid AND actor_type = 'player' AND status IN ('scheduled', 'running')
 ORDER BY id`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the timed actions of %s: %w", playerID, err)
	}
	type row struct {
		id string
		d  statesync.TimedActionData
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.d.Kind, &r.d.RefType, &r.d.RefID, &r.d.State, &r.d.StartedAt, &r.d.FinishAt); err != nil {
			rows.Close()
			return err
		}
		r.d.StartedAt, r.d.FinishAt = r.d.StartedAt.UTC(), r.d.FinishAt.UTC()
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		if err := add(statesync.KindTimedAction, r.id, r.d); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateSync) readLocation(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	var (
		d      statesync.LocationData
		city   *string
		origin *string
		cityID *string
		place  *string
	)
	err := q.QueryRow(ctx, `
SELECT c.code, c.origin, c.id::text, p.place_code
  FROM players p LEFT JOIN cities c ON c.id = p.city_id
 WHERE p.id = $1::uuid`, playerID).Scan(&city, &origin, &cityID, &place)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the location of %s: %w", playerID, err)
	}
	if city != nil {
		d.City = *city
		if origin != nil && *origin == "founded" && cityID != nil {
			d.Settlement = *cityID
		}
	}
	if place != nil {
		d.Place = *place
	}
	var t statesync.TravelData
	err = q.QueryRow(ctx, `
SELECT fc.code, tc.code, t.mode, t.departed_at, t.arrives_at
  FROM travels t JOIN cities fc ON fc.id = t.from_city_id JOIN cities tc ON tc.id = t.to_city_id
 WHERE t.player_id = $1::uuid AND t.status = 'in_transit'`, playerID).Scan(&t.From, &t.To, &t.Mode, &t.DepartedAt, &t.ArrivesAt)
	switch {
	case err == nil:
		t.DepartedAt, t.ArrivesAt = t.DepartedAt.UTC(), t.ArrivesAt.UTC()
		d.Travel = &t
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("postgres: statesync: reading the journey of %s: %w", playerID, err)
	}
	var w statesync.WalkData
	err = q.QueryRow(ctx, `SELECT from_place, to_place, started_at, arrives_at FROM place_moves
 WHERE player_id = $1::uuid AND status = 'moving'`, playerID).Scan(&w.From, &w.To, &w.StartedAt, &w.ArrivesAt)
	switch {
	case err == nil:
		w.StartedAt, w.ArrivesAt = w.StartedAt.UTC(), w.ArrivesAt.UTC()
		d.Walk = &w
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("postgres: statesync: reading the walk of %s: %w", playerID, err)
	}
	return add(statesync.KindLocation, statesync.SelfID, d)
}

// readNotices reads the inbox count and the latest notices. A notice held
// already is re-used from what was held when only its read flag is asked
// for again: its view never changes, so it is read in full only once.
func (s *StateSync) readNotices(ctx context.Context, q querier, playerID string, kinds statesync.KindSet,
	held map[string]json.RawMessage, add func(string, string, any) error,
) error {
	var unread int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM player_notifications WHERE player_id = $1::uuid AND read_at IS NULL`,
		playerID).Scan(&unread); err != nil {
		return fmt.Errorf("postgres: statesync: counting the unread of %s: %w", playerID, err)
	}
	rows, err := q.Query(ctx, `SELECT id::text, read_at IS NOT NULL FROM player_notifications
 WHERE player_id = $1::uuid ORDER BY created_at DESC, id DESC LIMIT $2`, playerID, s.Rules.NoticesKept)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the notices of %s: %w", playerID, err)
	}
	type head struct {
		id   string
		read bool
	}
	var heads []head
	for rows.Next() {
		var h head
		if err := rows.Scan(&h.id, &h.read); err != nil {
			rows.Close()
			return err
		}
		heads = append(heads, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	inbox := statesync.InboxData{Unread: unread, Latest: make([]string, 0, len(heads))}
	for _, h := range heads {
		inbox.Latest = append(inbox.Latest, h.id)
	}
	if kinds.Has(statesync.KindInbox) {
		if err := add(statesync.KindInbox, statesync.SelfID, inbox); err != nil {
			return err
		}
	}
	if !kinds.Has(statesync.KindNotice) {
		return nil
	}
	var missing []string
	known := map[string]statesync.NoticeData{}
	for _, h := range heads {
		if raw, ok := held[h.id]; ok {
			var d statesync.NoticeData
			if json.Unmarshal(raw, &d) == nil {
				d.Read = h.read
				known[h.id] = d
				continue
			}
		}
		missing = append(missing, h.id)
	}
	if len(missing) > 0 {
		rows, err := q.Query(ctx, `SELECT id::text, kind, category, screen, view, created_at, read_at IS NOT NULL
  FROM player_notifications WHERE id = ANY($1::uuid[])`, missing)
		if err != nil {
			return fmt.Errorf("postgres: statesync: reading notices of %s: %w", playerID, err)
		}
		for rows.Next() {
			var (
				id   string
				d    statesync.NoticeData
				view []byte
			)
			if err := rows.Scan(&id, &d.Kind, &d.Category, &d.Screen, &view, &d.CreatedAt, &d.Read); err != nil {
				rows.Close()
				return err
			}
			if len(view) > 0 {
				d.View = json.RawMessage(view)
			}
			d.CreatedAt = d.CreatedAt.UTC()
			known[id] = d
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for _, h := range heads {
		if d, ok := known[h.id]; ok {
			if err := add(statesync.KindNotice, h.id, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// readSettlements reads the residence and the summaries of the player's
// settlement and of the founded settlement they stand in, each cut for this
// player's kind of viewer.
func (s *StateSync) readSettlements(ctx context.Context, q querier, playerID string, kinds statesync.KindSet,
	add func(string, string, any) error,
) error {
	repo := &SettlementRepository{q: q}
	mine, err := repo.ByPlayer(ctx, playerID)
	switch {
	case errors.Is(err, application.ErrCityNotFound):
		mine = application.PlayerSettlement{}
	case err != nil:
		return err
	}
	isHead := false
	if mine.CityID != "" {
		head := settlement.HeadOffice(mine.Tier)
		for _, o := range mine.Offices {
			if o == head {
				isHead = true
			}
		}
		if kinds.Has(statesync.KindResidence) {
			if err := add(statesync.KindResidence, statesync.SelfID, statesync.ResidenceData{
				Settlement: mine.CityID, Code: mine.Code, Name: mine.Name, Tier: mine.Tier,
				IsHead: isHead, Resident: mine.Resident,
			}); err != nil {
				return err
			}
		}
	}
	if !kinds.Has(statesync.KindSettlement) {
		return nil
	}
	if mine.CityID != "" {
		viewer := statesync.ViewerMember
		if isHead {
			viewer = statesync.ViewerHead
		}
		if err := s.addSettlement(ctx, q, mine.CityID, viewer, add); err != nil {
			return err
		}
	}
	var here *string
	if err := q.QueryRow(ctx, `SELECT c.id::text FROM players p JOIN cities c ON c.id = p.city_id
 WHERE p.id = $1::uuid AND c.origin = 'founded'`, playerID).Scan(&here); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: statesync: reading where %s stands: %w", playerID, err)
	}
	if here != nil && *here != mine.CityID {
		return s.addSettlement(ctx, q, *here, statesync.ViewerPublic, add)
	}
	return nil
}

func (s *StateSync) addSettlement(ctx context.Context, q querier, id, viewer string, add func(string, string, any) error) error {
	d := statesync.SettlementData{ID: id, Viewer: viewer}
	var growth int
	err := q.QueryRow(ctx, `SELECT code, name, COALESCE(tier, ''), grid_growth FROM cities WHERE id = $1::uuid`, id).
		Scan(&d.Code, &d.Name, &d.Tier, &growth)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading settlement %s: %w", id, err)
	}
	if s.Layouts != nil {
		v, lots, err := s.Layouts.LayoutVersions(ctx, id)
		switch {
		case errors.Is(err, application.ErrCityNotFound):
		case err != nil:
			return fmt.Errorf("postgres: statesync: the layout versions of %s: %w", id, err)
		default:
			d.GridLots = lots
			switch viewer {
			case statesync.ViewerHead:
				d.LayoutVersion = v.Head
			case statesync.ViewerMember:
				d.LayoutVersion = v.Member
			default:
				d.LayoutVersion = v.Public
			}
		}
	}
	if viewer != statesync.ViewerPublic {
		var t statesync.TreasuryData
		err := q.QueryRow(ctx, `SELECT a.currency, a.balance FROM cities c JOIN accounts a ON a.id = c.treasury_account_id
 WHERE c.id = $1::uuid`, id).Scan(&t.Currency, &t.Balance)
		switch {
		case err == nil:
			d.Treasury = &t
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("postgres: statesync: reading the treasury of %s: %w", id, err)
		}
		if err := q.QueryRow(ctx, `SELECT count(*) FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`, id).
			Scan(&d.Knowledge); err != nil {
			return fmt.Errorf("postgres: statesync: counting the knowledge of %s: %w", id, err)
		}
		var r statesync.ResearchData
		err = q.QueryRow(ctx, `SELECT code, finish_at FROM settlement_research
 WHERE settlement_id = $1::uuid AND status = 'running' ORDER BY started_at DESC LIMIT 1`, id).Scan(&r.Code, &r.FinishAt)
		switch {
		case err == nil:
			r.FinishAt = r.FinishAt.UTC()
			d.Research = &r
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("postgres: statesync: reading the research of %s: %w", id, err)
		}
	}
	return add(statesync.KindSettlement, id, d)
}

func (s *StateSync) readRelations(ctx context.Context, q querier, playerID string, add func(string, string, any) error) error {
	d := statesync.RelationsData{Friends: []string{}}
	rows, err := q.Query(ctx, `SELECT friend_player_id::text FROM friendships
 WHERE player_id = $1::uuid AND status = 'accepted' ORDER BY friend_player_id`, playerID)
	if err != nil {
		return fmt.Errorf("postgres: statesync: reading the friends of %s: %w", playerID, err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		d.Friends = append(d.Friends, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var f statesync.FactionData
	err = q.QueryRow(ctx, `SELECT faction_id::text, rank FROM faction_members WHERE player_id = $1::uuid`, playerID).Scan(&f.ID, &f.Rank)
	switch {
	case err == nil:
		d.Faction = &f
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("postgres: statesync: reading the faction of %s: %w", playerID, err)
	}
	if err := q.QueryRow(ctx, `SELECT presence_visibility FROM players WHERE id = $1::uuid`, playerID).Scan(&d.Presence); err != nil {
		return fmt.Errorf("postgres: statesync: reading the presence setting of %s: %w", playerID, err)
	}
	return add(statesync.KindRelations, statesync.SelfID, d)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
