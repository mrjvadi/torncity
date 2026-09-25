package panel

// Players and what they hold: accounts and the ledger, items, property,
// work and study, and their life in the game.

// Shared pieces of the statements.
const (
	// accountJoins and accountOwner name an account's owner as kind:code.
	accountJoins = `
	LEFT JOIN players op ON op.id = a.owner_id AND a.kind IN ('player_cash', 'player_bank', 'player_escrow', 'player_savings')
	LEFT JOIN companies oc ON oc.id = a.owner_id AND a.kind = 'company_treasury'
	LEFT JOIN cities oci ON oci.id = a.owner_id AND a.kind = 'city_treasury'
	LEFT JOIN factions ofa ON ofa.id = a.owner_id AND a.kind = 'faction_treasury'
	LEFT JOIN jurisdictions oj ON oj.id = a.owner_id AND a.kind IN ('state_treasury', 'defence_fund', 'national_bank', 'insurance_fund')`
	accountOwner = `COALESCE('player:' || op.public_code, 'company:' || oc.code, 'city:' || oci.code,
	'faction:' || ofa.code, 'country:' || oj.code, 'system:' || a.kind)`

	// sameOwner compares a statement's owner uuid with a resolved scope.
	ownerIs = `v.owner_uuid = @::uuid`
)

func init() {
	register(
		View{Name: "players", SQL: `SELECT p.id AS player_uuid, p.public_code AS code, p.display_name AS name,
			COALESCE(p.username, '') AS username, p.telegram_user_id AS telegram_id, p.status, p.language,
			c.code AS city, rc.code AS residence, p.place_code AS place, ps.level, pl.rank, pl.net_worth,
			(SELECT balance FROM accounts WHERE kind = 'player_cash' AND owner_id = p.id) AS cash,
			(SELECT balance FROM accounts WHERE kind = 'player_bank' AND owner_id = p.id) AS bank,
			EXISTS (SELECT 1 FROM player_moderation m WHERE m.player_id = p.id AND m.lifted_at IS NULL
				AND (m.ends_at IS NULL OR m.ends_at > now())) AS moderated,
			p.created_at, p.last_active_at, p.city_id AS here_uuid, p.residence_city_id AS home_uuid
			FROM players p LEFT JOIN cities c ON c.id = p.city_id LEFT JOIN cities rc ON rc.id = p.residence_city_id
			LEFT JOIN player_stats ps ON ps.player_id = p.id LEFT JOIN player_life pl ON pl.player_id = p.id`,
			Cols: cols("code:player", "name:text", "username:text", "telegram_id:code", "status:status", "language:code",
				"city:city", "residence:city", "place:code", "level:int", "rank:code", "net_worth:money", "cash:money",
				"bank:money", "moderated:bool", "created_at:time", "last_active_at:time"),
			Scopes:  map[string]string{"city": `v.home_uuid = @::uuid`, "here": `v.here_uuid = @::uuid`},
			Filters: []Filter{opts("status", "active", "banned", "deleted"), opts("language", "fa", "en"), free("rank"), opts("moderated", "true", "false")},
			Search:  []string{"code", "name", "username", "telegram_id"},
			Order:   "last_active_at", Tie: "code", Time: "created_at"},

		View{Name: "accounts", SQL: `SELECT a.id AS account_uuid, a.id::text AS account, a.kind, ` + accountOwner + ` AS owner,
			a.balance, a.currency, a.created_at, a.owner_id AS owner_uuid
			FROM accounts a` + accountJoins,
			Cols: cols("account:id", "kind:enum", "owner:ref", "balance:money", "currency:code", "created_at:time"),
			Scopes: map[string]string{"player": ownerIs, "company": ownerIs, "city": ownerIs, "country": ownerIs,
				"faction": ownerIs, "account": `v.account_uuid = @::uuid`},
			Filters: []Filter{opts("kind", "player_cash", "player_bank", "player_escrow", "player_savings", "company_treasury",
				"faction_treasury", "city_treasury", "state_treasury", "defence_fund", "national_bank", "insurance_fund",
				"system_source", "system_sink")},
			Search: []string{"owner", "account"}, Order: "balance", Tie: "account"},

		View{Name: "ledger", SQL: `SELECT le.id::text AS id, le.created_at, le.transaction_id::text AS tx, a.kind AS account_kind,
			` + accountOwner + ` AS owner, le.amount, le.reason, COALESCE(le.reference_type, '') AS reference_type,
			le.reference_id::text AS reference, le.account_id::text AS account,
			le.transaction_id AS tx_uuid, le.account_id AS account_uuid, a.owner_id AS owner_uuid, le.reference_id AS reference_uuid
			FROM ledger_entries le JOIN accounts a ON a.id = le.account_id` + accountJoins,
			Cols: cols("created_at:time", "tx:id", "account_kind:enum", "owner:ref", "amount:money", "reason:enum",
				"reference_type:code", "reference:id", "account:id"),
			Scopes: map[string]string{"tx": `v.tx_uuid = @::uuid`, "account": `v.account_uuid = @::uuid`,
				"player": ownerIs, "company": ownerIs, "city": ownerIs, "country": ownerIs, "faction": ownerIs,
				"reference": `v.reference_uuid = @::uuid`},
			Filters: []Filter{free("reason"), opts("account_kind", "player_cash", "player_bank", "player_escrow", "player_savings",
				"company_treasury", "faction_treasury", "city_treasury", "state_treasury", "defence_fund", "national_bank",
				"insurance_fund", "system_source", "system_sink"), free("reference_type")},
			Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "ledger.reasons", SQL: `SELECT le.reason,
			COALESCE(SUM(-le.amount) FILTER (WHERE le.account_id = '00000000-0000-4000-8000-000000000001'::uuid), 0)::bigint AS minted,
			COALESCE(SUM(le.amount) FILTER (WHERE le.account_id = '00000000-0000-4000-8000-000000000002'::uuid), 0)::bigint AS burned,
			COALESCE(SUM(le.amount) FILTER (WHERE le.amount > 0), 0)::bigint AS moved,
			count(DISTINCT le.transaction_id) AS transactions
			FROM ledger_entries le WHERE true /*window*/ GROUP BY le.reason`,
			Cols:   cols("reason:enum", "minted:money", "burned:money", "moved:money", "transactions:int"),
			Search: []string{"reason"}, Order: "moved", Tie: "reason", Window: "le.created_at", WindowDays: 7},

		View{Name: "inventory", SQL: `SELECT s.player_id AS player_uuid, p.public_code AS player, s.item_code AS item, s.holding, s.quantity
			FROM item_stacks s JOIN players p ON p.id = s.player_id`,
			Cols:   cols("player:player", "item:item", "holding:status", "quantity:int"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Need: []string{"player"},
			Filters: []Filter{free("item"), opts("holding", "carried", "escrow")},
			Search:  []string{"item"}, Order: "quantity", Tie: "item"},

		View{Name: "warehouse", SQL: `SELECT o.org_id AS org_uuid, COALESCE('company:' || c.code, 'country:' || j.code) AS owner,
			o.item_code AS item, o.holding, o.quantity
			FROM org_stacks o LEFT JOIN companies c ON o.org_kind = 'company' AND c.id = o.org_id
			LEFT JOIN jurisdictions j ON o.org_kind = 'state' AND j.id = o.org_id`,
			Cols:    cols("owner:ref", "item:item", "holding:status", "quantity:int"),
			Scopes:  map[string]string{"company": `v.org_uuid = @::uuid`, "country": `v.org_uuid = @::uuid`},
			Filters: []Filter{free("item"), opts("holding", "warehouse", "listed")},
			Search:  []string{"item", "owner"}, Order: "quantity", Tie: "item"},

		View{Name: "pieces", SQL: `SELECT i.id AS piece_uuid, i.serial, i.item_code AS item, i.archetype, i.quality, i.uses_left,
			COALESCE('player:' || op.public_code, 'company:' || c.code, 'country:' || j.code) AS owner, i.holding, i.origin,
			d.no AS design, i.created_at, i.gone_at, i.owner_id AS owner_uuid, i.org_id AS org_uuid, i.design_id AS design_uuid
			FROM item_pieces i LEFT JOIN players op ON op.id = i.owner_id
			LEFT JOIN companies c ON i.org_kind = 'company' AND c.id = i.org_id
			LEFT JOIN jurisdictions j ON i.org_kind = 'state' AND j.id = i.org_id
			LEFT JOIN product_designs d ON d.id = i.design_id`,
			Cols: cols("serial:code", "item:item", "archetype:code", "quality:int", "uses_left:int", "owner:ref",
				"holding:status", "origin:status", "design:int", "created_at:time", "gone_at:time"),
			Scopes: map[string]string{"player": ownerIs, "company": `v.org_uuid = @::uuid`, "country": `v.org_uuid = @::uuid`,
				"design": `v.design_uuid = @::uuid`, "piece": `v.piece_uuid = @::uuid`},
			Filters: []Filter{free("item"), opts("holding", "carried", "escrow", "gone", "warehouse", "listed"),
				opts("origin", "supply", "loot", "grant", "production"), free("archetype")},
			Search: []string{"serial", "item"}, Order: "created_at", Tie: "serial", Time: "created_at"},

		View{Name: "movements", SQL: `SELECT m.id::text AS id, m.created_at, m.item_code AS item, ip.serial, m.quantity,
			COALESCE('player:' || fp.public_code, 'company:' || fc.code, 'country:' || fj.code) AS from_ref, m.from_holding,
			COALESCE('player:' || tp.public_code, 'company:' || tc.code, 'country:' || tj.code) AS to_ref, m.to_holding,
			m.reason, COALESCE(m.reference_type, '') AS reference_type, m.reference_id::text AS reference,
			m.piece_id AS piece_uuid, m.from_player AS from_uuid, m.to_player AS to_uuid, m.from_org AS from_org_uuid,
			m.to_org AS to_org_uuid
			FROM item_movements m LEFT JOIN item_pieces ip ON ip.id = m.piece_id
			LEFT JOIN players fp ON fp.id = m.from_player LEFT JOIN players tp ON tp.id = m.to_player
			LEFT JOIN companies fc ON m.from_org_kind = 'company' AND fc.id = m.from_org
			LEFT JOIN companies tc ON m.to_org_kind = 'company' AND tc.id = m.to_org
			LEFT JOIN jurisdictions fj ON m.from_org_kind = 'state' AND fj.id = m.from_org
			LEFT JOIN jurisdictions tj ON m.to_org_kind = 'state' AND tj.id = m.to_org`,
			Cols: cols("created_at:time", "item:item", "serial:code", "quantity:int", "from_ref:ref", "from_holding:code",
				"to_ref:ref", "to_holding:code", "reason:code", "reference_type:code", "reference:id"),
			Scopes: map[string]string{"piece": `v.piece_uuid = @::uuid`,
				"player":  `(v.from_uuid = @::uuid OR v.to_uuid = @::uuid)`,
				"company": `(v.from_org_uuid = @::uuid OR v.to_org_uuid = @::uuid)`,
				"country": `(v.from_org_uuid = @::uuid OR v.to_org_uuid = @::uuid)`},
			Need:    []string{"piece", "player", "company", "country"},
			Filters: []Filter{free("reason"), free("item")},
			Order:   "created_at", Tie: "id", Time: "created_at"},

		View{Name: "properties", SQL: `SELECT pr.id AS property_uuid, pr.no, c.code AS city, pr.type_code AS type,
			p.public_code AS owner, pr.status, pr.value, pr.tax_debt, pr.upkeep_debt, pr.unpaid_periods,
			(SELECT l.rent FROM property_leases l WHERE l.property_id = pr.id AND l.status = 'active') AS rent,
			(SELECT t.public_code FROM property_leases l JOIN players t ON t.id = l.tenant_player_id
				WHERE l.property_id = pr.id AND l.status = 'active') AS tenant,
			pr.acquired_at, pr.repossessed_at, pr.owner_player_id AS owner_uuid, pr.city_id AS city_uuid
			FROM properties pr JOIN cities c ON c.id = pr.city_id LEFT JOIN players p ON p.id = pr.owner_player_id`,
			Cols: cols("no:int", "city:city", "type:code", "owner:player", "status:status", "value:money", "tax_debt:money",
				"upkeep_debt:money", "unpaid_periods:int", "rent:money", "tenant:player", "acquired_at:time", "repossessed_at:time"),
			Scopes:  map[string]string{"player": ownerIs, "city": `v.city_uuid = @::uuid`, "property": `v.property_uuid = @::uuid`},
			Filters: []Filter{opts("status", "owned", "repossessed"), free("type")},
			Search:  []string{"owner", "type"}, Order: "no", Tie: "no"},

		View{Name: "leases", SQL: `SELECT l.id::text AS id, l.no, pr.no AS property, c.code AS city, pl.public_code AS landlord,
			t.public_code AS tenant, l.rent, l.status, l.arrears, l.started_at, l.ended_at, COALESCE(l.end_reason, '') AS end_reason,
			l.landlord_player_id AS landlord_uuid, l.tenant_player_id AS tenant_uuid, pr.city_id AS city_uuid, pr.id AS property_uuid
			FROM property_leases l JOIN properties pr ON pr.id = l.property_id JOIN cities c ON c.id = pr.city_id
			JOIN players pl ON pl.id = l.landlord_player_id JOIN players t ON t.id = l.tenant_player_id`,
			Cols: cols("no:int", "property:int", "city:city", "landlord:player", "tenant:player", "rent:money", "status:status",
				"arrears:int", "started_at:time", "ended_at:time", "end_reason:status"),
			Scopes: map[string]string{"player": `(v.landlord_uuid = @::uuid OR v.tenant_uuid = @::uuid)`,
				"city": `v.city_uuid = @::uuid`, "property": `v.property_uuid = @::uuid`},
			Filters: []Filter{opts("status", "active", "ended")}, Order: "started_at", Tie: "id", Time: "started_at"},

		View{Name: "property.listings", SQL: `SELECT pl.no, pr.no AS property, c.code AS city, pr.type_code AS type,
			s.public_code AS seller, pl.kind, pl.price, pl.status, b.public_code AS buyer, pl.created_at, pl.closed_at,
			pl.seller_player_id AS seller_uuid, pl.buyer_player_id AS buyer_uuid, pr.city_id AS city_uuid, pr.id AS property_uuid
			FROM property_listings pl JOIN properties pr ON pr.id = pl.property_id JOIN cities c ON c.id = pr.city_id
			JOIN players s ON s.id = pl.seller_player_id LEFT JOIN players b ON b.id = pl.buyer_player_id`,
			Cols: cols("no:int", "property:int", "city:city", "type:code", "seller:player", "kind:status", "price:money",
				"status:status", "buyer:player", "created_at:time", "closed_at:time"),
			Scopes: map[string]string{"player": `(v.seller_uuid = @::uuid OR v.buyer_uuid = @::uuid)`,
				"city": `v.city_uuid = @::uuid`, "property": `v.property_uuid = @::uuid`},
			Filters: []Filter{opts("status", "open", "taken", "cancelled"), opts("kind", "sale", "rent")},
			Order:   "created_at", Tie: "no", Time: "created_at"},

		View{Name: "property.charges", SQL: `SELECT pc.period_no, pr.no AS property, c.code AS city, p.public_code AS owner,
			pc.upkeep, pc.tax, pc.upkeep_paid, pc.tax_paid, pc.upkeep_debt, pc.tax_debt, pc.foreclosed, pc.charged_at,
			pc.owner_player_id AS owner_uuid, pc.city_id AS city_uuid, pc.property_id AS property_uuid
			FROM property_charges pc JOIN properties pr ON pr.id = pc.property_id JOIN cities c ON c.id = pc.city_id
			LEFT JOIN players p ON p.id = pc.owner_player_id`,
			Cols: cols("period_no:int", "property:int", "city:city", "owner:player", "upkeep:money", "tax:money",
				"upkeep_paid:money", "tax_paid:money", "upkeep_debt:money", "tax_debt:money", "foreclosed:bool", "charged_at:time"),
			Scopes: map[string]string{"player": ownerIs, "city": `v.city_uuid = @::uuid`, "property": `v.property_uuid = @::uuid`},
			Need:   []string{"player", "city", "property"}, Order: "charged_at", Tie: "property", Time: "charged_at"},

		View{Name: "employments", SQL: `SELECT e.id::text AS id, p.public_code AS player, e.career_code AS career, c.code AS city,
			co.code AS company, e.tier, e.rate, e.performance, e.total_shifts, e.total_earned, e.hired_at, e.ended_at,
			COALESCE(e.end_reason, '') AS end_reason, CASE WHEN e.ended_at IS NULL THEN 'current' ELSE 'ended' END AS state,
			e.player_id AS player_uuid, e.company_id AS company_uuid, e.city_id AS city_uuid
			FROM employments e JOIN players p ON p.id = e.player_id LEFT JOIN cities c ON c.id = e.city_id
			LEFT JOIN companies co ON co.id = e.company_id`,
			Cols: cols("player:player", "career:code", "city:city", "company:company", "tier:int", "rate:money",
				"performance:int", "total_shifts:int", "total_earned:money", "hired_at:time", "ended_at:time",
				"end_reason:status", "state:status"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "company": `v.company_uuid = @::uuid`,
				"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("state", "current", "ended"), free("career")},
			Search:  []string{"player", "career"}, Order: "hired_at", Tie: "id", Time: "hired_at"},

		View{Name: "shifts", SQL: `SELECT w.id::text AS id, w.worked_at, p.public_code AS player, co.code AS company, w.tier,
			w.gross, w.tax, w.net, w.xp, w.performance_delta, w.fatigue_bps,
			w.player_id AS player_uuid, w.company_id AS company_uuid
			FROM work_shifts w JOIN players p ON p.id = w.player_id LEFT JOIN companies co ON co.id = w.company_id`,
			Cols: cols("worked_at:time", "player:player", "company:company", "tier:int", "gross:money", "tax:money",
				"net:money", "xp:int", "performance_delta:int", "fatigue_bps:bps"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "company": `v.company_uuid = @::uuid`},
			Need:   []string{"player", "company"}, Order: "worked_at", Tie: "id", Time: "worked_at"},

		View{Name: "shift.sessions", SQL: `SELECT s.id::text AS id, s.started_at, p.public_code AS player, co.code AS company,
			s.tier, s.status, s.ends_at, s.completed_at, s.energy_cost, s.fatigue_bps, s.wage_reserved,
			s.player_id AS player_uuid, s.company_id AS company_uuid
			FROM shift_sessions s JOIN players p ON p.id = s.player_id LEFT JOIN companies co ON co.id = s.company_id`,
			Cols: cols("started_at:time", "player:player", "company:company", "tier:int", "status:status", "ends_at:time",
				"completed_at:time", "energy_cost:int", "fatigue_bps:bps", "wage_reserved:money"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`, "company": `v.company_uuid = @::uuid`},
			Need:    []string{"player", "company"},
			Filters: []Filter{opts("status", "working", "completed", "abandoned")},
			Order:   "started_at", Tie: "id", Time: "started_at"},

		View{Name: "enrollments", SQL: `SELECT e.id::text AS id, p.public_code AS player, e.course_code AS course, e.status, e.fee,
			e.started_at, e.completes_at, e.completed_at, e.paused_at, e.player_id AS player_uuid
			FROM enrollments e JOIN players p ON p.id = e.player_id`,
			Cols: cols("player:player", "course:code", "status:status", "fee:money", "started_at:time", "completes_at:time",
				"completed_at:time", "paused_at:time"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("status", "in_progress", "completed", "abandoned"), free("course")},
			Search:  []string{"player", "course"}, Order: "started_at", Tie: "id", Time: "started_at"},

		View{Name: "certificates", SQL: `SELECT c.id::text AS id, p.public_code AS player, c.course_code AS course, c.issued_at,
			c.player_id AS player_uuid FROM certifications c JOIN players p ON p.id = c.player_id`,
			Cols:   cols("player:player", "course:code", "issued_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Filters: []Filter{free("course")},
			Order: "issued_at", Tie: "id", Time: "issued_at"},

		View{Name: "skills", SQL: `SELECT s.id::text AS id, p.public_code AS player, s.skill_code AS skill, s.level, s.xp, s.updated_at,
			s.player_id AS player_uuid FROM player_skills s JOIN players p ON p.id = s.player_id`,
			Cols:   cols("player:player", "skill:code", "level:int", "xp:int", "updated_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Filters: []Filter{free("skill")},
			Order: "level", Tie: "id"},

		View{Name: "friends", SQL: `SELECT f.id::text AS id, p.public_code AS player, o.public_code AS friend, f.status, f.created_at,
			f.player_id AS player_uuid, f.friend_player_id AS friend_uuid
			FROM friendships f JOIN players p ON p.id = f.player_id JOIN players o ON o.id = f.friend_player_id`,
			Cols:    cols("player:player", "friend:player", "status:status", "created_at:time"),
			Scopes:  map[string]string{"player": `(v.player_uuid = @::uuid OR v.friend_uuid = @::uuid)`},
			Need:    []string{"player"},
			Filters: []Filter{opts("status", "pending", "accepted", "blocked")}, Order: "created_at", Tie: "id"},

		View{Name: "life", SQL: `SELECT h.id::text AS id, h.at, p.public_code AS player, h.kind, h.public, h.backfilled, h.source,
			h.data, h.player_id AS player_uuid FROM life_history h JOIN players p ON p.id = h.player_id`,
			Cols:   cols("at:time", "player:player", "kind:status", "public:bool", "backfilled:bool", "source:code", "data:json"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("kind", "joined", "first_job", "hired", "promoted", "course", "certificate", "company_founded",
				"company_closed", "property_bought", "property_sold", "election_won", "election_lost", "office_taken", "office_lost",
				"jailed", "convicted", "hospitalised", "war_command", "achievement", "rank_up", "rank_down", "big_trade")},
			Order: "at", Tie: "id", Time: "at"},

		View{Name: "moderation", SQL: `SELECT m.no, p.public_code AS player, m.kind, m.reason, m.imposed_by, m.imposed_at, m.ends_at,
			COALESCE(m.lifted_by, '') AS lifted_by, m.lifted_at, COALESCE(m.lift_reason, '') AS lift_reason,
			CASE WHEN m.lifted_at IS NOT NULL THEN 'lifted' WHEN m.ends_at IS NOT NULL AND m.ends_at <= now() THEN 'expired'
				ELSE 'standing' END AS state, m.player_id AS player_uuid
			FROM player_moderation m JOIN players p ON p.id = m.player_id`,
			Cols: cols("no:int", "player:player", "kind:status", "reason:text", "imposed_by:text", "imposed_at:time",
				"ends_at:time", "lifted_by:text", "lifted_at:time", "lift_reason:text", "state:status"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("kind", "mute", "ban"), opts("state", "standing", "expired", "lifted")},
			Search:  []string{"player", "reason", "imposed_by"}, Order: "imposed_at", Tie: "no", Time: "imposed_at"},

		View{Name: "travels", SQL: `SELECT t.id::text AS id, t.departed_at, p.public_code AS player, f.code AS from_city,
			c.code AS to_city, t.mode, t.cost, t.status, t.arrives_at, t.game_action_id::text AS action,
			t.player_id AS player_uuid, t.from_city_id AS from_uuid, t.to_city_id AS to_uuid
			FROM travels t JOIN players p ON p.id = t.player_id JOIN cities f ON f.id = t.from_city_id
			JOIN cities c ON c.id = t.to_city_id`,
			Cols: cols("departed_at:time", "player:player", "from_city:city", "to_city:city", "mode:code", "cost:money",
				"status:status", "arrives_at:time", "action:id"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`,
				"city": `(v.from_uuid = @::uuid OR v.to_uuid = @::uuid)`},
			Filters: []Filter{opts("status", "in_transit", "arrived", "cancelled"), free("mode")},
			Order:   "departed_at", Tie: "id", Time: "departed_at"},

		View{Name: "walks", SQL: `SELECT m.id::text AS id, m.started_at, p.public_code AS player, c.code AS city, m.from_place,
			m.to_place, m.status, m.energy, m.arrives_at, m.arrived_at, m.game_action_id::text AS action, m.player_id AS player_uuid,
			m.city_id AS city_uuid
			FROM place_moves m JOIN players p ON p.id = m.player_id JOIN cities c ON c.id = m.city_id`,
			Cols: cols("started_at:time", "player:player", "city:city", "from_place:code", "to_place:code", "status:status",
				"energy:int", "arrives_at:time", "arrived_at:time", "action:id"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`, "city": `v.city_uuid = @::uuid`},
			Need:    []string{"player", "city"},
			Filters: []Filter{opts("status", "moving", "arrived", "cancelled")},
			Order:   "started_at", Tie: "id", Time: "started_at"},

		View{Name: "actions", SQL: `SELECT a.id::text AS id, a.action_type AS type, a.actor_type, p.public_code AS player,
			COALESCE(a.reference_type, '') AS reference_type, a.reference_id::text AS reference, a.status, a.retry_count,
			a.started_at, a.finish_at, a.completed_at, a.claimed_at, COALESCE(a.claimed_by, '') AS claimed_by,
			COALESCE(a.payload->>'last_error', '') AS last_error, a.actor_id AS actor_uuid, a.id AS action_uuid,
			a.reference_id AS reference_uuid
			FROM game_actions a LEFT JOIN players p ON a.actor_type = 'player' AND p.id = a.actor_id`,
			Cols: cols("id:id", "type:code", "actor_type:code", "player:player", "reference_type:code", "reference:id",
				"status:status", "retry_count:int", "started_at:time", "finish_at:time", "completed_at:time", "claimed_at:time",
				"claimed_by:text", "last_error:text"),
			Scopes: map[string]string{"player": `v.actor_uuid = @::uuid`, "action": `v.action_uuid = @::uuid`,
				"reference": `v.reference_uuid = @::uuid`},
			Need:    []string{"player", "action", "reference"},
			Filters: []Filter{opts("status", "scheduled", "running", "completed", "failed", "cancelled"), free("type")},
			Order:   "finish_at", Tie: "id", Time: "finish_at"},

		View{Name: "grants", SQL: `SELECT g.id::text AS id, g.created_at, p.public_code AS player, g.source, g.amount, g.quantity,
			g.granted_by, g.ledger_transaction_id::text AS tx, g.player_id AS player_uuid
			FROM reward_grants g JOIN players p ON p.id = g.player_id`,
			Cols: cols("created_at:time", "player:player", "source:status", "amount:money", "quantity:int", "granted_by:text",
				"tx:id"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("source", "mission", "event", "achievement", "admin", "starting_grant")},
			Search:  []string{"player", "granted_by"}, Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "bots.links", SQL: `SELECT l.id::text AS id, p.public_code AS player, b.bot_key AS bot, l.telegram_chat_id AS chat,
			l.is_reachable AS reachable, l.first_seen_at, l.last_seen_at, l.player_id AS player_uuid
			FROM player_bot_links l JOIN players p ON p.id = l.player_id JOIN telegram_bots b ON b.id = l.bot_id`,
			Cols:   cols("player:player", "bot:code", "chat:code", "reachable:bool", "first_seen_at:time", "last_seen_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Need: []string{"player"},
			Order: "last_seen_at", Tie: "id"},

		View{Name: "sleeps", SQL: `SELECT s.id::text AS id, s.slept_at, p.public_code AS player, s.spot, c.code AS city, s.price,
			COALESCE(s.method, '') AS method, s.player_id AS player_uuid
			FROM life_sleeps s JOIN players p ON p.id = s.player_id LEFT JOIN cities c ON c.id = s.city_id`,
			Cols:   cols("slept_at:time", "player:player", "spot:code", "city:city", "price:money", "method:code"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Need: []string{"player"},
			Order: "slept_at", Tie: "id", Time: "slept_at"},

		View{Name: "audit", SQL: `SELECT l.id, l.created_at, l.actor, l.action, l.target_type, l.target_id::text AS target,
			l.reason, l.new_value, l.old_value, l.target_id AS target_uuid, l.new_value->>'player' AS player_ref
			FROM audit_logs l`,
			Cols: cols("id:int", "created_at:time", "actor:text", "action:code", "target_type:code", "target:id",
				"reason:text", "new_value:json", "old_value:json"),
			Scopes: map[string]string{"player": `(v.target_uuid = @::uuid OR v.player_ref = (@::uuid)::text)`,
				"company": `v.target_uuid = @::uuid`},
			Filters: []Filter{{Key: "action", Prefix: true}, {Key: "actor", Prefix: true}, free("target_type")},
			Search:  []string{"actor", "action", "reason"}, Order: "id", Tie: "id", Time: "created_at"},
	)
}
