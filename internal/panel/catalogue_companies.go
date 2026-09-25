package panel

// Companies: their books and periods, staff and openings, what they design,
// research and make, what they sell, and their shares on the exchange.

const companyIs = `v.company_uuid = @::uuid`

func init() {
	register(
		View{Name: "companies", SQL: `SELECT c.id AS company_uuid, c.code, c.name, c.type_code AS type, ci.code AS city, c.status,
			o.public_code AS owner, m.public_code AS manager,
			(SELECT balance FROM accounts WHERE kind = 'company_treasury' AND owner_id = c.id) AS treasury,
			c.debt, c.arrears, c.price_bps, c.rating_bps, c.total_shares, (c.listed_at IS NOT NULL) AS listed,
			(SELECT count(*) FROM employments e WHERE e.company_id = c.id AND e.ended_at IS NULL) AS staff,
			c.founded_at, c.closed_at, COALESCE(c.close_reason, '') AS close_reason,
			c.owner_player_id AS owner_uuid, c.manager_player_id AS manager_uuid, c.city_id AS city_uuid
			FROM companies c JOIN cities ci ON ci.id = c.city_id LEFT JOIN players o ON o.id = c.owner_player_id
			LEFT JOIN players m ON m.id = c.manager_player_id`,
			Cols: cols("code:company", "name:text", "type:code", "city:city", "status:status", "owner:player", "manager:player",
				"treasury:money", "debt:money", "arrears:int", "price_bps:bps", "rating_bps:bps", "total_shares:int",
				"listed:bool", "staff:int", "founded_at:time", "closed_at:time", "close_reason:status"),
			Scopes: map[string]string{"player": `(v.owner_uuid = @::uuid OR v.manager_uuid = @::uuid)`,
				"city": `v.city_uuid = @::uuid`, "company": companyIs},
			Filters: []Filter{opts("status", "active", "dissolved"), free("type"), opts("listed", "true", "false")},
			Search:  []string{"code", "name", "owner"}, Order: "founded_at", Tie: "code", Time: "founded_at"},

		View{Name: "company.periods", SQL: `SELECT p.company_id AS company_uuid, c.code AS company, p.period_no, ci.code AS city,
			p.started_at, p.ended_at, p.presence_bps, p.price_bps, p.quality_bps, p.shifts, p.wanted_units, p.capacity_units,
			p.sold_units, p.stock_units, p.revenue, p.sales_tax, p.wages, p.upkeep_due, p.upkeep_paid, p.debt, p.balance_after,
			p.insolvent, p.settled_at, p.city_id AS city_uuid
			FROM company_periods p JOIN companies c ON c.id = p.company_id JOIN cities ci ON ci.id = p.city_id`,
			Cols: cols("company:company", "period_no:int", "city:city", "started_at:time", "ended_at:time", "presence_bps:bps",
				"price_bps:bps", "quality_bps:bps", "shifts:int", "wanted_units:int", "capacity_units:int", "sold_units:int",
				"stock_units:int", "revenue:money", "sales_tax:money", "wages:money", "upkeep_due:money", "upkeep_paid:money",
				"debt:money", "balance_after:money", "insolvent:bool", "settled_at:time"),
			Scopes: map[string]string{"company": companyIs, "city": `v.city_uuid = @::uuid`},
			Need:   []string{"company", "city"}, Order: "period_no", Tie: "company", Time: "started_at"},

		View{Name: "company.openings", SQL: `SELECT o.no, c.code AS company, o.career_code AS career, o.wage, o.positions, o.status,
			(SELECT count(*) FROM company_applications a WHERE a.opening_id = o.id AND a.status = 'pending') AS pending,
			o.created_at, o.updated_at, o.company_id AS company_uuid
			FROM company_openings o JOIN companies c ON c.id = o.company_id`,
			Cols: cols("no:int", "company:company", "career:code", "wage:money", "positions:int", "status:status",
				"pending:int", "created_at:time", "updated_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("status", "open", "closed")}, Order: "created_at", Tie: "no"},

		View{Name: "company.applications", SQL: `SELECT a.no, c.code AS company, o.no AS opening, o.career_code AS career,
			p.public_code AS player, a.status, a.applied_at, a.decided_at, a.company_id AS company_uuid, a.player_id AS player_uuid
			FROM company_applications a JOIN companies c ON c.id = a.company_id JOIN company_openings o ON o.id = a.opening_id
			JOIN players p ON p.id = a.player_id`,
			Cols: cols("no:int", "company:company", "opening:int", "career:code", "player:player", "status:status",
				"applied_at:time", "decided_at:time"),
			Scopes:  map[string]string{"company": companyIs, "player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("status", "pending", "accepted", "rejected", "withdrawn")},
			Order:   "applied_at", Tie: "no"},

		View{Name: "company.designs", SQL: `SELECT d.no, c.code AS company, d.item_code AS item, d.archetype, d.name, d.origin,
			d.status, d.quality_loss_bps, d.overhead_bps, sd.no AS source, cp.public_code AS created_by, d.created_at,
			d.finalized_at, d.fills, (SELECT count(*) FROM item_pieces i WHERE i.design_id = d.id AND i.holding <> 'gone') AS pieces,
			d.company_id AS company_uuid, d.id AS design_uuid
			FROM product_designs d JOIN companies c ON c.id = d.company_id LEFT JOIN product_designs sd ON sd.id = d.source_design_id
			LEFT JOIN players cp ON cp.id = d.created_by`,
			Cols: cols("no:int", "company:company", "item:item", "archetype:code", "name:text", "origin:status", "status:status",
				"quality_loss_bps:bps", "overhead_bps:bps", "source:int", "created_by:player", "created_at:time",
				"finalized_at:time", "pieces:int", "fills:json"),
			Scopes:  map[string]string{"company": companyIs, "design": `v.design_uuid = @::uuid`},
			Filters: []Filter{opts("status", "draft", "final", "retired"), opts("origin", "authored", "reverse_engineered"), free("item")},
			Search:  []string{"name", "item"}, Order: "created_at", Tie: "no"},

		View{Name: "company.techs", SQL: `SELECT c.code AS company, t.tech_code AS tech, t.mode, t.license_price, t.acquired_at,
			t.updated_at, t.published_at, t.company_id AS company_uuid
			FROM company_technologies t JOIN companies c ON c.id = t.company_id`,
			Cols: cols("company:company", "tech:code", "mode:status", "license_price:money", "acquired_at:time",
				"updated_at:time", "published_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("mode", "private", "license", "published"), free("tech")},
			Search:  []string{"tech"}, Order: "acquired_at", Tie: "tech"},

		View{Name: "company.licences", SQL: `SELECT l.id::text AS id, l.tech_code AS tech, lo.code AS licensor, le.code AS licensee,
			l.price, bp.public_code AS bought_by, l.granted_at, l.licensor_company_id AS licensor_uuid,
			l.licensee_company_id AS licensee_uuid
			FROM technology_licenses l JOIN companies lo ON lo.id = l.licensor_company_id
			JOIN companies le ON le.id = l.licensee_company_id LEFT JOIN players bp ON bp.id = l.bought_by`,
			Cols:   cols("tech:code", "licensor:company", "licensee:company", "price:money", "bought_by:player", "granted_at:time"),
			Scopes: map[string]string{"company": `(v.licensor_uuid = @::uuid OR v.licensee_uuid = @::uuid)`},
			Search: []string{"tech"}, Order: "granted_at", Tie: "id"},

		View{Name: "company.research", SQL: `SELECT r.id::text AS id, c.code AS company, r.tech_code AS tech, r.status, r.cost,
			sp.public_code AS started_by, r.started_at, r.finish_at, r.completed_at, r.company_id AS company_uuid
			FROM company_research r JOIN companies c ON c.id = r.company_id LEFT JOIN players sp ON sp.id = r.started_by`,
			Cols: cols("company:company", "tech:code", "status:status", "cost:money", "started_by:player", "started_at:time",
				"finish_at:time", "completed_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("status", "running", "done")}, Order: "started_at", Tie: "id"},

		View{Name: "company.orders", SQL: `SELECT o.no, c.code AS company, o.kind, d.no AS design, o.output_code AS output,
			o.quantity, o.output_qty, o.workers, o.input_quality, o.skill_level, o.quality, o.status, pb.public_code AS placed_by,
			o.started_at, o.finish_at, o.completed_at, o.consumed, o.game_action_id::text AS action, o.company_id AS company_uuid
			FROM production_orders o JOIN companies c ON c.id = o.company_id LEFT JOIN product_designs d ON d.id = o.design_id
			LEFT JOIN players pb ON pb.id = o.placed_by`,
			Cols: cols("no:int", "company:company", "kind:status", "design:int", "output:item", "quantity:int", "output_qty:int",
				"workers:int", "input_quality:int", "skill_level:int", "quality:int", "status:status", "placed_by:player",
				"started_at:time", "finish_at:time", "completed_at:time", "action:id", "consumed:json"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("status", "running", "done"), opts("kind", "design", "component"), free("output")},
			Order:   "started_at", Tie: "no", Time: "started_at"},

		View{Name: "company.reverse", SQL: `SELECT j.no, c.code AS company, j.item_code AS item, sd.no AS source, j.skill, j.level,
			j.chance_bps, j.status, rd.no AS result, j.started_at, j.finish_at, j.completed_at, j.company_id AS company_uuid
			FROM reverse_jobs j JOIN companies c ON c.id = j.company_id LEFT JOIN product_designs sd ON sd.id = j.source_design_id
			LEFT JOIN product_designs rd ON rd.id = j.result_design_id`,
			Cols: cols("no:int", "company:company", "item:item", "source:int", "skill:code", "level:int", "chance_bps:bps",
				"status:status", "result:int", "started_at:time", "finish_at:time", "completed_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("status", "running", "succeeded", "failed")}, Order: "started_at", Tie: "no"},

		View{Name: "company.listings", SQL: `SELECT l.no, c.code AS company, ci.code AS city, l.item_code AS item, d.no AS design,
			l.quantity, l.sold, l.unit_price, l.status, l.created_at, l.updated_at, l.closed_at, l.company_id AS company_uuid,
			l.city_id AS city_uuid
			FROM company_listings l JOIN companies c ON c.id = l.company_id JOIN cities ci ON ci.id = l.city_id
			LEFT JOIN product_designs d ON d.id = l.design_id`,
			Cols: cols("no:int", "company:company", "city:city", "item:item", "design:int", "quantity:int", "sold:int",
				"unit_price:money", "status:status", "created_at:time", "updated_at:time", "closed_at:time"),
			Scopes:  map[string]string{"company": companyIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "open", "sold", "withdrawn"), free("item")},
			Search:  []string{"item", "company"}, Order: "created_at", Tie: "no", Time: "created_at"},

		View{Name: "company.sales", SQL: `SELECT s.id::text AS id, s.created_at, c.code AS company, s.item_code AS item,
			COALESCE('player:' || b.public_code, CASE s.buyer_org_kind WHEN 'company' THEN 'company:' || bc.code
				WHEN 'state' THEN 'country:' || bj.code END) AS buyer,
			s.quantity, s.unit_price, s.total, s.tax, s.method, s.ledger_transaction_id::text AS tx,
			s.company_id AS company_uuid, s.buyer_player_id AS buyer_uuid, c.city_id AS city_uuid
			FROM company_sales s JOIN companies c ON c.id = s.company_id LEFT JOIN players b ON b.id = s.buyer_player_id
			LEFT JOIN companies bc ON s.buyer_org_kind = 'company' AND bc.id = s.buyer_org_id
			LEFT JOIN jurisdictions bj ON s.buyer_org_kind = 'state' AND bj.id = s.buyer_org_id`,
			Cols: cols("created_at:time", "company:company", "item:item", "buyer:ref", "quantity:int", "unit_price:money",
				"total:money", "tax:money", "method:code", "tx:id"),
			Scopes: map[string]string{"company": companyIs, "player": `v.buyer_uuid = @::uuid`},
			Need:   []string{"company", "player"}, Filters: []Filter{free("item")},
			Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "company.supplies", SQL: `SELECT s.id::text AS id, s.created_at, c.code AS company, ci.code AS city,
			s.supplier_code AS supplier, s.component, s.quantity, s.unit_price, s.total, bp.public_code AS bought_by,
			s.company_id AS company_uuid
			FROM supply_purchases s JOIN companies c ON c.id = s.company_id JOIN cities ci ON ci.id = s.city_id
			LEFT JOIN players bp ON bp.id = s.bought_by`,
			Cols: cols("created_at:time", "company:company", "city:city", "supplier:code", "component:item", "quantity:int",
				"unit_price:money", "total:money", "bought_by:player"),
			Scopes: map[string]string{"company": companyIs}, Need: []string{"company"},
			Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "shareholders", SQL: `SELECT c.code AS company, p.public_code AS player, h.shares, h.locked, h.cost,
			round(h.shares * 10000.0 / NULLIF(c.total_shares, 0))::bigint AS stake_bps, h.acquired_at,
			h.company_id AS company_uuid, h.player_id AS player_uuid
			FROM company_shareholders h JOIN companies c ON c.id = h.company_id JOIN players p ON p.id = h.player_id`,
			Cols: cols("company:company", "player:player", "shares:int", "locked:int", "cost:money", "stake_bps:bps",
				"acquired_at:time"),
			Scopes: map[string]string{"company": companyIs, "player": `v.player_uuid = @::uuid`},
			Need:   []string{"company", "player"}, Order: "shares", Tie: "player"},

		View{Name: "share.orders", SQL: `SELECT o.no, c.code AS company, o.side, o.quantity, o.filled, o.unit_price,
			p.public_code AS owner, o.status, o.created_at, o.expires_at, o.closed_at, o.company_id AS company_uuid,
			o.owner_id AS owner_uuid
			FROM share_orders o JOIN companies c ON c.id = o.company_id JOIN players p ON p.id = o.owner_id`,
			Cols: cols("no:int", "company:company", "side:status", "quantity:int", "filled:int", "unit_price:money",
				"owner:player", "status:status", "created_at:time", "expires_at:time", "closed_at:time"),
			Scopes:  map[string]string{"company": companyIs, "player": ownerIs},
			Filters: []Filter{opts("status", "open", "filled", "cancelled", "expired"), opts("side", "buy", "sell")},
			Order:   "created_at", Tie: "no", Time: "created_at"},

		View{Name: "share.trades", SQL: `SELECT t.id::text AS id, t.created_at, c.code AS company, b.public_code AS buyer,
			s.public_code AS seller, t.quantity, t.unit_price, t.notional, t.fee, t.company_id AS company_uuid,
			t.buyer_id AS buyer_uuid, t.seller_id AS seller_uuid
			FROM share_trades t JOIN companies c ON c.id = t.company_id JOIN players b ON b.id = t.buyer_id
			JOIN players s ON s.id = t.seller_id`,
			Cols: cols("created_at:time", "company:company", "buyer:player", "seller:player", "quantity:int",
				"unit_price:money", "notional:money", "fee:money"),
			Scopes: map[string]string{"company": companyIs, "player": `(v.buyer_uuid = @::uuid OR v.seller_uuid = @::uuid)`},
			Order:  "created_at", Tie: "id", Time: "created_at"},

		View{Name: "stocks", SQL: `SELECT c.code AS company, c.name, ci.code AS city, l.float_shares, c.total_shares, l.price AS ipo_price,
			(SELECT t.unit_price FROM share_trades t WHERE t.company_id = c.id ORDER BY t.created_at DESC LIMIT 1) AS last_price,
			l.book_per_share, l.fee, lp.public_code AS listed_by, l.listed_at,
			(SELECT count(*) FROM company_shareholders h WHERE h.company_id = c.id) AS holders,
			(SELECT count(*) FROM share_orders o WHERE o.company_id = c.id AND o.status = 'open') AS open_orders,
			c.id AS company_uuid
			FROM stock_listings l JOIN companies c ON c.id = l.company_id JOIN cities ci ON ci.id = c.city_id
			LEFT JOIN players lp ON lp.id = l.listed_by`,
			Cols: cols("company:company", "name:text", "city:city", "float_shares:int", "total_shares:int", "ipo_price:money",
				"last_price:money", "book_per_share:money", "fee:money", "listed_by:player", "listed_at:time", "holders:int",
				"open_orders:int"),
			Scopes: map[string]string{"company": companyIs},
			Search: []string{"company", "name"}, Order: "listed_at", Tie: "company"},

		View{Name: "dividends", SQL: `SELECT d.no, c.code AS company, dp.public_code AS declared_by, d.amount, d.tax, d.per_share,
			d.total_shares, d.paid, d.declared_at, d.company_id AS company_uuid
			FROM dividends d JOIN companies c ON c.id = d.company_id LEFT JOIN players dp ON dp.id = d.declared_by`,
			Cols: cols("no:int", "company:company", "declared_by:player", "amount:money", "tax:money", "per_share:money",
				"total_shares:int", "paid:money", "declared_at:time"),
			Scopes: map[string]string{"company": companyIs}, Order: "declared_at", Tie: "no", Time: "declared_at"},

		View{Name: "dividend.payments", SQL: `SELECT d.no AS dividend, c.code AS company, p.public_code AS player, x.shares, x.amount,
			d.declared_at, d.company_id AS company_uuid, x.player_id AS player_uuid, d.id AS dividend_uuid
			FROM dividend_payments x JOIN dividends d ON d.id = x.dividend_id JOIN companies c ON c.id = d.company_id
			JOIN players p ON p.id = x.player_id`,
			Cols: cols("dividend:int", "company:company", "player:player", "shares:int", "amount:money", "declared_at:time"),
			Scopes: map[string]string{"company": companyIs, "player": `v.player_uuid = @::uuid`,
				"dividend": `v.dividend_uuid = @::uuid`},
			Need: []string{"company", "player", "dividend"}, Order: "declared_at", Tie: "player"},

		View{Name: "defence.licences", SQL: `SELECT l.no, c.code AS company, l.kind, l.basis, l.status,
			ap.public_code AS applied_by, l.applied_at, COALESCE(l.decided_office, '') AS decided_office, l.decided_at,
			COALESCE(l.revoked_office, '') AS revoked_office, l.revoked_at, l.effective_at, l.company_id AS company_uuid
			FROM defence_licences l JOIN companies c ON c.id = l.company_id LEFT JOIN players ap ON ap.id = l.applied_by`,
			Cols: cols("no:int", "company:company", "kind:status", "basis:status", "status:status", "applied_by:player",
				"applied_at:time", "decided_office:code", "decided_at:time", "revoked_office:code", "revoked_at:time",
				"effective_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{opts("status", "pending", "active", "revoking", "revoked", "rejected"), opts("kind", "manufacturer", "contractor")},
			Order:   "applied_at", Tie: "no"},

		View{Name: "clinics", SQL: `SELECT c.code AS company, c.name, ci.code AS city, s.price, s.open, c.status,
			(SELECT COALESCE(SUM(quantity), 0) FROM org_stacks o WHERE o.org_kind = 'company' AND o.org_id = c.id
				AND o.holding = 'warehouse') AS stock_units,
			(SELECT count(*) FROM hospital_treatments t WHERE t.company_id = c.id) AS treatments,
			s.updated_at, c.id AS company_uuid, c.city_id AS city_uuid
			FROM clinic_services s JOIN companies c ON c.id = s.company_id JOIN cities ci ON ci.id = c.city_id`,
			Cols: cols("company:company", "name:text", "city:city", "price:money", "open:bool", "status:status",
				"stock_units:int", "treatments:int", "updated_at:time"),
			Scopes:  map[string]string{"company": companyIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("open", "true", "false")}, Order: "treatments", Tie: "company"},
	)
}
