package panel

// Cities, countries, the armed forces and wars, diplomacy, and the offices,
// levers, proposals and elections that govern them.

// placeScopes are the scopes of a statement with a jurisdiction column:
// a city's own, a country's own, or any jurisdiction by kind:code.
func placeScopes(col string, more map[string]string) map[string]string {
	m := map[string]string{
		"city":         col + " = (SELECT jurisdiction_id FROM cities WHERE id = @::uuid)",
		"country":      col + " = @::uuid",
		"jurisdiction": col + " = @::uuid",
	}
	for k, v := range more {
		m[k] = v
	}
	return m
}

const countryIs = `v.country_uuid = @::uuid`

func init() {
	register(
		View{Name: "cities", SQL: `SELECT c.code, c.name, k.code AS country, c.population, c.tax_rate_bps, c.cost_of_living,
			(SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = c.id) AS treasury,
			(SELECT count(*) FROM players WHERE residence_city_id = c.id) AS residents,
			(SELECT count(*) FROM players WHERE city_id = c.id) AS present,
			(SELECT count(*) FROM companies WHERE city_id = c.id AND status = 'active') AS companies,
			(SELECT count(*) FROM properties WHERE city_id = c.id AND status = 'owned') AS properties,
			COALESCE(d.damage_bps, 0) AS damage_bps, ctl.code AS controller,
			(SELECT count(*) FROM city_group_links g WHERE g.city_id = c.id) AS groups, c.spawn_weight,
			c.id AS city_uuid, k.id AS country_uuid
			FROM cities c LEFT JOIN jurisdictions j ON j.id = c.jurisdiction_id LEFT JOIN jurisdictions k ON k.id = j.parent_id
			LEFT JOIN city_war_damage d ON d.city_id = c.id LEFT JOIN city_control cc ON cc.city_id = c.id
			LEFT JOIN jurisdictions ctl ON ctl.id = cc.controller_country_id`,
			Cols: cols("code:city", "name:text", "country:country", "population:int", "tax_rate_bps:bps", "cost_of_living:money",
				"treasury:money", "residents:int", "present:int", "companies:int", "properties:int", "damage_bps:bps",
				"controller:country", "groups:int", "spawn_weight:int"),
			Scopes: map[string]string{"country": countryIs, "city": `v.city_uuid = @::uuid`},
			Search: []string{"code", "name"}, Order: "code", Asc: true, Tie: "code"},

		View{Name: "city.budgets", SQL: `SELECT c.code AS city, b.period_no, b.started_at, b.ended_at, b.treasury, b.spendable,
			b.spent, b.defence, b.lines, b.city_id AS city_uuid
			FROM city_budget_periods b JOIN cities c ON c.id = b.city_id`,
			Cols: cols("city:city", "period_no:int", "started_at:time", "ended_at:time", "treasury:money", "spendable:money",
				"spent:money", "defence:money", "lines:json"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`}, Order: "started_at", Tie: "period_no", Time: "started_at"},

		View{Name: "city.demand", SQL: `SELECT c.code AS city, m.period_no, m.started_at, m.ended_at, m.population, m.budget,
			m.asked, m.paid, m.companies, m.settled_at, m.city_id AS city_uuid
			FROM company_market_periods m JOIN cities c ON c.id = m.city_id`,
			Cols: cols("city:city", "period_no:int", "started_at:time", "ended_at:time", "population:int", "budget:money",
				"asked:money", "paid:money", "companies:int", "settled_at:time"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`}, Order: "started_at", Tie: "period_no", Time: "started_at"},

		View{Name: "shops", SQL: `SELECT c.code AS city, s.shop_code AS shop, s.item_code AS item, s.stock, s.restocked_at,
			s.city_id AS city_uuid FROM shop_shelves s JOIN cities c ON c.id = s.city_id`,
			Cols:   cols("city:city", "shop:code", "item:item", "stock:int", "restocked_at:time"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`}, Filters: []Filter{free("shop"), free("item")},
			Search: []string{"shop", "item"}, Order: "stock", Asc: true, Tie: "item"},

		View{Name: "shop.sales", SQL: `SELECT s.id::text AS id, s.created_at, p.public_code AS player, c.code AS city,
			s.shop_code AS shop, s.item_code AS item, s.direction, s.quantity, s.unit_price, s.total, s.tax, s.method,
			s.player_id AS player_uuid, s.city_id AS city_uuid
			FROM shop_sales s JOIN players p ON p.id = s.player_id JOIN cities c ON c.id = s.city_id`,
			Cols: cols("created_at:time", "player:player", "city:city", "shop:code", "item:item", "direction:status",
				"quantity:int", "unit_price:money", "total:money", "tax:money", "method:code"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`, "city": `v.city_uuid = @::uuid`},
			Need:    []string{"player", "city"},
			Filters: []Filter{opts("direction", "buy", "sell"), free("shop"), free("item")},
			Order:   "created_at", Tie: "id", Time: "created_at"},

		View{Name: "city.groups", SQL: `SELECT c.code AS city, g.chat_id, b.bot_key AS bot, g.language, g.linked_by, g.linked_at,
			g.city_id AS city_uuid
			FROM city_group_links g JOIN cities c ON c.id = g.city_id LEFT JOIN telegram_bots b ON b.id = g.bot_id`,
			Cols:   cols("city:city", "chat_id:code", "bot:code", "language:code", "linked_by:text", "linked_at:time"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`}, Order: "linked_at", Tie: "chat_id"},

		View{Name: "city.control", SQL: `SELECT c.code AS city, dj.code AS country, ct.code AS controller, w.no AS war,
			cc.since, COALESCE(d.damage_bps, 0) AS damage_bps, d.last_struck_at, d.closed_until, c.id AS city_uuid,
			cc.de_jure_country_id AS country_uuid, cc.controller_country_id AS controller_uuid
			FROM cities c LEFT JOIN city_control cc ON cc.city_id = c.id LEFT JOIN jurisdictions dj ON dj.id = cc.de_jure_country_id
			LEFT JOIN jurisdictions ct ON ct.id = cc.controller_country_id LEFT JOIN wars w ON w.id = cc.war_id
			LEFT JOIN city_war_damage d ON d.city_id = c.id`,
			Cols: cols("city:city", "country:country", "controller:country", "war:int", "since:time", "damage_bps:bps",
				"last_struck_at:time", "closed_until:time"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`,
				"country": `(v.country_uuid = @::uuid OR v.controller_uuid = @::uuid)`},
			Order: "damage_bps", Tie: "city"},

		View{Name: "countries", SQL: `SELECT j.code, j.name,
			(SELECT count(*) FROM cities c JOIN jurisdictions cj ON cj.id = c.jurisdiction_id WHERE cj.parent_id = j.id) AS cities,
			(SELECT balance FROM accounts WHERE kind = 'state_treasury' AND owner_id = j.id) AS treasury,
			(SELECT balance FROM accounts WHERE kind = 'defence_fund' AND owner_id = j.id) AS defence_fund,
			(SELECT balance FROM accounts WHERE kind = 'national_bank' AND owner_id = j.id) AS national_bank,
			(SELECT balance FROM accounts WHERE kind = 'insurance_fund' AND owner_id = j.id) AS insurance_fund,
			(SELECT readiness_bps FROM military_clocks WHERE country_id = j.id) AS readiness_bps,
			(SELECT count(*) FROM military_assets a WHERE a.country_id = j.id AND a.status IN ('stationed', 'moving', 'committed')) AS forces,
			(SELECT count(*) FROM wars w WHERE (w.attacker_id = j.id OR w.defender_id = j.id) AND w.status <> 'ended') AS wars,
			(SELECT count(*) FROM loans l WHERE l.country_id = j.id AND l.status = 'active') AS loans,
			j.id AS country_uuid
			FROM jurisdictions j WHERE j.kind = 'country'`,
			Cols: cols("code:country", "name:text", "cities:int", "treasury:money", "defence_fund:money", "national_bank:money",
				"insurance_fund:money", "readiness_bps:bps", "forces:int", "wars:int", "loans:int"),
			Scopes: map[string]string{"country": countryIs}, Search: []string{"code", "name"}, Order: "code", Asc: true, Tie: "code"},

		View{Name: "forces", SQL: `SELECT j.code AS country, a.branch, a.class_code AS class, a.status, a.condition,
			g.code AS garrison, count(*) AS pieces, a.country_id AS country_uuid, a.garrison_city_id AS city_uuid
			FROM military_assets a JOIN jurisdictions j ON j.id = a.country_id LEFT JOIN cities g ON g.id = a.garrison_city_id
			GROUP BY j.code, a.branch, a.class_code, a.status, a.condition, g.code, a.country_id, a.garrison_city_id`,
			Cols: cols("country:country", "branch:code", "class:item", "status:status", "condition:status", "garrison:city",
				"pieces:int"),
			Scopes: map[string]string{"country": countryIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "stationed", "moving", "committed", "destroyed", "expended"), free("branch"),
				opts("condition", "ready", "damaged"), free("class")},
			Order: "pieces", Tie: "class"},

		View{Name: "military.periods", SQL: `SELECT j.code AS country, m.period_no, m.started_at, m.ended_at, m.revenue, m.levy,
			m.appropriation, m.upkeep_due, m.upkeep_paid, m.pieces, m.readiness_bps, m.war_levy, m.repairs, m.repaired,
			m.country_id AS country_uuid
			FROM military_periods m JOIN jurisdictions j ON j.id = m.country_id`,
			Cols: cols("country:country", "period_no:int", "started_at:time", "ended_at:time", "revenue:money", "levy:money",
				"appropriation:money", "upkeep_due:money", "upkeep_paid:money", "pieces:int", "readiness_bps:bps",
				"war_levy:money", "repairs:money", "repaired:int"),
			Scopes: map[string]string{"country": countryIs}, Order: "started_at", Tie: "period_no", Time: "started_at"},

		View{Name: "military.moves", SQL: `SELECT m.no, j.code AS country, m.branch, c.code AS to_city, m.item_code AS item,
			m.quantity, m.status, p.public_code AS ordered_by, COALESCE(m.office_code, '') AS office, m.started_at,
			m.arrives_at, m.arrived_at, m.country_id AS country_uuid, m.to_city_id AS city_uuid
			FROM military_moves m JOIN jurisdictions j ON j.id = m.country_id JOIN cities c ON c.id = m.to_city_id
			LEFT JOIN players p ON p.id = m.ordered_by`,
			Cols: cols("no:int", "country:country", "branch:code", "to_city:city", "item:item", "quantity:int", "status:status",
				"ordered_by:player", "office:code", "started_at:time", "arrives_at:time", "arrived_at:time"),
			Scopes:  map[string]string{"country": countryIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "moving", "arrived")}, Order: "started_at", Tie: "no", Time: "started_at"},

		View{Name: "procurements", SQL: `SELECT pr.no, j.code AS country, c.code AS company, pr.item_code AS item, d.no AS design,
			pr.quantity, pr.unit_price, pr.total, p.public_code AS bought_by, COALESCE(pr.office_code, '') AS office,
			pr.created_at, pr.country_id AS country_uuid, pr.company_id AS company_uuid
			FROM procurements pr JOIN jurisdictions j ON j.id = pr.country_id JOIN companies c ON c.id = pr.company_id
			LEFT JOIN product_designs d ON d.id = pr.design_id LEFT JOIN players p ON p.id = pr.bought_by`,
			Cols: cols("no:int", "country:country", "company:company", "item:item", "design:int", "quantity:int",
				"unit_price:money", "total:money", "bought_by:player", "office:code", "created_at:time"),
			Scopes: map[string]string{"country": countryIs, "company": companyIs},
			Order:  "created_at", Tie: "no", Time: "created_at"},

		View{Name: "wars", SQL: `SELECT w.no, a.code AS attacker, d.code AS defender, w.ground, w.status, p.public_code AS declared_by,
			w.declared_office, w.declared_at, w.active_at, w.border_closed, w.broke_treaties, w.ended_at,
			(SELECT count(*) FROM war_operations o WHERE o.war_id = w.id) AS operations,
			w.id AS war_uuid, w.attacker_id AS attacker_uuid, w.defender_id AS defender_uuid
			FROM wars w JOIN jurisdictions a ON a.id = w.attacker_id JOIN jurisdictions d ON d.id = w.defender_id
			LEFT JOIN players p ON p.id = w.declared_by`,
			Cols: cols("no:int", "attacker:country", "defender:country", "ground:code", "status:status", "declared_by:player",
				"declared_office:code", "declared_at:time", "active_at:time", "border_closed:bool", "ended_at:time",
				"operations:int", "broke_treaties:list"),
			Scopes: map[string]string{"war": `v.war_uuid = @::uuid`,
				"country": `(v.attacker_uuid = @::uuid OR v.defender_uuid = @::uuid OR EXISTS (SELECT 1 FROM war_parties wp WHERE wp.war_id = v.war_uuid AND wp.country_id = @::uuid))`},
			Filters: []Filter{opts("status", "declared", "ceasefire", "ended")}, Order: "declared_at", Tie: "no", Time: "declared_at"},

		View{Name: "war.parties", SQL: `SELECT w.no AS war, j.code AS country, wp.side, p.public_code AS joined_by,
			COALESCE(wp.office_code, '') AS office, wp.joined_at, wp.war_id AS war_uuid, wp.country_id AS country_uuid
			FROM war_parties wp JOIN wars w ON w.id = wp.war_id JOIN jurisdictions j ON j.id = wp.country_id
			LEFT JOIN players p ON p.id = wp.joined_by`,
			Cols:   cols("war:int", "country:country", "side:status", "joined_by:player", "office:code", "joined_at:time"),
			Scopes: map[string]string{"war": `v.war_uuid = @::uuid`, "country": countryIs}, Order: "joined_at", Tie: "country"},

		View{Name: "war.operations", SQL: `SELECT o.no, w.no AS war, o.kind, o.objective, j.code AS country, t.code AS target_country,
			fc.code AS from_city, tc.code AS target_city, o.class_code AS class, o.committed, o.munitions, o.status,
			p.public_code AS ordered_by, o.launched_at, o.strikes_at, o.resolved_at, o.attacker_lost, o.attacker_damaged,
			o.defender_lost, o.defender_damaged, o.munitions_used, o.hits, o.damage_bps, o.captured, o.report,
			o.war_id AS war_uuid, o.country_id AS country_uuid, o.target_country_id AS target_uuid, o.target_city_id AS city_uuid,
			o.id AS operation_uuid
			FROM war_operations o JOIN wars w ON w.id = o.war_id JOIN jurisdictions j ON j.id = o.country_id
			JOIN jurisdictions t ON t.id = o.target_country_id LEFT JOIN cities fc ON fc.id = o.from_city_id
			LEFT JOIN cities tc ON tc.id = o.target_city_id LEFT JOIN players p ON p.id = o.ordered_by`,
			Cols: cols("no:int", "war:int", "kind:status", "objective:status", "country:country", "target_country:country",
				"from_city:city", "target_city:city", "class:item", "committed:int", "munitions:int", "status:status",
				"ordered_by:player", "launched_at:time", "strikes_at:time", "resolved_at:time", "attacker_lost:int",
				"attacker_damaged:int", "defender_lost:int", "defender_damaged:int", "munitions_used:int", "hits:int",
				"damage_bps:bps", "captured:bool", "report:json"),
			Scopes: map[string]string{"war": `v.war_uuid = @::uuid`,
				"country": `(v.country_uuid = @::uuid OR v.target_uuid = @::uuid)`, "city": `v.city_uuid = @::uuid`,
				"operation": `v.operation_uuid = @::uuid`},
			Filters: []Filter{opts("status", "launched", "resolved", "called_off"), opts("kind", "air", "missile", "ground")},
			Order:   "launched_at", Tie: "no", Time: "launched_at"},

		View{Name: "war.proposals", SQL: `SELECT x.no, w.no AS war, x.kind, a.code AS proposer, b.code AS partner, x.status,
			p.public_code AS proposed_by, x.proposed_office, x.proposed_at, x.expires_at, x.decided_at,
			COALESCE(x.decided_office, '') AS decided_office, x.war_id AS war_uuid, x.proposer_id AS proposer_uuid,
			x.partner_id AS partner_uuid
			FROM war_proposals x JOIN wars w ON w.id = x.war_id JOIN jurisdictions a ON a.id = x.proposer_id
			JOIN jurisdictions b ON b.id = x.partner_id LEFT JOIN players p ON p.id = x.proposed_by`,
			Cols: cols("no:int", "war:int", "kind:status", "proposer:country", "partner:country", "status:status",
				"proposed_by:player", "proposed_office:code", "proposed_at:time", "expires_at:time", "decided_at:time",
				"decided_office:code"),
			Scopes: map[string]string{"war": `v.war_uuid = @::uuid`,
				"country": `(v.proposer_uuid = @::uuid OR v.partner_uuid = @::uuid)`},
			Filters: []Filter{opts("kind", "ceasefire", "peace"), opts("status", "proposed", "accepted", "declined", "withdrawn", "expired")},
			Order:   "proposed_at", Tie: "no"},

		View{Name: "war.events", SQL: `SELECT e.id::text AS id, e.created_at, e.kind, w.no AS war, j.code AS country,
			o.code AS other, c.code AS city, op.no AS operation, p.public_code AS player, COALESCE(e.office_code, '') AS office,
			e.war_id AS war_uuid, e.country_id AS country_uuid, e.other_country_id AS other_uuid
			FROM war_events e LEFT JOIN wars w ON w.id = e.war_id LEFT JOIN jurisdictions j ON j.id = e.country_id
			LEFT JOIN jurisdictions o ON o.id = e.other_country_id LEFT JOIN cities c ON c.id = e.city_id
			LEFT JOIN war_operations op ON op.id = e.operation_id LEFT JOIN players p ON p.id = e.player_id`,
			Cols: cols("created_at:time", "kind:status", "war:int", "country:country", "other:country", "city:city",
				"operation:int", "player:player", "office:code"),
			Scopes: map[string]string{"war": `v.war_uuid = @::uuid`,
				"country": `(v.country_uuid = @::uuid OR v.other_uuid = @::uuid)`},
			Filters: []Filter{opts("kind", "declared", "treaty_broken", "joined", "proposed", "ceasefire", "peace", "declined",
				"resumed", "operation", "captured", "liberated")},
			Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "sanctions", SQL: `SELECT s.no, i.code AS imposer, t.code AS target, s.measures, s.ground,
			p.public_code AS imposed_by, s.imposed_office, s.imposed_at, s.effective_at, s.lifted_at,
			COALESCE(s.lifted_office, '') AS lifted_office,
			CASE WHEN s.lifted_at IS NULL THEN 'standing' ELSE 'lifted' END AS state,
			s.imposer_id AS imposer_uuid, s.target_id AS target_uuid
			FROM sanctions s JOIN jurisdictions i ON i.id = s.imposer_id JOIN jurisdictions t ON t.id = s.target_id
			LEFT JOIN players p ON p.id = s.imposed_by`,
			Cols: cols("no:int", "imposer:country", "target:country", "measures:list", "ground:code", "imposed_by:player",
				"imposed_office:code", "imposed_at:time", "effective_at:time", "lifted_at:time", "lifted_office:code",
				"state:status"),
			Scopes:  map[string]string{"country": `(v.imposer_uuid = @::uuid OR v.target_uuid = @::uuid)`},
			Filters: []Filter{opts("state", "standing", "lifted")}, Order: "imposed_at", Tie: "no"},

		View{Name: "treaties", SQL: `SELECT t.no, t.kind, a.code AS proposer, b.code AS partner, t.status, p.public_code AS proposed_by,
			t.proposed_office, t.proposed_at, t.expires_at, t.decided_at, t.ended_at, t.proposer_id AS proposer_uuid,
			t.partner_id AS partner_uuid
			FROM treaties t JOIN jurisdictions a ON a.id = t.proposer_id JOIN jurisdictions b ON b.id = t.partner_id
			LEFT JOIN players p ON p.id = t.proposed_by`,
			Cols: cols("no:int", "kind:code", "proposer:country", "partner:country", "status:status", "proposed_by:player",
				"proposed_office:code", "proposed_at:time", "expires_at:time", "decided_at:time", "ended_at:time"),
			Scopes: map[string]string{"country": `(v.proposer_uuid = @::uuid OR v.partner_uuid = @::uuid)`},
			Filters: []Filter{opts("status", "proposed", "active", "declined", "withdrawn", "expired", "terminated"),
				free("kind")},
			Order: "proposed_at", Tie: "no"},

		View{Name: "diplomacy", SQL: `SELECT e.id::text AS id, e.created_at, e.kind, j.code AS country, o.code AS other,
			p.public_code AS player, COALESCE(e.office_code, '') AS office, e.country_id AS country_uuid,
			e.other_country_id AS other_uuid
			FROM diplomacy_events e JOIN jurisdictions j ON j.id = e.country_id LEFT JOIN jurisdictions o ON o.id = e.other_country_id
			LEFT JOIN players p ON p.id = e.player_id`,
			Cols:   cols("created_at:time", "kind:status", "country:country", "other:country", "player:player", "office:code"),
			Scopes: map[string]string{"country": `(v.country_uuid = @::uuid OR v.other_uuid = @::uuid)`},
			Filters: []Filter{opts("kind", "sanction_imposed", "sanction_lifted", "treaty_proposed", "treaty_signed",
				"treaty_declined", "treaty_withdrawn", "treaty_terminated")},
			Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "tariffs", SQL: `SELECT t.id::text AS id, t.at, t.reference_type, i.code AS importer, e.code AS exporter,
			t.value, t.rate_bps, t.tariff, t.importer_id AS importer_uuid, t.exporter_id AS exporter_uuid
			FROM border_tariffs t JOIN jurisdictions i ON i.id = t.importer_id JOIN jurisdictions e ON e.id = t.exporter_id`,
			Cols: cols("at:time", "reference_type:code", "importer:country", "exporter:country", "value:money", "rate_bps:bps",
				"tariff:money"),
			Scopes: map[string]string{"country": `(v.importer_uuid = @::uuid OR v.exporter_uuid = @::uuid)`},
			Order:  "at", Tie: "id", Time: "at"},

		View{Name: "bank.fundings", SQL: `SELECT j.code AS country, f.period_no, f.amount, f.funded_at, f.country_id AS country_uuid
			FROM bank_fundings f JOIN jurisdictions j ON j.id = f.country_id`,
			Cols:   cols("country:country", "period_no:int", "amount:money", "funded_at:time"),
			Scopes: map[string]string{"country": countryIs}, Order: "funded_at", Tie: "period_no", Time: "funded_at"},

		View{Name: "seats", SQL: `SELECT j.kind AS place_kind, j.code AS place, o.office_code AS office, o.seat,
			p.public_code AS holder, COALESCE(o.acquired_by, '') AS acquired_by, o.since, o.term_ends_at,
			CASE WHEN o.holder_player_id IS NULL THEN 'vacant' ELSE 'held' END AS state,
			o.jurisdiction_id AS jurisdiction_uuid, o.holder_player_id AS holder_uuid
			FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id LEFT JOIN players p ON p.id = o.holder_player_id`,
			Cols: cols("place_kind:code", "place:code", "office:code", "seat:int", "holder:player", "acquired_by:status",
				"since:time", "term_ends_at:time", "state:status"),
			Scopes:  placeScopes("v.jurisdiction_uuid", map[string]string{"player": `v.holder_uuid = @::uuid`}),
			Filters: []Filter{free("office"), opts("state", "vacant", "held"), opts("place_kind", "world", "country", "city")},
			Search:  []string{"office", "place", "holder"}, Order: "term_ends_at", Asc: true, Tie: "office"},

		View{Name: "policy.values", SQL: `SELECT j.kind AS place_kind, j.code AS place, v.lever_code AS lever, v.value_kind,
			v.value, v.value_json, p.public_code AS set_by, o.office_code AS office, v.set_at, v.effective_at,
			v.jurisdiction_id AS jurisdiction_uuid, v.set_by_player_id AS player_uuid
			FROM policy_values v JOIN jurisdictions j ON j.id = v.jurisdiction_id LEFT JOIN players p ON p.id = v.set_by_player_id
			LEFT JOIN offices o ON o.id = v.office_id`,
			Cols: cols("place_kind:code", "place:code", "lever:code", "value_kind:code", "value:int", "set_by:player",
				"office:code", "set_at:time", "effective_at:time", "value_json:json"),
			Scopes:  placeScopes("v.jurisdiction_uuid", map[string]string{"player": `v.player_uuid = @::uuid`}),
			Filters: []Filter{free("lever")}, Search: []string{"lever", "place"}, Order: "effective_at", Tie: "lever"},

		View{Name: "policy.changes", SQL: `SELECT c.id::text AS id, c.set_at, j.kind AS place_kind, j.code AS place,
			c.lever_code AS lever, c.office_code AS office, p.public_code AS set_by, c.old_value, c.new_value,
			c.old_value_json, c.new_value_json, c.effective_at, c.jurisdiction_id AS jurisdiction_uuid,
			c.set_by_player_id AS player_uuid
			FROM policy_changes c JOIN jurisdictions j ON j.id = c.jurisdiction_id LEFT JOIN players p ON p.id = c.set_by_player_id`,
			Cols: cols("set_at:time", "place_kind:code", "place:code", "lever:code", "office:code", "set_by:player",
				"old_value:int", "new_value:int", "effective_at:time", "old_value_json:json", "new_value_json:json"),
			Scopes:  placeScopes("v.jurisdiction_uuid", map[string]string{"player": `v.player_uuid = @::uuid`}),
			Filters: []Filter{free("lever")}, Order: "set_at", Tie: "id", Time: "set_at"},

		View{Name: "proposals", SQL: `SELECT x.no, x.kind, j.kind AS place_kind, j.code AS place, x.subject, x.value, x.value_json,
			p.public_code AS proposed_by, x.proposer_office, x.body, x.rule, x.threshold, x.quorum, x.seats, x.status,
			x.opened_at, x.closes_at, x.decided_at, x.yes_votes, x.no_votes, COALESCE(x.lapse_reason, '') AS lapse_reason,
			x.jurisdiction_id AS jurisdiction_uuid, x.proposed_by AS player_uuid, x.id AS proposal_uuid
			FROM proposals x JOIN jurisdictions j ON j.id = x.jurisdiction_id LEFT JOIN players p ON p.id = x.proposed_by`,
			Cols: cols("no:int", "kind:status", "place_kind:code", "place:code", "subject:code", "value:int",
				"proposed_by:player", "proposer_office:code", "rule:status", "threshold:code", "quorum:code", "seats:int",
				"status:status", "opened_at:time", "closes_at:time", "decided_at:time", "yes_votes:int", "no_votes:int",
				"lapse_reason:code", "body:text", "value_json:json"),
			Scopes: placeScopes("v.jurisdiction_uuid", map[string]string{"player": `v.player_uuid = @::uuid`,
				"proposal": `v.proposal_uuid = @::uuid`}),
			Filters: []Filter{opts("status", "open", "passed", "failed", "lapsed"), opts("kind", "lever", "action")},
			Search:  []string{"subject", "body"}, Order: "opened_at", Tie: "no", Time: "opened_at"},

		View{Name: "proposal.votes", SQL: `SELECT x.no AS proposal, x.subject, o.office_code AS office, p.public_code AS player,
			v.vote, v.cast_at, v.proposal_id AS proposal_uuid, v.player_id AS player_uuid
			FROM proposal_votes v JOIN proposals x ON x.id = v.proposal_id JOIN offices o ON o.id = v.office_id
			JOIN players p ON p.id = v.player_id`,
			Cols:   cols("proposal:int", "subject:code", "office:code", "player:player", "vote:status", "cast_at:time"),
			Scopes: map[string]string{"proposal": `v.proposal_uuid = @::uuid`, "player": `v.player_uuid = @::uuid`},
			Need:   []string{"proposal", "player"}, Order: "cast_at", Tie: "player"},

		View{Name: "elections", SQL: `SELECT e.no, e.office_code AS office, j.kind AS place_kind, j.code AS place, e.seats, e.status,
			e.opens_at, e.candidacy_ends_at, e.voting_ends_at, e.counted_at, e.votes_cast,
			(SELECT count(*) FROM election_candidates c WHERE c.election_id = e.id) AS candidates,
			(SELECT count(*) FROM election_voters v WHERE v.election_id = e.id) AS voters,
			e.jurisdiction_id AS jurisdiction_uuid, e.id AS election_uuid
			FROM elections e JOIN jurisdictions j ON j.id = e.jurisdiction_id`,
			Cols: cols("no:int", "office:code", "place_kind:code", "place:code", "seats:int", "status:status", "opens_at:time",
				"candidacy_ends_at:time", "voting_ends_at:time", "counted_at:time", "votes_cast:int", "candidates:int",
				"voters:int"),
			Scopes:  placeScopes("v.jurisdiction_uuid", map[string]string{"election": `v.election_uuid = @::uuid`}),
			Filters: []Filter{opts("status", "open", "counted"), free("office")}, Order: "opens_at", Tie: "no", Time: "opens_at"},

		View{Name: "election.candidates", SQL: `SELECT e.no AS election, e.office_code AS office, j.code AS place,
			p.public_code AS player, c.stood_at, c.deposit, c.deposit_method, c.votes, c.elected, c.seat, c.deposit_returned,
			c.election_id AS election_uuid, c.player_id AS player_uuid
			FROM election_candidates c JOIN elections e ON e.id = c.election_id JOIN jurisdictions j ON j.id = e.jurisdiction_id
			JOIN players p ON p.id = c.player_id`,
			Cols: cols("election:int", "office:code", "place:code", "player:player", "stood_at:time", "deposit:money",
				"deposit_method:code", "votes:int", "elected:bool", "seat:int", "deposit_returned:bool"),
			Scopes: map[string]string{"election": `v.election_uuid = @::uuid`, "player": `v.player_uuid = @::uuid`},
			Need:   []string{"election", "player"}, Order: "votes", Tie: "player"},

		View{Name: "election.voters", SQL: `SELECT e.no AS election, e.office_code AS office, j.code AS place, e.status,
			e.voting_ends_at, v.player_id AS player_uuid, v.election_id AS election_uuid
			FROM election_voters v JOIN elections e ON e.id = v.election_id JOIN jurisdictions j ON j.id = e.jurisdiction_id`,
			Cols:   cols("election:int", "office:code", "place:code", "status:status", "voting_ends_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Need: []string{"player"},
			Order: "voting_ends_at", Tie: "election"},
	)
}
