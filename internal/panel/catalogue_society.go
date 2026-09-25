package panel

// Crime and justice, health, missions and achievements, factions and the
// leaderboards; and the system's own tables.

const playerIs = `v.player_uuid = @::uuid`

func init() {
	register(
		View{Name: "crimes", SQL: `SELECT c.id::text AS id, c.started_at, p.public_code AS player, c.crime_code AS crime, c.category,
			ci.code AS city, c.venue_code AS venue, c.victim_kind, v.public_code AS victim, c.status, c.chance_bps, c.nerve_cost,
			c.reward_amount, c.witnessed, c.fine_amount, c.fine_paid, COALESCE(c.stolen_item, '') AS stolen_item, c.stolen_qty,
			c.resolves_at, c.resolved_at, c.player_id AS player_uuid, c.victim_player_id AS victim_uuid, c.city_id AS city_uuid
			FROM crimes c JOIN players p ON p.id = c.player_id JOIN cities ci ON ci.id = c.city_id
			LEFT JOIN players v ON v.id = c.victim_player_id`,
			Cols: cols("started_at:time", "player:player", "crime:code", "category:code", "city:city", "venue:code",
				"victim_kind:status", "victim:player", "status:status", "chance_bps:bps", "nerve_cost:int", "reward_amount:money",
				"witnessed:bool", "fine_amount:money", "fine_paid:money", "stolen_item:item", "stolen_qty:int",
				"resolves_at:time", "resolved_at:time"),
			Scopes: map[string]string{"player": `(v.player_uuid = @::uuid OR v.victim_uuid = @::uuid)`,
				"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "in_progress", "succeeded", "escaped", "caught"), free("category"), free("crime"),
				opts("victim_kind", "npc", "player", "business", "property")},
			Order: "started_at", Tie: "id", Time: "started_at"},

		View{Name: "crime.stats", SQL: `SELECT c.crime_code AS crime, c.category, count(*) AS attempts,
			count(*) FILTER (WHERE c.status = 'succeeded') AS succeeded,
			count(*) FILTER (WHERE c.status = 'escaped') AS escaped,
			count(*) FILTER (WHERE c.status = 'caught') AS caught,
			(10000 * count(*) FILTER (WHERE c.status = 'succeeded') / NULLIF(count(*) FILTER (WHERE c.status <> 'in_progress'), 0)) AS success_bps,
			(10000 * count(*) FILTER (WHERE c.status = 'caught') / NULLIF(count(*) FILTER (WHERE c.status <> 'in_progress'), 0)) AS caught_bps,
			COALESCE(SUM(c.reward_amount) FILTER (WHERE c.status = 'succeeded'), 0)::bigint AS rewards,
			(AVG(c.chance_bps))::bigint AS avg_chance_bps
			FROM crimes c WHERE true /*window*/ GROUP BY c.crime_code, c.category`,
			Cols: cols("crime:code", "category:code", "attempts:int", "succeeded:int", "escaped:int", "caught:int",
				"success_bps:bps", "caught_bps:bps", "rewards:money", "avg_chance_bps:bps"),
			Filters: []Filter{free("category")}, Search: []string{"crime", "category"},
			Order: "attempts", Tie: "crime", Window: "c.started_at", WindowDays: 7},

		View{Name: "jail", SQL: `SELECT s.id::text AS id, p.public_code AS player, c.code AS city, s.reason, s.term_seconds, s.status,
			s.bail_paid, s.starts_at, s.ends_at, s.released_at, s.game_action_id::text AS action, s.player_id AS player_uuid,
			s.city_id AS city_uuid
			FROM jail_sentences s JOIN players p ON p.id = s.player_id JOIN cities c ON c.id = s.city_id`,
			Cols: cols("player:player", "city:city", "reason:status", "term_seconds:seconds", "status:status", "bail_paid:money",
				"starts_at:time", "ends_at:time", "released_at:time", "action:id"),
			Scopes:  map[string]string{"player": playerIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "serving", "released", "bailed"), opts("reason", "arrest", "conviction")},
			Order:   "starts_at", Tie: "id", Time: "starts_at"},

		View{Name: "crime.reports", SQL: `SELECT r.id::text AS id, r.filed_at, v.public_code AS victim, s.public_code AS suspect,
			c.code AS city, r.status, r.stolen, r.report_fee, r.solve_chance_bps, r.restitution_paid, r.restitution_shortfall,
			r.fine_amount, r.fine_paid, r.concludes_at, r.concluded_at, r.victim_player_id AS victim_uuid,
			r.suspect_player_id AS suspect_uuid, r.city_id AS city_uuid
			FROM crime_reports r JOIN players v ON v.id = r.victim_player_id LEFT JOIN players s ON s.id = r.suspect_player_id
			JOIN cities c ON c.id = r.city_id`,
			Cols: cols("filed_at:time", "victim:player", "suspect:player", "city:city", "status:status", "stolen:money",
				"report_fee:money", "solve_chance_bps:bps", "restitution_paid:money", "restitution_shortfall:money",
				"fine_amount:money", "fine_paid:money", "concludes_at:time", "concluded_at:time"),
			Scopes: map[string]string{"player": `(v.victim_uuid = @::uuid OR v.suspect_uuid = @::uuid)`,
				"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "investigating", "solved", "unsolved")}, Order: "filed_at", Tie: "id", Time: "filed_at"},

		View{Name: "criminals", SQL: `SELECT p.public_code AS player, cp.nerve, cp.heat, cp.criminal_xp, cp.attempts, cp.successes,
			cp.arrests, cp.convictions, cp.unpaid_restitution, cp.unpaid_fines, cp.updated_at, cp.player_id AS player_uuid
			FROM criminal_profiles cp JOIN players p ON p.id = cp.player_id`,
			Cols: cols("player:player", "nerve:int", "heat:int", "criminal_xp:int", "attempts:int", "successes:int", "arrests:int",
				"convictions:int", "unpaid_restitution:money", "unpaid_fines:money", "updated_at:time"),
			Scopes: map[string]string{"player": playerIs}, Order: "heat", Tie: "player"},

		View{Name: "hospital.stays", SQL: `SELECT s.id::text AS id, s.admitted_at, p.public_code AS player, c.code AS city, s.cause,
			s.status, s.health_in, s.health_out, s.ends_at, s.discharged_at, s.game_action_id::text AS action,
			s.player_id AS player_uuid, s.city_id AS city_uuid
			FROM hospital_stays s JOIN players p ON p.id = s.player_id JOIN cities c ON c.id = s.city_id`,
			Cols: cols("admitted_at:time", "player:player", "city:city", "cause:status", "status:status", "health_in:int",
				"health_out:int", "ends_at:time", "discharged_at:time", "action:id"),
			Scopes:  map[string]string{"player": playerIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "admitted", "discharged"), opts("cause", "crime", "war", "work", "faction_crime")},
			Order:   "admitted_at", Tie: "id", Time: "admitted_at"},

		View{Name: "hospital.treatments", SQL: `SELECT t.id::text AS id, t.created_at, p.public_code AS player, c.code AS city,
			t.provider, co.code AS company, t.price, t.method, COALESCE(t.medicine_item, '') AS medicine, t.medicine_units,
			t.doctor_level, t.reduction_bps, t.saved_seconds, t.player_id AS player_uuid, t.company_id AS company_uuid,
			t.city_id AS city_uuid
			FROM hospital_treatments t JOIN players p ON p.id = t.player_id JOIN cities c ON c.id = t.city_id
			LEFT JOIN companies co ON co.id = t.company_id`,
			Cols: cols("created_at:time", "player:player", "city:city", "provider:status", "company:company", "price:money",
				"method:code", "medicine:item", "medicine_units:int", "doctor_level:int", "reduction_bps:bps",
				"saved_seconds:seconds"),
			Scopes:  map[string]string{"player": playerIs, "company": companyIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("provider", "city", "clinic")}, Order: "created_at", Tie: "id", Time: "created_at"},

		View{Name: "missions", SQL: `SELECT m.no, p.public_code AS player, m.mission_code AS mission, c.code AS city, m.board,
			m.status, m.progress, m.accepted_at, m.expires_at, m.ended_at, m.reward_cash, m.reward_withheld, m.reward_xp,
			m.player_id AS player_uuid, m.city_id AS city_uuid
			FROM mission_assignments m JOIN players p ON p.id = m.player_id LEFT JOIN cities c ON c.id = m.city_id`,
			Cols: cols("no:int", "player:player", "mission:code", "city:city", "board:code", "status:status", "progress:list",
				"accepted_at:time", "expires_at:time", "ended_at:time", "reward_cash:money", "reward_withheld:money",
				"reward_xp:int"),
			Scopes:  map[string]string{"player": playerIs, "city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "active", "completed", "abandoned", "expired"), free("mission"), free("board")},
			Order:   "accepted_at", Tie: "no", Time: "accepted_at"},

		View{Name: "mission.stats", SQL: `SELECT m.mission_code AS mission, m.board, count(*) AS accepted,
			count(*) FILTER (WHERE m.status = 'completed') AS completed,
			count(*) FILTER (WHERE m.status = 'abandoned') AS abandoned,
			count(*) FILTER (WHERE m.status = 'expired') AS expired,
			count(*) FILTER (WHERE m.status = 'active') AS active,
			COALESCE(SUM(m.reward_cash) FILTER (WHERE m.status = 'completed'), 0)::bigint AS cash_paid,
			COALESCE(SUM(m.reward_withheld), 0)::bigint AS withheld
			FROM mission_assignments m WHERE true /*window*/ GROUP BY m.mission_code, m.board`,
			Cols: cols("mission:code", "board:code", "accepted:int", "completed:int", "abandoned:int", "expired:int",
				"active:int", "cash_paid:money", "withheld:money"),
			Search: []string{"mission"}, Order: "accepted", Tie: "mission", Window: "m.accepted_at", WindowDays: 30},

		View{Name: "achievements", SQL: `SELECT a.awarded_at, p.public_code AS player, a.code, a.cash, a.withheld,
			a.player_id AS player_uuid
			FROM player_achievements a JOIN players p ON p.id = a.player_id`,
			Cols:    cols("awarded_at:time", "player:player", "code:code", "cash:money", "withheld:money"),
			Scopes:  map[string]string{"player": playerIs},
			Filters: []Filter{free("code")}, Order: "awarded_at", Tie: "code", Time: "awarded_at"},

		View{Name: "achievement.stats", SQL: `SELECT a.code, count(*) AS awarded, MIN(a.awarded_at) AS first_at,
			MAX(a.awarded_at) AS last_at, COALESCE(SUM(a.cash), 0)::bigint AS cash, COALESCE(SUM(a.withheld), 0)::bigint AS withheld
			FROM player_achievements a GROUP BY a.code`,
			Cols:   cols("code:code", "awarded:int", "first_at:time", "last_at:time", "cash:money", "withheld:money"),
			Search: []string{"code"}, Order: "awarded", Tie: "code"},

		View{Name: "achievement.progress", SQL: `SELECT p.public_code AS player, a.code, a.count, a.updated_at, a.player_id AS player_uuid
			FROM achievement_progress a JOIN players p ON p.id = a.player_id`,
			Cols:   cols("player:player", "code:code", "count:int", "updated_at:time"),
			Scopes: map[string]string{"player": playerIs}, Need: []string{"player"}, Order: "count", Tie: "code"},

		View{Name: "factions", SQL: `SELECT f.code, f.name, c.code AS city, l.public_code AS leader, f.status,
			(SELECT count(*) FROM faction_members m WHERE m.faction_id = f.id) AS members,
			(SELECT balance FROM accounts WHERE kind = 'faction_treasury' AND owner_id = f.id) AS treasury,
			(SELECT count(*) FROM faction_operations o WHERE o.faction_id = f.id) AS operations,
			f.chat_id, f.founded_at, f.disbanded_at, f.id AS faction_uuid, f.city_id AS city_uuid, f.leader_id AS leader_uuid
			FROM factions f LEFT JOIN cities c ON c.id = f.city_id LEFT JOIN players l ON l.id = f.leader_id`,
			Cols: cols("code:faction", "name:text", "city:city", "leader:player", "status:status", "members:int",
				"treasury:money", "operations:int", "chat_id:code", "founded_at:time", "disbanded_at:time"),
			Scopes: map[string]string{"faction": `v.faction_uuid = @::uuid`, "city": `v.city_uuid = @::uuid`,
				"player": `(v.leader_uuid = @::uuid OR EXISTS (SELECT 1 FROM faction_members fm WHERE fm.faction_id = v.faction_uuid AND fm.player_id = @::uuid))`},
			Filters: []Filter{opts("status", "active", "disbanded")},
			Search:  []string{"code", "name"}, Order: "members", Tie: "code"},

		View{Name: "faction.members", SQL: `SELECT f.code AS faction, p.public_code AS player, m.rank, m.joined_at, m.updated_at,
			m.faction_id AS faction_uuid, m.player_id AS player_uuid
			FROM faction_members m JOIN factions f ON f.id = m.faction_id JOIN players p ON p.id = m.player_id`,
			Cols:    cols("faction:faction", "player:player", "rank:status", "joined_at:time", "updated_at:time"),
			Scopes:  map[string]string{"faction": `v.faction_uuid = @::uuid`, "player": playerIs},
			Filters: []Filter{opts("rank", "leader", "officer", "member")}, Order: "joined_at", Tie: "player"},

		View{Name: "faction.requests", SQL: `SELECT r.no, f.code AS faction, p.public_code AS player, r.kind, r.status, r.created_at,
			r.decided_at, r.faction_id AS faction_uuid, r.player_id AS player_uuid
			FROM faction_requests r JOIN factions f ON f.id = r.faction_id JOIN players p ON p.id = r.player_id`,
			Cols:    cols("no:int", "faction:faction", "player:player", "kind:status", "status:status", "created_at:time", "decided_at:time"),
			Scopes:  map[string]string{"faction": `v.faction_uuid = @::uuid`, "player": playerIs},
			Filters: []Filter{opts("status", "pending", "accepted", "declined", "withdrawn"), opts("kind", "invite", "apply")},
			Order:   "created_at", Tie: "no"},

		View{Name: "faction.operations", SQL: `SELECT o.no, f.code AS faction, o.crime_code AS crime, c.code AS city, o.place_code AS place,
			pp.public_code AS planned_by, o.status, o.chance_bps, o.take, o.faction_cut,
			(SELECT count(*) FROM faction_operation_crew x WHERE x.operation_id = o.id) AS crew,
			o.created_at, o.gather_until, o.launched_at, o.resolves_at, o.resolved_at,
			o.faction_id AS faction_uuid, o.id AS crew_uuid, o.city_id AS city_uuid
			FROM faction_operations o JOIN factions f ON f.id = o.faction_id JOIN cities c ON c.id = o.city_id
			LEFT JOIN players pp ON pp.id = o.planned_by`,
			Cols: cols("no:int", "faction:faction", "crime:code", "city:city", "place:code", "planned_by:player",
				"status:status", "chance_bps:bps", "take:money", "faction_cut:money", "crew:int", "created_at:time",
				"gather_until:time", "launched_at:time", "resolves_at:time", "resolved_at:time"),
			Scopes: map[string]string{"faction": `v.faction_uuid = @::uuid`, "crew": `v.crew_uuid = @::uuid`,
				"city": `v.city_uuid = @::uuid`},
			Filters: []Filter{opts("status", "gathering", "running", "succeeded", "escaped", "caught", "called_off")},
			Order:   "created_at", Tie: "no", Time: "created_at"},

		View{Name: "faction.crew", SQL: `SELECT o.no AS operation, f.code AS faction, p.public_code AS player, x.rank, x.share,
			x.joined_at, x.operation_id AS crew_uuid, x.player_id AS player_uuid, o.faction_id AS faction_uuid
			FROM faction_operation_crew x JOIN faction_operations o ON o.id = x.operation_id JOIN factions f ON f.id = o.faction_id
			JOIN players p ON p.id = x.player_id`,
			Cols: cols("operation:int", "faction:faction", "player:player", "rank:status", "share:money", "joined_at:time"),
			Scopes: map[string]string{"crew": `v.crew_uuid = @::uuid`, "player": playerIs,
				"faction": `v.faction_uuid = @::uuid`},
			Need: []string{"crew", "player", "faction"}, Order: "joined_at", Tie: "player"},

		View{Name: "leaderboards", SQL: `SELECT l.period_no, l.board, l.position, l.code, l.name, l.tag, l.tag_name, l.value,
			l.extra, l.extra2
			FROM leaderboard_lines l WHERE l.period_no = (SELECT max(period_no) FROM leaderboard_periods)`,
			Cols: cols("board:status", "position:int", "code:code", "name:text", "tag:code", "tag_name:text", "value:int",
				"extra:int", "extra2:int", "period_no:int"),
			Filters: []Filter{opts("board", "richest", "companies", "cities", "workers", "investors")},
			Search:  []string{"code", "name"}, Order: "position", Asc: true, Tie: "board"},

		View{Name: "outbox", SQL: `SELECT o.id, o.event_id::text AS event, o.subject, o.status, o.attempts, o.created_at,
			o.published_at, o.payload FROM outbox o`,
			Cols: cols("id:int", "event:id", "subject:code", "status:status", "attempts:int", "created_at:time",
				"published_at:time", "payload:json"),
			Filters: []Filter{opts("status", "pending", "published", "failed"), {Key: "subject", Prefix: true}},
			Search:  []string{"subject"}, Order: "id", Tie: "id", Time: "created_at"},

		View{Name: "migrations", SQL: `SELECT version, applied_at FROM schema_migrations`,
			Cols: cols("version:code", "applied_at:time"), Order: "version", Tie: "version"},

		View{Name: "bots", SQL: `SELECT b.bot_key, b.username, b.telegram_bot_id, b.status, b.enabled, b.gateway_group,
			b.rate_limit, (SELECT count(*) FROM player_bot_links l WHERE l.bot_id = b.id) AS players,
			(SELECT count(*) FROM city_group_links g WHERE g.bot_id = b.id) AS groups, b.updated_at
			FROM telegram_bots b`,
			Cols: cols("bot_key:code", "username:code", "telegram_bot_id:code", "status:status", "enabled:bool",
				"gateway_group:code", "rate_limit:int", "players:int", "groups:int", "updated_at:time"),
			Order: "bot_key", Asc: true, Tie: "bot_key"},

		View{Name: "content.versions", SQL: `SELECT v.version, v.status, v.loaded_at, v.loaded_by, v.source_checksum AS checksum,
			COALESCE(v.notes, '') AS notes FROM content_versions v`,
			Cols:  cols("version:int", "status:status", "loaded_at:time", "loaded_by:text", "checksum:code", "notes:text"),
			Order: "version", Tie: "version"},

		View{Name: "clocks", SQL: `SELECT 'city' AS kind, c.code AS owner, k.period_no, k.period_started_at, k.next_at, k.updated_at
			FROM city_clocks k JOIN cities c ON c.id = k.city_id
			UNION ALL SELECT 'market', c.code, k.period_no, k.period_started_at, k.next_at, k.updated_at
			FROM company_markets k JOIN cities c ON c.id = k.city_id
			UNION ALL SELECT 'military', j.code, k.period_no, k.period_started_at, k.next_at, k.updated_at
			FROM military_clocks k JOIN jurisdictions j ON j.id = k.country_id
			UNION ALL SELECT 'finance', '', period_no, period_started_at, next_at, updated_at FROM finance_clock
			UNION ALL SELECT 'leaderboard', '', period_no, period_started_at, next_at, updated_at FROM leaderboard_clock`,
			Cols:    cols("kind:status", "owner:code", "period_no:int", "period_started_at:time", "next_at:time", "updated_at:time"),
			Filters: []Filter{opts("kind", "city", "market", "military", "finance", "leaderboard")},
			Order:   "next_at", Asc: true, Tie: "owner"},

		// One branch per status, so each walks its own partial index
		// instead of the whole history of actions.
		View{Name: "action.backlog", SQL: `SELECT a.action_type AS type, 'scheduled' AS status, count(*) AS actions,
			MIN(a.finish_at) AS oldest_due, MAX(a.retry_count) AS max_retries,
			count(*) FILTER (WHERE a.finish_at < now() - interval '1 minute') AS overdue
			FROM game_actions a WHERE a.status = 'scheduled' GROUP BY a.action_type
			UNION ALL SELECT a.action_type, 'running', count(*), MIN(a.finish_at), MAX(a.retry_count),
			count(*) FILTER (WHERE a.claimed_at < now() - interval '5 minutes')
			FROM game_actions a WHERE a.status = 'running' GROUP BY a.action_type
			UNION ALL SELECT a.action_type, 'failed', count(*), MIN(a.finish_at), MAX(a.retry_count), 0
			FROM game_actions a WHERE a.status = 'failed' GROUP BY a.action_type`,
			Cols:    cols("type:code", "status:status", "actions:int", "overdue:int", "oldest_due:time", "max_retries:int"),
			Filters: []Filter{opts("status", "scheduled", "running", "failed")}, Order: "actions", Tie: "type"},
	)
}
