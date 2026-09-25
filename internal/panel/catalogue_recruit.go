package panel

// Specialist recruitment (migrations/0031): the companies' NPC specialists,
// their campaigns and candidates, and each city's pool of specialists.

func init() {
	register(
		View{Name: "npc.staff", SQL: `SELECT s.no, c.code AS company, h.code AS home_city, s.skill, s.level, s.preference,
			s.salary, s.housing, s.accepted_bps, s.term_periods, s.served, s.expiring, s.unpaid_run, s.underpaid_run, s.shares,
			s.status, COALESCE(s.leave_reason, '') AS leave_reason, s.hired_at, s.left_at,
			s.company_id AS company_uuid, s.home_city_id AS city_uuid
			FROM npc_staff s JOIN companies c ON c.id = s.company_id LEFT JOIN cities h ON h.id = s.home_city_id`,
			Cols: cols("no:int", "company:company", "home_city:city", "skill:code", "level:int", "preference:code", "salary:money",
				"housing:money", "accepted_bps:bps", "term_periods:int", "served:int", "expiring:bool", "unpaid_run:int",
				"underpaid_run:int", "shares:int", "status:status", "leave_reason:status", "hired_at:time", "left_at:time"),
			Scopes:  map[string]string{"company": companyIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{free("status"), free("skill")},
			Order:   "hired_at", Tie: "no", Time: "hired_at"},

		View{Name: "recruit.campaigns", SQL: `SELECT r.no, c.code AS company, r.status, r.skill, r.min_level, r.cities, r.positions,
			r.hired, r.salary, r.housing, r.signing, r.relocation, r.term_periods, r.shares, r.auto_accept, r.ad_fee,
			r.checks_done, r.checks_total, r.next_check_at, p.public_code AS created_by, r.created_at, r.ended_at,
			r.company_id AS company_uuid
			FROM recruit_campaigns r JOIN companies c ON c.id = r.company_id LEFT JOIN players p ON p.id = r.created_by`,
			Cols: cols("no:int", "company:company", "status:status", "skill:code", "min_level:int", "positions:int", "hired:int",
				"salary:money", "housing:money", "signing:money", "relocation:money", "term_periods:int", "shares:int",
				"auto_accept:bool", "ad_fee:money", "checks_done:int", "checks_total:int", "next_check_at:time",
				"created_by:player", "created_at:time", "ended_at:time", "cities:list"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{free("status"), free("skill")},
			Order:   "created_at", Tie: "no", Time: "created_at"},

		View{Name: "recruit.candidates", SQL: `SELECT x.no, r.no AS campaign, c.code AS company, h.code AS home_city, x.skill, x.level,
			x.preference, x.expected, x.move_cost, x.value, x.chance_bps, x.status, x.applied_at, x.expires_at, x.decided_at,
			x.company_id AS company_uuid
			FROM recruit_candidates x JOIN recruit_campaigns r ON r.id = x.campaign_id JOIN companies c ON c.id = x.company_id
			LEFT JOIN cities h ON h.id = x.home_city_id`,
			Cols: cols("no:int", "campaign:int", "company:company", "home_city:city", "skill:code", "level:int", "preference:code",
				"expected:money", "move_cost:money", "value:money", "chance_bps:bps", "status:status", "applied_at:time",
				"expires_at:time", "decided_at:time"),
			Scopes:  map[string]string{"company": companyIs},
			Filters: []Filter{free("status"), free("skill")},
			Order:   "applied_at", Tie: "no", Time: "applied_at"},

		View{Name: "specialist.pools", SQL: `SELECT c.code AS city, p.skill, p.level, p.available, p.refilled_at, p.city_id AS city_uuid
			FROM specialist_pools p JOIN cities c ON c.id = p.city_id`,
			Cols:    cols("city:city", "skill:code", "level:int", "available:int", "refilled_at:time"),
			Scopes:  map[string]string{"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{free("skill")}, Order: "available", Tie: "skill"},
	)
}
