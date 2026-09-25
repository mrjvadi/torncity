package panel

// The markets — the player market's books and trades, the auction house,
// gold — and finance: loans and credit, insurance, savings.

func init() {
	register(
		View{Name: "market.orders", SQL: `SELECT o.no, c.code AS city, o.item_code AS item, o.side, o.order_type, o.quantity, o.filled,
			o.unit_price, p.public_code AS owner, o.funding, o.status, o.created_at, o.expires_at, o.closed_at,
			o.city_id AS city_uuid, o.owner_id AS owner_uuid
			FROM market_orders o JOIN cities c ON c.id = o.city_id JOIN players p ON p.id = o.owner_id`,
			Cols: cols("no:int", "city:city", "item:item", "side:status", "order_type:code", "quantity:int", "filled:int",
				"unit_price:money", "owner:player", "funding:code", "status:status", "created_at:time", "expires_at:time",
				"closed_at:time"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`, "player": ownerIs},
			Filters: []Filter{opts("status", "open", "filled", "cancelled", "expired"), opts("side", "buy", "sell"),
				free("item")},
			Search: []string{"item", "owner"}, Order: "created_at", Tie: "no", Time: "created_at"},

		View{Name: "market.book", SQL: `SELECT c.code AS city, o.item_code AS item, o.side, o.unit_price,
			SUM(o.quantity - o.filled)::bigint AS quantity, count(*) AS orders, o.city_id AS city_uuid
			FROM market_orders o JOIN cities c ON c.id = o.city_id WHERE o.status = 'open'
			GROUP BY c.code, o.item_code, o.side, o.unit_price, o.city_id`,
			Cols:    cols("city:city", "item:item", "side:status", "unit_price:money", "quantity:int", "orders:int"),
			Scopes:  map[string]string{"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{free("item"), opts("side", "buy", "sell")},
			Search:  []string{"item"}, Order: "item", Asc: true, Tie: "unit_price"},

		View{Name: "market.trades", SQL: `SELECT t.id::text AS id, t.created_at, c.code AS city, t.item_code AS item,
			b.public_code AS buyer, s.public_code AS seller, t.quantity, t.unit_price, t.notional, t.fee,
			t.ledger_transaction_id::text AS tx, t.city_id AS city_uuid, t.buyer_id AS buyer_uuid, t.seller_id AS seller_uuid
			FROM market_trades t JOIN cities c ON c.id = t.city_id JOIN players b ON b.id = t.buyer_id
			JOIN players s ON s.id = t.seller_id`,
			Cols: cols("created_at:time", "city:city", "item:item", "buyer:player", "seller:player", "quantity:int",
				"unit_price:money", "notional:money", "fee:money", "tx:id"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`,
				"player": `(v.buyer_uuid = @::uuid OR v.seller_uuid = @::uuid)`},
			Filters: []Filter{free("item")}, Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "market.items", SQL: `SELECT t.item_code AS item, count(*) AS trades, SUM(t.quantity)::bigint AS quantity,
			SUM(t.notional)::bigint AS notional, (SUM(t.notional) / NULLIF(SUM(t.quantity), 0))::bigint AS avg_price,
			MIN(t.unit_price) AS low, MAX(t.unit_price) AS high, MAX(t.created_at) AS last_at
			FROM market_trades t WHERE true /*window*/ GROUP BY t.item_code`,
			Cols:   cols("item:item", "trades:int", "quantity:int", "notional:money", "avg_price:money", "low:money", "high:money", "last_at:time"),
			Search: []string{"item"}, Order: "notional", Tie: "item", Window: "t.created_at", WindowDays: 7},

		View{Name: "auctions", SQL: `SELECT a.no, c.code AS city, s.public_code AS seller, a.item_code AS item, ip.serial,
			a.reserve, hb.amount AS high_bid, hp.public_code AS high_bidder, a.status, a.opens_at, a.ends_at, a.closed_at,
			a.fee, (SELECT count(*) FROM auction_bids b WHERE b.auction_id = a.id) AS bids,
			a.city_id AS city_uuid, a.seller_id AS seller_uuid, a.id AS auction_uuid
			FROM auctions a JOIN cities c ON c.id = a.city_id JOIN players s ON s.id = a.seller_id
			LEFT JOIN item_pieces ip ON ip.id = a.piece_id LEFT JOIN auction_bids hb ON hb.id = a.high_bid_id
			LEFT JOIN players hp ON hp.id = hb.bidder_id`,
			Cols: cols("no:int", "city:city", "seller:player", "item:item", "serial:code", "reserve:money", "high_bid:money",
				"high_bidder:player", "status:status", "opens_at:time", "ends_at:time", "closed_at:time", "fee:money", "bids:int"),
			Scopes: map[string]string{"city": `v.city_uuid = @::uuid`, "player": `v.seller_uuid = @::uuid`,
				"auction": `v.auction_uuid = @::uuid`},
			Filters: []Filter{opts("status", "open", "sold", "unsold"), free("item")},
			Order:   "opens_at", Tie: "no", Time: "opens_at"},

		View{Name: "auction.bids", SQL: `SELECT b.id::text AS id, b.created_at, a.no AS auction, a.item_code AS item,
			p.public_code AS bidder, b.amount, b.method, b.status, b.auction_id AS auction_uuid, b.bidder_id AS player_uuid
			FROM auction_bids b JOIN auctions a ON a.id = b.auction_id JOIN players p ON p.id = b.bidder_id`,
			Cols: cols("created_at:time", "auction:int", "item:item", "bidder:player", "amount:money", "method:code",
				"status:status"),
			Scopes: map[string]string{"auction": `v.auction_uuid = @::uuid`, "player": `v.player_uuid = @::uuid`},
			Need:   []string{"auction", "player"}, Order: "created_at", Tie: "id"},

		View{Name: "gold.prices", SQL: `SELECT g.period_no, g.price, g.net_grams, g.set_at FROM gold_prices g`,
			Cols:  cols("period_no:int", "price:money", "net_grams:int", "set_at:time"),
			Order: "period_no", Tie: "period_no", Time: "set_at"},

		View{Name: "gold.trades", SQL: `SELECT t.id::text AS id, t.traded_at, p.public_code AS player, t.side, t.grams, t.unit_price,
			t.total, COALESCE(t.method, '') AS method, t.player_id AS player_uuid
			FROM gold_trades t JOIN players p ON p.id = t.player_id`,
			Cols: cols("traded_at:time", "player:player", "side:status", "grams:int", "unit_price:money", "total:money",
				"method:code"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("side", "buy", "sell")}, Order: "traded_at", Tie: "id", Time: "traded_at"},

		View{Name: "gold.holdings", SQL: `SELECT p.public_code AS player, h.grams, h.cost, h.updated_at, h.player_id AS player_uuid
			FROM gold_holdings h JOIN players p ON p.id = h.player_id`,
			Cols:   cols("player:player", "grams:int", "cost:money", "updated_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Order: "grams", Tie: "player"},

		View{Name: "loans", SQL: `SELECT l.no, l.product, l.borrower_kind,
			COALESCE('player:' || p.public_code, 'company:' || c.code) AS borrower, j.code AS country, pr.no AS property,
			l.principal, l.rate_bps, l.interest, l.periods, l.paid_periods, l.arrears, l.missed_total, l.principal_paid,
			l.interest_paid, l.fees_due, l.fees_paid, (l.principal - l.principal_paid) AS outstanding, l.status, l.recovered,
			l.written_off, l.opened_at, l.closed_at, l.player_id AS player_uuid, l.company_id AS company_uuid,
			l.country_id AS country_uuid, l.id AS loan_uuid
			FROM loans l LEFT JOIN players p ON p.id = l.player_id LEFT JOIN companies c ON c.id = l.company_id
			LEFT JOIN jurisdictions j ON j.id = l.country_id LEFT JOIN properties pr ON pr.id = l.property_id`,
			Cols: cols("no:int", "product:code", "borrower_kind:code", "borrower:ref", "country:country", "property:int",
				"principal:money", "rate_bps:bps", "interest:money", "periods:int", "paid_periods:int", "arrears:int",
				"missed_total:int", "principal_paid:money", "interest_paid:money", "fees_due:money", "fees_paid:money",
				"outstanding:money", "status:status", "recovered:money", "written_off:money", "opened_at:time", "closed_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "company": companyIs, "country": countryIs,
				"loan": `v.loan_uuid = @::uuid`},
			Filters: []Filter{opts("status", "active", "repaid", "defaulted"), free("product"), opts("borrower_kind", "player", "company")},
			Order:   "opened_at", Tie: "no", Time: "opened_at"},

		View{Name: "loan.periods", SQL: `SELECT l.no AS loan, p.period_no, p.due, p.paid, p.principal, p.interest, p.fees, p.new_fee,
			p.missed, p.defaulted, p.at, p.loan_id AS loan_uuid, l.player_id AS player_uuid
			FROM loan_periods p JOIN loans l ON l.id = p.loan_id`,
			Cols: cols("loan:int", "period_no:int", "due:int", "paid:int", "principal:money", "interest:money", "fees:money",
				"new_fee:money", "missed:bool", "defaulted:bool", "at:time"),
			Scopes: map[string]string{"loan": `v.loan_uuid = @::uuid`, "player": `v.player_uuid = @::uuid`},
			Need:   []string{"loan", "player"}, Order: "at", Tie: "period_no"},

		View{Name: "credit.events", SQL: `SELECT e.id::text AS id, e.at, p.public_code AS player, l.no AS loan, e.kind, e.period_no,
			e.player_id AS player_uuid
			FROM credit_events e JOIN players p ON p.id = e.player_id LEFT JOIN loans l ON l.id = e.loan_id`,
			Cols:    cols("at:time", "player:player", "loan:int", "kind:status", "period_no:int"),
			Scopes:  map[string]string{"player": `v.player_uuid = @::uuid`},
			Filters: []Filter{opts("kind", "opened", "on_time", "missed", "repaid", "default")},
			Order:   "at", Tie: "id", Time: "at"},

		View{Name: "insurance.policies", SQL: `SELECT x.no, p.public_code AS player, x.product, x.covers, j.code AS country,
			pr.no AS property, x.status, x.started_at, x.claims_from, x.ended_at, COALESCE(x.end_reason, '') AS end_reason,
			(SELECT COALESCE(SUM(amount), 0) FROM insurance_premiums m WHERE m.policy_id = x.id) AS premiums,
			(SELECT COALESCE(SUM(paid), 0) FROM insurance_claims c WHERE c.policy_id = x.id) AS claims_paid,
			x.player_id AS player_uuid, x.country_id AS country_uuid, x.id AS policy_uuid
			FROM insurance_policies x JOIN players p ON p.id = x.player_id LEFT JOIN jurisdictions j ON j.id = x.country_id
			LEFT JOIN properties pr ON pr.id = x.property_id`,
			Cols: cols("no:int", "player:player", "product:code", "covers:status", "country:country", "property:int",
				"status:status", "started_at:time", "claims_from:time", "ended_at:time", "end_reason:status", "premiums:money",
				"claims_paid:money"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "country": countryIs,
				"policy": `v.policy_uuid = @::uuid`},
			Filters: []Filter{opts("status", "active", "ended"), opts("covers", "hospital", "war_damage"), free("product")},
			Order:   "started_at", Tie: "no", Time: "started_at"},

		View{Name: "insurance.claims", SQL: `SELECT c.id::text AS id, c.claimed_at, x.no AS policy, p.public_code AS player, c.source,
			c.loss, c.due, c.paid, c.player_id AS player_uuid, c.policy_id AS policy_uuid, x.country_id AS country_uuid
			FROM insurance_claims c JOIN insurance_policies x ON x.id = c.policy_id JOIN players p ON p.id = c.player_id`,
			Cols: cols("claimed_at:time", "policy:int", "player:player", "source:code", "loss:money", "due:money", "paid:money"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "policy": `v.policy_uuid = @::uuid`,
				"country": countryIs},
			Order: "claimed_at", Tie: "id", Time: "claimed_at"},

		View{Name: "insurance.premiums", SQL: `SELECT m.paid_at, x.no AS policy, p.public_code AS player, m.period_no, m.amount,
			m.policy_id AS policy_uuid, x.player_id AS player_uuid
			FROM insurance_premiums m JOIN insurance_policies x ON x.id = m.policy_id JOIN players p ON p.id = x.player_id`,
			Cols:   cols("paid_at:time", "policy:int", "player:player", "period_no:int", "amount:money"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "policy": `v.policy_uuid = @::uuid`},
			Need:   []string{"player", "policy"}, Order: "paid_at", Tie: "period_no"},

		View{Name: "savings", SQL: `SELECT p.public_code AS player,
			(SELECT balance FROM accounts WHERE kind = 'player_savings' AND owner_id = s.player_id) AS balance,
			s.marked_balance, s.marked_period, s.updated_at,
			(SELECT COALESCE(SUM(amount), 0) FROM savings_interest i WHERE i.player_id = s.player_id) AS interest_paid,
			s.player_id AS player_uuid
			FROM savings_accounts s JOIN players p ON p.id = s.player_id`,
			Cols: cols("player:player", "balance:money", "marked_balance:money", "marked_period:int", "updated_at:time",
				"interest_paid:money"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Order: "balance", Tie: "player"},

		View{Name: "savings.interest", SQL: `SELECT i.paid_at, p.public_code AS player, i.period_no, j.code AS country, i.balance,
			i.rate_bps, i.amount, i.player_id AS player_uuid, i.country_id AS country_uuid
			FROM savings_interest i JOIN players p ON p.id = i.player_id LEFT JOIN jurisdictions j ON j.id = i.country_id`,
			Cols: cols("paid_at:time", "player:player", "period_no:int", "country:country", "balance:money", "rate_bps:bps",
				"amount:money"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`, "country": countryIs},
			Order:  "paid_at", Tie: "player", Time: "paid_at"},

		View{Name: "portfolio", SQL: `SELECT p.public_code AS player, m.value, m.gain, m.marked_at, m.player_id AS player_uuid
			FROM portfolio_marks m JOIN players p ON p.id = m.player_id`,
			Cols:   cols("player:player", "value:money", "gain:money", "marked_at:time"),
			Scopes: map[string]string{"player": `v.player_uuid = @::uuid`}, Order: "value", Tie: "player"},

		View{Name: "holds", SQL: `SELECT h.no, h.created_at, a.public_code AS payer, b.public_code AS payee, h.method, h.amount,
			h.status, f.no AS flag, h.settled_at, COALESCE(h.settled_by, '') AS settled_by, COALESCE(h.note, '') AS note,
			h.payer_id AS payer_uuid, h.payee_id AS payee_uuid
			FROM payment_holds h JOIN players a ON a.id = h.payer_id JOIN players b ON b.id = h.payee_id
			LEFT JOIN watch_flags f ON f.id = h.flag_id`,
			Cols: cols("no:int", "created_at:time", "payer:player", "payee:player", "method:code", "amount:money",
				"status:status", "flag:int", "settled_at:time", "settled_by:text", "note:text"),
			Scopes:  map[string]string{"player": `(v.payer_uuid = @::uuid OR v.payee_uuid = @::uuid)`},
			Filters: []Filter{opts("status", "held", "released", "returned")}, Order: "created_at", Tie: "no"},

		View{Name: "flags", SQL: `SELECT f.no, f.rule, p.public_code AS player, o.public_code AS other, f.score, f.hits, f.status,
			f.created_at, f.updated_at, f.cleared_at, COALESCE(f.cleared_by, '') AS cleared_by, COALESCE(f.note, '') AS note,
			f.evidence, f.player_id AS player_uuid, f.other_player_id AS other_uuid
			FROM watch_flags f JOIN players p ON p.id = f.player_id LEFT JOIN players o ON o.id = f.other_player_id`,
			Cols: cols("no:int", "rule:status", "player:player", "other:player", "score:int", "hits:int", "status:status",
				"created_at:time", "updated_at:time", "cleared_at:time", "cleared_by:text", "note:text", "evidence:json"),
			Scopes: map[string]string{"player": `(v.player_uuid = @::uuid OR v.other_uuid = @::uuid)`},
			Filters: []Filter{opts("status", "open", "cleared"), opts("rule", "one_way_transfers", "off_market_trade",
				"single_partner", "command_rate", "wash_trade")},
			Order: "updated_at", Tie: "no"},
	)
}
