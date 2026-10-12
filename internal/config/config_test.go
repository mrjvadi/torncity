package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// repoConfig is the committed file, reached from this package's directory.
const repoConfig = "../../configs/config.yml"

// write puts a config file in a temporary directory and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the test config: %v", err)
	}
	return path
}

// mustLoad loads a config that is expected to be good.
func mustLoad(t *testing.T, body string) *Config {
	t.Helper()

	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}
	return cfg
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults() does not validate, so no service could start without a config file: %v", err)
	}
}

// TestCommittedFileMatchesDefaults is the check that keeps the two halves of
// this package honest. configs/config.yml holds the values the code used to
// hardcode, and Defaults() holds the same ones, so loading the file with no
// environment set must reproduce the defaults exactly. If it does not, one of
// the two drifted and an operator reading the file would be misled.
func TestCommittedFileMatchesDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(repoConfig)
	if err != nil {
		t.Fatalf("the committed configuration does not load: %v", err)
	}

	if !reflect.DeepEqual(cfg, Defaults()) {
		t.Errorf("configs/config.yml and Defaults() disagree\n file: %+v\n code: %+v", cfg, Defaults())
	}
}

func TestLoadRejectsBadPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want error
	}{
		{"empty path", "   ", ErrNoPath},
		{"missing file", filepath.Join(t.TempDir(), "absent.yml"), os.ErrNotExist},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.path)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Load(%q) error = %v, want one wrapping %v", tc.path, err, tc.want)
			}
		})
	}
}

// TestUnknownKeysAreRejected is the reason the decoder runs with
// KnownFields(true). A misspelled key that is merely ignored leaves the
// default in force while the file plainly shows the value the operator meant,
// and nothing anywhere reports the disagreement.
func TestUnknownKeysAreRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "misspelled key",
			yaml: "gateway:\n  pol_timeout: 30s\n",
			want: "pol_timeout",
		},
		{
			name: "key in the wrong section",
			yaml: "gateway:\n  batch_size: 100\n",
			want: "batch_size",
		},
		{
			name: "unknown section",
			yaml: "gatewy:\n  poll_timeout: 30s\n",
			want: "gatewy",
		},
		{
			name: "plausible but invented key",
			yaml: "nats:\n  max_redeliveries: 5\n",
			want: "max_redeliveries",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.yaml))
			if err == nil {
				t.Fatal("Load() accepted an unknown key; a value nobody reads is worse than a missing one")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name the offending key %q: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), "line") {
				t.Errorf("the error does not carry a line number: %v", err)
			}
		})
	}
}

// TestMalformedValuesNameTheField covers the other silent failure: a value
// that cannot be read must not become a zero. A zero duration is "no timeout"
// to every standard library API that takes one.
func TestMalformedValuesNameTheField(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name:  "duration with a bad unit",
			yaml:  "gateway:\n  poll_timeout: 3zz\n",
			field: "gateway.poll_timeout",
		},
		{
			name:  "duration with no unit at all",
			yaml:  "nats:\n  ack_wait: 30\n",
			field: "nats.ack_wait",
		},
		{
			name:  "empty duration",
			yaml:  "lease:\n  ttl: \"\"\n",
			field: "lease.ttl",
		},
		{
			name:  "malformed entry in a list",
			yaml:  "nats:\n  backoff:\n    - 1s\n    - later\n",
			field: "nats.backoff[1]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.yaml))
			if err == nil {
				t.Fatal("Load() accepted a malformed value")
			}
			if !errors.Is(err, ErrInvalidValue) {
				t.Errorf("error = %v, want one wrapping ErrInvalidValue", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("the error does not name the field %q: %v", tc.field, err)
			}
		})
	}
}

// TestWrongYAMLTypesAreRejected checks that the decoder, not this package, is
// the one catching a value of the wrong shape.
func TestWrongYAMLTypesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"mapping where a scalar belongs", "gateway:\n  send_attempts:\n    nested: 2\n"},
		{"text where a number belongs", "worker:\n  batch_size: many\n"},
		{"scalar where a section belongs", "lease: 30s\n"},
		{"scalar where a list belongs", "nats:\n  backoff: 1s\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(write(t, tc.yaml)); err == nil {
				t.Fatal("Load() accepted a value of the wrong type")
			}
		})
	}
}

func TestEmptyAndPartialFiles(t *testing.T) {
	clearEnv(t)

	t.Run("empty file keeps every default", func(t *testing.T) {
		if got := mustLoad(t, ""); !reflect.DeepEqual(got, Defaults()) {
			t.Errorf("an empty file changed the configuration: %+v", got)
		}
	})

	t.Run("a partial file leaves the rest alone", func(t *testing.T) {
		cfg := mustLoad(t, "worker:\n  batch_size: 42\n")

		if cfg.Worker.BatchSize != 42 {
			t.Errorf("BatchSize = %d, want 42", cfg.Worker.BatchSize)
		}
		if cfg.Worker.PollInterval != Defaults().Worker.PollInterval {
			t.Errorf("PollInterval = %s, want the default %s; a missing key must never become a zero",
				cfg.Worker.PollInterval, Defaults().Worker.PollInterval)
		}
		if cfg.Gateway.ShutdownTimeout != Defaults().Gateway.ShutdownTimeout {
			t.Errorf("an untouched section changed: %s", cfg.Gateway.ShutdownTimeout)
		}
	})
}

func TestMultipleDocumentsAreRejected(t *testing.T) {
	_, err := Load(write(t, "worker:\n  batch_size: 42\n---\nworker:\n  batch_size: 7\n"))
	if !errors.Is(err, ErrMultipleDocuments) {
		t.Fatalf("error = %v, want one wrapping ErrMultipleDocuments; only the first document would ever be read", err)
	}
}

// fullFile sets every single setting to a value that differs from its default,
// so a field the settings table forgot shows up as one that did not change.
const fullFile = `
gateway:
  poll_timeout: 11s
  poll_error_backoff: 3s
  shutdown_timeout: 21s
  send_attempts: 4
  redirect_cooldown: 90s
  webapp_private_cooldown: 7s
lease:
  ttl: 31s
  renew_divisor: 4
  release_timeout: 6s
ratelimit:
  default_rate: 26
  default_burst: 2
telegram:
  request_timeout: 29s
  max_poll_timeout: 119s
  poll_timeout_grace: 16s
  default_flood_wait: 6s
groups:
  callback_alert_max_runes: 150
  deep_link_ttl: 25m
menu:
  commands: [profile, map]
dedup:
  ttl: 25h
nats:
  command_max_age: 23h
  event_max_age: 700h
  duplicate_window: 3m
  ack_wait: 31s
  max_deliver: 6
  nak_delay: 6s
  backoff:
    - 2s
    - 6s
    - 16s
worker:
  poll_interval: 300ms
  batch_size: 101
  shutdown_timeout: 16s
  noisy_attempts: 6
scheduler:
  tick_interval: 2s
  batch_size: 102
  shutdown_timeout: 17s
  noisy_attempts: 7
  claim_timeout: 3m
notifier:
  send_budget: 14s
  receipt_margin: 4s
  max_age: 23h
  shutdown_timeout: 16s
game:
  shutdown_timeout: 22s
  idempotency_ttl: 23h
  content_reload_interval: 31s
  command_timeout: 31s
  time_scale: 61
  clock_epoch: "2026-02-01T00:00:00Z"
  clock_legacy_scale: 61
  clock_cutover: "2026-11-06T00:00:00Z"
  travel_time_scale: 62
travel:
  arrival_xp: 26
  city_locations: ["support=10.5:20.5"]
  world_reach: ["walk=61"]
player:
  default_language: "en"
  default_timezone: "Europe/Berlin"
economy:
  starting_cash: 5001
  bank_min_amount: 2
  bank_max_amount: 999
  bank_quick_amounts: [7, 70]
governance:
  fine_step_divisor: 101
  coarse_step_divisor: 11
  allocation_step_bps: 1000
crime:
  nerve_max: 21
  nerve_regen_amount: 2
  nerve_regen_interval: 6m
  heat_max: 101
  heat_decay_per_hour: 5
  protect_min_level: 4
  protect_min_age: 73h
  active_window: 31m
  arrival_linger: 21m
  victim_cooldown: 7h
  thief_cooldown: 25h
  report_window: 25h
  investigation_duration: 7h
  investigation_base_bps: 2501
  investigation_per_heat_bps: 41
  investigation_witness_bonus_bps: 3501
  investigation_effort_weight_bps: 3001
  npc_daily_cap: 500001
  gear_max_success_bps: 2501
  gear_max_catch_bps: 2501
  gear_max_witness_bps: 3001
  gear_max_solve_bps: 3001
  gear_max_reward_bps: 5001
  gear_max_nerve: 6
trade:
  market_order_ttl: 169h
  market_max_open_orders: 21
  village_stalls_post: 7
  village_stalls_hall: 21
  village_stalls_per_player_post: 4
  village_stalls_per_player_hall: 7
  market_day_every_days: 8
  stall_keeper_share_bps: 1100
  stall_keeper_share_min_bps: 510
  stall_keeper_share_max_bps: 2100
  stall_keeper_wage: 610
  stall_keeper_wage_min: 310
  stall_keeper_wage_max: 1510
  market_max_quantity: 10001
  market_max_price: 100000001
  auction_durations: [2h, 7h]
  auction_max_reserve: 100000001
  auction_step_bps: 501
  auction_min_step: 11
  auction_max_open: 6
  auction_reserves_bps: [4000, 9000]
company:
  period: 25h
  max_per_player: 3
  name_min_length: 4
  name_max_length: 25
  founding_shares: 1001
  insolvency_periods: 4
  npc_city_period_cap: 50001
  max_openings: 6
  price_step_bps: 1001
  citizen_shifts_per_period: 3
  citizen_productivity_bps: 7001
  citizen_labour_share_bps: 501
  max_running_orders: 4
  max_designs: 21
  max_listings: 11
  design_min_skill: 2
  quick_order_units: 6
  reverse_time: 5h
  improvement_time: 3h
  improvement_cost: 20001
  retrofit_time: 90m
  obsolescence_decay_bps: 801
  obsolescence_floor_bps: 3001
  recruit_check_every: 5h
  recruit_checks: 5
  recruit_max_campaigns: 3
  recruit_max_positions: 6
  recruit_max_candidates: 7
  recruit_patience: 23h
  recruit_max_staff: 11
military:
  period: 25h
  readiness_loss_bps: 1001
  readiness_recovery_bps: 501
  reference_radar_km: 151
  licence_revoke_notice: 25h
  ended_licences_shown: 6
diplomacy:
  sanction_notice: 2h
  sanction_min_duration: 25h
  treaty_offer_ttl: 73h
  ended_shown_for: 169h
  history_page_size: 9
war:
  declaration_notice: 25h
  proposal_ttl: 49h
  ended_shown_for: 169h
  board_operations: 9
  notice_cap: 201
missions:
  max_active: 6
  player_daily_cap: 5001
  economy_daily_cap: 1000001
factions:
  name_min_length: 4
  name_max_length: 25
  max_members: 31
  max_pending: 21
  min_founders: 22
  list_size: 11
anticheat:
  window: 25h
  one_way_count: 5
  one_way_min_total: 20001
  one_way_ratio_bps: 9001
  off_market_bps: 5001
  off_market_min_value: 5001
  single_partner_min_count: 7
  single_partner_share_bps: 9001
  commands_per_minute: 91
  wash_trade_count: 4
  hold_above: 50001
input:
  ttl: 7m
  cooldown: 4s
  max_length: 33
announce:
  window: 2m
  max_per_window: 7
  village_merge_window: 21s
  village_min_gap: 91s
  village_flush_interval: 6s
  village_literacy_step_percent: 11
notifications:
  inbox_page_size: 9
  edit_throttle: 11s
  reminder_delay: 30h
  reminder_check_interval: 20m
  retention: 800h
  prune_interval: 30h
  hunger_alert_cooldown: 3h
  vitals_min_interval: 4s
legislature:
  vote_window: 49h
  list_size: 9
city:
  period: 25h
property:
  foreclosure_periods: 4
  eviction_periods: 3
  max_owned: 6
  max_price: 100000001
  max_rent: 1000001
  rest_cooldown: 9h
achievements:
  player_daily_cap: 5001
  economy_daily_cap: 500001
postgres:
  max_conns: 17
  idle_in_transaction_timeout: 61s
panel:
  listen: 127.0.0.1:8091
  public_url: https://panel.example.test
  trusted_proxies: ["10.0.0.0/8"]
  session_idle: 31m
  session_absolute: 13h
  login_per_minute: 11
  lockout_after: 6
  lockout_base: 2m
  lockout_max: 2h
  mutations_per_minute: 31
  max_body_bytes: 65537
  idempotency_ttl: 25h
  password_min_length: 13
  totp_issuer: torncity-test
  request_timeout: 3m
  read_timeout: 9s
  export_max_rows: 10001
  moderation_cache_ttl: 31s
  switch_cache_ttl: 16s
  feed_interval: 4s
  kpi_interval: 31s
  realtime_api_url: http://centrifugo.test:8000/api
  realtime_websocket_url: wss://live.example.test/connection/websocket
  realtime_token_ttl: 11m
  nats_monitor_url: http://nats.test:8222
client:
  listen: 127.0.0.1:8082
  trusted_proxies: ["10.0.0.0/8"]
  access_ttl: 16m
  refresh_ttl: 721h
  link_code_ttl: 11m
  link_codes_per_hour: 6
  sign_ins_per_minute: 11
  max_devices: 11
  command_timeout: 16s
  commands_per_minute: 121
  max_body_bytes: 16385
  telegram_auth_max_age: 61m
  realtime_token_ttl: 16m
  group_commands: allow
  mini_app_url: https://play.example.test
  legacy_text_screens: [village_overview]
  chunk_cache_entries: 4097
  chunks_per_minute: 1201
  layouts_per_minute: 121
  world_recheck_interval: 31s
  photo_ttl: 7h
  photo_missing_ttl: 31m
  photos_per_minute: 7
realtime:
  api_url: http://realtime.example.test:8000/api
  publish_timeout: 3s
  presence_ttl: 31s
  roster_limit: 201
  settlement_event_ttl: 25h
state_sync:
  enabled: false
  epoch: "2"
  reset_threshold: 2001
  pull_limit: 501
  pulls_per_minute: 241
  command_wait: 301ms
  push_max_records: 101
  push_max_bytes: 32769
  max_event_age: 11m
  player_keys: [payee_id]
  fanout_limit: 501
  cause_window: 201
  notices_kept: 51
  lock_timeout: 3s
  retention_age: 169h
  retention_records: 5001
  retention_min: 501
  trim_interval: 61m
  trim_batch: 201
  metrics_interval: 6m
worldgen:
  cell_count: 41000
  neighbor_k: 7
  plate_count: 17
  oceanic_plate_fraction_permille: 551
  land_fraction_permille: 451
  noise_octaves: 7
  noise_base_frequency: 2.1
  noise_persistence_permille: 521
  warp_amplitude: 0.46
  warp_frequency: 1.11
  boundary_influence_steps: 10
  moisture_bands: 91
  river_flow_threshold: 13
  lake_min_depth: 41
  lake_min_area_cells: 21
  planet_radius_km: 6372
  chunk_base_lod: 11
  chunk_tile_edge: 33
  chunk_detail_frequency: 41
  chunk_detail_amplitude: 301
  chunk_stream_frequency: 61
  chunk_stream_amplitude: 61
  chunk_deposit_tiles_per_deposit: 6
households:
  rule_at: "2098-01-01T00:00:00Z"
  firewood_item: logs
  per_batch: 57
  catchup_days_max: 10
  arrivals_per_day: 9
  vacancy_margin_bps: 1007
  arrive_above_bps: 5007
  leave_below_bps: 3007
  leave_after_days: 12
  notice_days: 9
  satisfaction_alpha_bps: 2007
  start_satisfaction_bps: 7007
  birth_above_bps: 8007
  birth_chance_bps: 207
  dependant_weight_bps: 5007
  dependant_to_earner_days: 67
  earner_bps: 6007
  player_reserve_bps: 7007
  wallet_cap: 2007
  welcome_grant: 307
  welcome_floor: 1007
  food_points_per_person_day: 11
  water_milli_per_person: 1007
  firewood_milli_per_person: 507
  stock_days: 9
  tick_keep_days: 37
  clinic_chance_bps: 507
  weight_food: 3007
  weight_water: 2507
  weight_warmth: 1507
  weight_shelter: 1507
  weight_work: 1007
  weight_care: 507
  max_rent: 207
  welcome_quiet_days: 12
  surplus_spend_bps: 5007
  discretionary_items: ["rope"]
  grace_days: 21
  compositions: ["1+0:1"]
raid:
  rule_at: "2098-01-01T00:00:00Z"
  grace_days: 1010
  death_rule_at: "2098-01-01T00:00:00Z"
  death_grace_days: 1024
  min_lead_hours: 1031
  max_lead_hours: 1038
  window_minutes: 1045
  cooldown_hours: 1052
  max_per_week: 1059
  after_loss_shield_hours: 1066
  shield_loot_cut_bps: 1073
  founding_grace_days: 1080
  grace_ratio: 1087
  target_ratio_max: 1094
  min_target_value: 1101
  fee: 1108
  loot_treasury_bps: 1115
  loot_stack_bps: 1122
  damage_max_bps: 1129
  carry_per_raider: 1136
  npc_raider_wage: 1143
  max_npc_raiders: 1150
  seat_firepower: 1157
  seat_armour: 1164
  rounds: 1171
  defender_advantage_bps: 1178
  base_hit_bps: 1185
  hit_floor_bps: 1192
  hit_ceiling_bps: 1199
  death_cash_bps: 1206
  death_goods_bps: 1213
  gear_wear_bps: 1220
  loss_cap_bps: 1227
  fall_cooldown_hours: 1234
  stealth_bps_per_step: 1241
  stealth_steps: 1248
  stealth_floor_bps: 1255
  night_penalty_bps: 1262
  beacon_hop_minutes: 1269
  relay_m: 1276
  speed_foot_kmh: 1283
  speed_cart_kmh: 1290
  speed_horse_kmh: 1297
  speed_rail_kmh: 1304
  tribute_min: 1311
mining:
  rule_at: "2095-01-01T00:00:00Z"
  grace_days: 3007
  reserve_divisor: 3014
  surface_share_bps: 3021
  shaft1_share_bps: 3028
  shaft2_share_bps: 3035
  deep_share_bps: 3042
  thin_below_bps: 3049
  thin_floor_bps: 3056
  lean_below_permille: 3063
  rich_from_permille: 3070
  lean_out_bps: 3077
  medium_out_bps: 3084
  rich_out_bps: 3091
  shaft1_depth_m: 3098
  shaft2_depth_m: 3105
  hoist_windlass_kg_min: 3112
  hoist_gin_kg_min: 3119
  hoist_depth_ref_m: 3133
  hauler_kg_min: 3140
  unmade_way_bps: 3147
  adit_gradient: 3154
  min_condition_bps: 3161
  water_limit_bps: 3168
  wear_per_shift_bps: 3175
  wear_per_day_bps: 3182
  inflow_per_day_bps: 3189
  adit_outflow_bps: 3196
  pump_gin_outflow_bps: 3203
  set_props_gain_bps: 3217
  set_props_items: 3224
  works_shift_minutes: 3999
  works_shift_wage: 3231
  survey_fee: 3238
  min_surface_days: 3245
  ore_unit_g: 3252
  cart_broken_bps: 3259
  cart_speed_kmh: 3266
  road_link_lot_minutes: 3273
  road_link_lot_stone: 3280
  road_link_lot_hours: 3287
  rail_work_hour_wage: 3294
  rail_worn_bps: 3301
  rail_retry_minutes: 3998
  stuck_hours: 3308
  team_feed_wheat: 3315
  default_rate_per_tonne_km: 3322
  chain_default_per_hour: 3329
polity:
  rule_at: "2097-01-01T00:00:00Z"
  ratify_days: 5002
  exit_notice_days: 5003
  country_min_residents: 5004
  league_fee: 5005
  country_fee: 5006
  max_dues_bps: 5007
  weight_cap_bps: 5008
  members_majority_bps: 5009
  population_majority_bps: 5010
  vote_window_hours: 5011
  expel_after_periods: 5012
  dues_period_hours: 5013
  treaty_cooling_days: 5014
  tender_fee: 5015
  bond_bps: 5016
  retention_bps: 5017
  defects_days: 5018
  debar_days: 5019
  coi_refuse_from_bps: 5020
  tender_min_hours: 5021
  tender_grace_hours: 5022
  debt_default_after: 5023
  debt_late_fee_bps: 5024
  debt_service_cap_bps: 5025
  debt_max_instalments: 5026
  debt_period_hours: 5027
  debt_max_rate_bps: 5028
projects:
  rule_at: "2098-01-01T00:00:00Z"
  grace_days: 8001
  start_fee: 8002
  pledge_min: 8003
  cancel_before_stage: 8004
  max_open: 8007
  shift_minutes: 9006
  shift_hours: 9007
  wage_per_hour: 9008
  npc_wage_per_hour: 9009
  npc_hours_per_day: 9010
intel:
  rule_at: "2098-01-01T00:00:00Z"
  sabotage_rule_at: "2098-01-01T00:00:00Z"
  grace_days: 8008
  report_ttl_days: 8009
  op_fee: 8010
  agent_wage: 8011
  ops_per_day: 8012
  cover_cap_days: 8013
  cover_bps_per_day: 8014
  base_chance_bps: 8015
  skill_bps_per_level: 8016
  chance_floor_bps: 8017
  chance_cap_bps: 8018
  counter_post_bps: 8019
  counter_vet_bps: 8020
  counter_small_bps: 8021
  small_residents: 8022
  detect_bps: 8023
  attribution_bps: 8024
  expel_days: 8025
  sabotage_delay_hours: 8026
  sabotage_spoil_bps: 8027
  sabotage_blind_bps: 8028
  sabotage_weekly_limit: 8029
  sabotage_per_target_days: 8030
  sabotage_skip_days: 8031
  sabotage_cost: 8032
  skill_xp_per_op: 9014
licence:
  rule_at: "2098-01-01T00:00:00Z"
  grace_days: 8033
  auction_min_hours: 8034
  reserve_min: 8035
  transfer_fee_bps: 8036
  transfer_cooldown_days: 8037
  max_share_bps: 8038
  notice_days: 8039
  nonuse_days: 8040
  coi_share_bps: 8041
  listing_fee: 8042
  term_max_days: 8043
  share_cap_from: 9012
law:
  police_rule_at: "2098-01-01T00:00:00Z"
  courts_rule_at: "2098-01-01T00:00:00Z"
  black_rule_at: "2098-01-01T00:00:00Z"
  customs_rule_at: "2098-01-01T00:00:00Z"
  launder_rule_at: "2098-01-01T00:00:00Z"
  grace_days: 11001
  points_per_staff: 11002
  post_base_points: 11003
  station_base_points: 11004
  patrol_cost: 11005
  investigate_cost: 11006
  arrest_cost: 11007
  base_detect_bps: 11008
  patrol_detect_bps: 11009
  detect_cap_bps: 11010
  custody_hours: 11011
  complaint_fee: 11012
  warrant_threshold: 11013
  warrant_days: 11014
  improper_penalty: 11015
  exclusion_bps: 11016
  strength_witness: 11017
  strength_record: 11018
  strength_ledger_trace: 11019
  strength_seized_item: 11020
  strength_informant: 11021
  strength_confession: 11022
  strength_inspection: 11023
  strength_intel_trace: 11024
  weight_decay_bps: 11025
  hearing_delay_hours: 11026
  npc_threshold: 11027
  defence_strength: 11028
  defence_fee: 11029
  appeal_days: 11030
  appeal_fee: 11031
  bail_per_hour: 11032
  jail_food_per_day: 11033
  cells_per_warder: 11034
  jail_cells_base: 11035
  informant_pay: 11036
  reliability_start: 11037
  reliability_floor: 11038
  reliability_gain: 11039
  reliability_loss: 11040
  rotate_days: 11041
  max_informants: 11042
  retaliation_fine: 11043
  retaliation_ban_days: 11044
  deal_cut_bps: 11045
  bribe_min: 11046
  bribe_max_effect_bps: 11047
  bribe_term_days: 11048
  audit_cost: 11049
  audit_base_bps: 11050
  audit_age_bps_per_day: 11051
  audit_cap_bps: 11052
  captured_from: 11053
  standing_gain: 11054
  standing_intro: 11055
  black_premium_bps: 11056
  escrow_fee_bps: 11057
  escrow_timeout_hours: 11058
  broker_new_cap: 11059
  broker_cap_per_rep: 11060
  scam_rep_loss: 11061
  trace_bps: 11062
  bust_base_bps: 11063
  bust_patrol_bps: 11064
  listing_fee: 11065
  launder_fee_bps: 11066
  launder_cap_bps: 11067
  launder_window_days: 11068
  anomaly_bps: 11069
  launder_stage_hours: 11070
  launder_exposure_bps: 11071
  inspect_base_bps: 11072
  inspect_per_inspector_bps: 11073
  inspect_size_bps: 11074
  inspect_cap_bps: 11075
  customs_penalty_bps: 11076
  duty_bps: 11077
  police_share_bps: 11078
  racket_convictions: 11079
  gang_discover_bps: 11080
  witness_bps: 11081
  fact_window_days: 11082
  charge_threshold: 11083
  judge_wait_hours: 11084
  npc_sentence_bps: 11085
  appeal_margin: 11086
  burn_bps: 11087
  ask_bps: 11088
  gang_heat_per_conviction: 11089
growth:
  capabilities: off
  cache_ttl: 6s
  ruined_bps: 9001
  flush_interval: 31s
settlement:
  protection_window: 169h
  timezone_cooldown: 169h
  residence_cooldown: 73h
  home_city_code: hearth
  property_hub_min_stage: city
  min_spawn_distance_km: 31
  threat_radius_km: 151
  search_max_cells: 2001
  search_max_attempts: 201
  spawn_circle_radius_km: 301
  spawn_circle_capacity: 13
  spawn_circle_fill_band_km: 121
  spawn_circle_max_advance: 201
  building_area_per_cell: 7
  building_storey_timber_per_cell: 5
  building_storey_stone_per_cell: 7
  building_storey_stone_from: 4
  building_storey_shifts_per_cell: 5
  building_storey_knowledge: ["masonry=2"]
  building_salvage_bps: 3100
  use_change_fee_bps: 201
  building_look_rerolls: 9
  building_templates_max: 21
  excluded_biomes: [polar_ice, glacier]
  max_abs_latitude_deg: 66.5
  biome_penalties: ["desert=5", "tundra=7"]
  village_grid_lots: 6
  min_buildable_lot_share_bps: 6500
  grid_shift_max_lots: 4
  auto_road_cost: 11
  lot_access_crossing_cost: 61
  lot_access_max_crossing: 3
  street_pitch: 6
  street_plan_min_grid: 13
  road_frontage_depth_lots: 4
  road_plan_max_lots: 1501
  road_open_lots_max: 30001
  road_foreign_buffer_tiles: 4
  road_steep_slope_m: 31
  road_corridor_ring_tiles: 2
  road_track_cost_bps: 15001
  founding_draft_ttl: 31m
  founding_name_min: 4
  founding_name_max: 25
  founding_motto_max: 61
  founding_currency_name_min: 4
  founding_currency_name_max: 25
  founding_currency_code_len: 4
  founding_currency_symbol_max: 4
  teach_period: 25h
  teach_rate_bps: 1501
  base_school_capacity_bps: 8001
  scarcity_k_bps: 10001
  scarcity_floor_bps: 3001
  scarcity_cap_bps: 80001
  seller_band_bps: 501
  demolition_salvage_bps: 2001
  material_markup_bps: 12001
  stock_base_capacity: 61
  build_homes_per_crew: 18
  charter_max_offices: 125
  charter_max_seats: 116
  charter_max_permissions: 141
  charter_title_min: 103
  charter_title_max: 133
  charter_election_term_days: 114
  charter_candidacy_hours: 148
  charter_voting_hours: 172
  charter_recall_min_tenure_days: 105
  charter_recall_signature_bps: 2100
  charter_recall_min_signatures: 103
  charter_recall_vote_hours: 172
  charter_recall_cooldown_days: 114
  charter_amend_vote_hours: 172
  charter_amend_quorum_bps: 3100
  charter_amend_vote_min_residents: 106
  charter_acting_days: 107
  charter_acting_spend_cap: 2100
  charter_min_residency_days: 103
  storage_spoil_kept_bps: 6
  storage_spoil_unkept_bps: 31
  storage_keeper_rule_at: "2026-11-01T00:00:00Z"
  storage_keeper_grace_days: 21
  real_items_rule_at: "2026-11-02T00:00:00Z"
  real_items_grace_days: 9
  tool_bare_hands_bps: 5500
  service_rule_at: "2026-11-03T00:00:00Z"
  service_grace_days: 8
  personal_rule_at: "2026-11-04T00:00:00Z"
  personal_grace_days: 9
  land_rule_at: "2026-11-05T00:00:00Z"
  land_grace_days: 108
  land_woodland_ring: 104
  land_regrow_hours: 221
  land_sapling_hours: 125
  land_fell_radius: 105
  land_quarry_radius: 104
  land_rock_shifts: 103
  zone_max_cells: 401
  zoning_rule_at: "2026-12-10T00:00:00Z"
  zoning_grace_days: 301
  slope_rule_at: "2026-12-11T00:00:00Z"
  slope_grace_days: 302
  slope_flat_m: 9
  slope_gentle_m: 21
  slope_unusable_m: 31
  parcel_max_cells: 61
  parcel_change_fee: 51
  zone_open_fee: 21
  zone_notice_minutes: 25
  foundation_stone: 5
  foundation_wage: 61
  claim_cost: 2001
  claim_upkeep: 6
  claim_max_tiles: 201
  farm_rule_at: "2026-11-06T00:00:00Z"
  farm_grace_days: 109
  crafting_rule_at: "2026-11-07T00:00:00Z"
  crafting_grace_days: 110
  care_rule_at: "2026-11-08T00:00:00Z"
  care_grace_days: 111
  survival_rule_at: "2026-11-09T00:00:00Z"
  survival_grace_days: 112
  teaching_rule_at: "2026-11-10T00:00:00Z"
  teaching_grace_days: 113
  teacher_base_bps: 6100
  teacher_per_level_bps: 151
  teacher_xp_per_class: 21
  research_free_slots: 200
  research_speed_floor_bps: 201
  research_scholar_floor_bps: 202
  research_skill_bps_per_level: 203
  research_scholar_cap_bps: 204
  research_npc_scholar_level: 205
  research_literacy_bonus_bps: 206
  research_catch_up_bps: 207
  research_era_base_depth: 208
  research_era_share_bps: 209
  research_ahead_per_step_bps: 210
  research_ahead_cap_bps: 211
  research_share_per_partner_bps: 212
  research_share_cap_bps: 213
  research_breakthrough_need_per_depth: 214
  research_breakthrough_max_bps: 215
  research_experience_per_shift: 216
  research_scholar_xp: 217
  export_price_bps: 9001
  export_cap_base: 401
  export_cap_per_resident: 41
  export_keep_presets: [11, 26, 51]
  material_buy_max: 201
  material_buy_presets: [6, 21, 51]
  founding_grant: 10001
  donation_min: 101
  donation_max: 100001
  donation_presets: [251, 1001, 5001]
  citizen_lot_price: 401
  citizen_lot_price_min: 101
  citizen_lot_price_max: 5001
  citizen_permit_fee: 101
  citizen_permit_fee_max: 1001
  citizen_tax_bps: 201
  citizen_tax_bps_max: 501
  citizen_tax_period: 25h
  citizen_material_markup_bps: 12001
  citizen_max_lots_per_player: 7
  citizen_private_share_max_bps: 6001
  citizen_home_rest_cooldown: 7h
  citizen_home_rest_health: 11
  citizen_home_rest_happiness: 6
education:
  teacher_wage_bps: 7001
  teacher_min_wage: 41
  teacher_max_students: 13
training:
  energy_cost: 11
  stamina_gain: 7
  strength_xp: 31
  diminish_stamina: 401
  stamina_per_max_energy: 51
  max_energy_bonus_cap: 31
  yard_bps: 4001
  ground_bps: 6001
  gym_bps: 10001
  ground_fee: 21
  gym_fee: 61
currency:
  charter_r0: 11
  charter_fee: 1001
  charter_min_deposit: 5001
  mint_fee_bps: 51
  auto_charter_share_bps: 5003
  auto_charter_floor: 501
  desk_slippage_bps: 101
  desk_presets: [101, 501, 2001]
  fx_reserve_fee_bps: 31
  fx_max_move_bps: 2001
  fx_min_trades: 9
  fx_window_periods: 8
  fx_min_order_sup: 11
  fx_book_limit: 201
  fx_max_open_orders: 21
  fx_convert_slippage_bps: 101
  fx_order_ttl: 169h
  fx_period: 25h
  fx_unit_presets: [1001, 5001, 20001]
  reserve_gold_haircut_bps: 1001
  reserve_withdraw_notice_hours: 73
  reserve_policy_rate_bps: 1201
  intervention_cap_bps: 801
  intervention_pot_floor_bps: 3001
  wind_down_days: 61
  macro_m_norm_bps: 2001
  macro_kappa_bps: 201
  macro_pi_max_bps: 301
  macro_w_tradable_bps: 6001
  intervention_delay: 25h
labor:
  shift_minutes: 61
  shift_real_minutes: 3
  reference_crew: 5
  base_wage: 31
  min_wage: 12
  min_wage_village: 11
  min_wage_town: 16
  min_wage_city: 26
  participation_bps: 6001
  base_housing: 9
  npc_productivity_bps: 8501
  fee_bps: 501
  budget_slack_bps: 5001
  npc_hours_per_slot_day: 11
  hungry_output_bps: 5001
  hungry_shift_hunger: 6
  repair_material_share_bps: 2001
  repair_shifts_full: 11
  worn_output_bps: 7501
  closed_bps: 2501
  worn_bps: 5003
  repair_below_bps: 7001
  decay_bps_per_day: 51
  journeyman_shifts: 7
  master_shifts: 31
  apprentice_bps: 7001
  journeyman_bps: 10001
  master_bps: 13001
  tight_balanced_bps: 5001
  tight_tight_bps: 10001
  tight_short_bps: 20001
  wage_slack_bps: 7001
  wage_balanced_bps: 10001
  wage_tight_bps: 15001
  wage_short_bps: 25001
  hire_presets: [2, 3, 5]
  wage_presets: [101, 126, 151, 201]
bag:
  carry_base: 9
  base_comfort_kg: 8
  base_hard_kg: 21
  full_share_bps: 5001
  torn_space_bps: 5001
  repair_share_bps: 2501
  wear_per_day: 2
merchant:
  restock_hour: 7
  markup_min_bps: 10001
  markup_max_bps: 15001
  stock_days: 3
  food_share_bps: 4001
  other_share_bps: 3001
  player_day_food: 4
  player_day_other: 3
  supply_value_per_resident_day: 61
  building_boost_bps: 15001
  cap_presets: [10001, 11001]
  buy_presets: [2, 4]
  tax_default_bps: 301
  tax_max_bps: 1501
  tax_presets: [1, 301]
  output_days: 8
premium:
  nil_unit_sup: 101
  nil_examples: [11, 111]
`

// envOverrides is the same exercise through the environment. Every entry is a
// different value again, so a field reachable from the file but not from the
// environment is caught too.
var envOverrides = map[string]string{
	"TORN_GATEWAY_POLL_TIMEOUT":            "12s",
	"TORN_GATEWAY_POLL_ERROR_BACKOFF":      "4s",
	"TORN_GATEWAY_SHUTDOWN_TIMEOUT":        "23s",
	"TORN_GATEWAY_SEND_ATTEMPTS":           "5",
	"TORN_GATEWAY_REDIRECT_COOLDOWN":       "91s",
	"TORN_GATEWAY_WEBAPP_PRIVATE_COOLDOWN": "8s",

	"TORN_LEASE_TTL":             "33s",
	"TORN_LEASE_RENEW_DIVISOR":   "5",
	"TORN_LEASE_RELEASE_TIMEOUT": "7s",

	"TORN_RATELIMIT_DEFAULT_RATE":  "27",
	"TORN_RATELIMIT_DEFAULT_BURST": "3",

	"TORN_TELEGRAM_REQUEST_TIMEOUT":    "28s",
	"TORN_TELEGRAM_MAX_POLL_TIMEOUT":   "118s",
	"TORN_TELEGRAM_POLL_TIMEOUT_GRACE": "17s",
	"TORN_TELEGRAM_DEFAULT_FLOOD_WAIT": "7s",

	"TORN_GROUPS_CALLBACK_ALERT_MAX_RUNES": "160",
	"TORN_GROUPS_DEEP_LINK_TTL":            "20m",
	"TORN_MENU_COMMANDS":                   "profile, skills",

	"TORN_DEDUP_TTL": "26h",

	"TORN_NATS_COMMAND_MAX_AGE":  "22h",
	"TORN_NATS_EVENT_MAX_AGE":    "690h",
	"TORN_NATS_DUPLICATE_WINDOW": "4m",
	"TORN_NATS_ACK_WAIT":         "32s",
	"TORN_NATS_MAX_DELIVER":      "7",
	"TORN_NATS_NAK_DELAY":        "7s",
	"TORN_NATS_BACKOFF":          "3s,7s,17s",

	"TORN_WORKER_POLL_INTERVAL":       "400ms",
	"TORN_WORKER_BATCH_SIZE":          "102",
	"TORN_WORKER_SHUTDOWN_TIMEOUT":    "17s",
	"TORN_WORKER_NOISY_ATTEMPTS":      "7",
	"TORN_SCHEDULER_TICK_INTERVAL":    "3s",
	"TORN_SCHEDULER_BATCH_SIZE":       "103",
	"TORN_SCHEDULER_SHUTDOWN_TIMEOUT": "37s",
	"TORN_SCHEDULER_NOISY_ATTEMPTS":   "9",
	"TORN_SCHEDULER_CLAIM_TIMEOUT":    "4m",

	"TORN_NOTIFIER_SEND_BUDGET":      "13s",
	"TORN_NOTIFIER_RECEIPT_MARGIN":   "5s",
	"TORN_NOTIFIER_MAX_AGE":          "22h",
	"TORN_NOTIFIER_SHUTDOWN_TIMEOUT": "17s",

	"TORN_GAME_SHUTDOWN_TIMEOUT":                "24s",
	"TORN_GAME_IDEMPOTENCY_TTL":                 "22h",
	"TORN_GAME_CONTENT_RELOAD_INTERVAL":         "32s",
	"TORN_GAME_COMMAND_TIMEOUT":                 "32s",
	"TORN_POSTGRES_MAX_CONNS":                   "18",
	"TORN_POSTGRES_IDLE_IN_TRANSACTION_TIMEOUT": "62s",

	"TORN_TRAVEL_ARRIVAL_XP":     "27",
	"TORN_TRAVEL_CITY_LOCATIONS": "support=11.5:21.5",
	"TORN_TRAVEL_WORLD_REACH":    "walk=62, cart=501",
	// The legacy spelling of the game clock; TORN_GAME_TIME_SCALE wins.
	"TORN_TRAVEL_TIME_SCALE": "62",
	"TORN_GAME_TIME_SCALE":   "63",

	"TORN_PLAYER_DEFAULT_LANGUAGE": "de",
	"TORN_PLAYER_DEFAULT_TIMEZONE": "Asia/Tokyo",

	"TORN_ECONOMY_STARTING_CASH":      "5002",
	"TORN_ECONOMY_BANK_MIN_AMOUNT":    "3",
	"TORN_ECONOMY_BANK_MAX_AMOUNT":    "998",
	"TORN_ECONOMY_BANK_QUICK_AMOUNTS": "8,80",

	"TORN_INPUT_TTL":        "8m",
	"TORN_INPUT_COOLDOWN":   "5s",
	"TORN_INPUT_MAX_LENGTH": "34",

	"TORN_ANNOUNCE_WINDOW":         "3m",
	"TORN_ANNOUNCE_MAX_PER_WINDOW": "8",

	"TORN_ANNOUNCE_VILLAGE_MERGE_WINDOW":          "22s",
	"TORN_ANNOUNCE_VILLAGE_MIN_GAP":               "92s",
	"TORN_ANNOUNCE_VILLAGE_FLUSH_INTERVAL":        "7s",
	"TORN_ANNOUNCE_VILLAGE_LITERACY_STEP_PERCENT": "12",

	"TORN_NOTIFICATIONS_INBOX_PAGE_SIZE":         "10",
	"TORN_NOTIFICATIONS_EDIT_THROTTLE":           "12s",
	"TORN_NOTIFICATIONS_REMINDER_DELAY":          "31h",
	"TORN_NOTIFICATIONS_REMINDER_CHECK_INTERVAL": "21m",
	"TORN_NOTIFICATIONS_RETENTION":               "810h",
	"TORN_NOTIFICATIONS_PRUNE_INTERVAL":          "31h",
	"TORN_NOTIFICATIONS_HUNGER_ALERT_COOLDOWN":   "4h",
	"TORN_NOTIFICATIONS_VITALS_MIN_INTERVAL":     "5s",

	"TORN_GOVERNANCE_FINE_STEP_DIVISOR":   "102",
	"TORN_GOVERNANCE_COARSE_STEP_DIVISOR": "12",
	"TORN_GOVERNANCE_ALLOCATION_STEP_BPS": "2000",

	"TORN_CRIME_NERVE_MAX":                       "22",
	"TORN_CRIME_NERVE_REGEN_AMOUNT":              "3",
	"TORN_CRIME_NERVE_REGEN_INTERVAL":            "7m",
	"TORN_CRIME_HEAT_MAX":                        "102",
	"TORN_CRIME_HEAT_DECAY_PER_HOUR":             "6",
	"TORN_CRIME_PROTECT_MIN_LEVEL":               "5",
	"TORN_CRIME_PROTECT_MIN_AGE":                 "74h",
	"TORN_CRIME_ACTIVE_WINDOW":                   "32m",
	"TORN_CRIME_ARRIVAL_LINGER":                  "22m",
	"TORN_CRIME_VICTIM_COOLDOWN":                 "8h",
	"TORN_CRIME_THIEF_COOLDOWN":                  "26h",
	"TORN_CRIME_REPORT_WINDOW":                   "26h",
	"TORN_CRIME_INVESTIGATION_DURATION":          "8h",
	"TORN_CRIME_INVESTIGATION_BASE_BPS":          "2502",
	"TORN_CRIME_INVESTIGATION_PER_HEAT_BPS":      "42",
	"TORN_CRIME_INVESTIGATION_WITNESS_BONUS_BPS": "3502",
	"TORN_CRIME_INVESTIGATION_EFFORT_WEIGHT_BPS": "3002",
	"TORN_CRIME_NPC_DAILY_CAP":                   "500002",
	"TORN_CRIME_GEAR_MAX_SUCCESS_BPS":            "2502",
	"TORN_CRIME_GEAR_MAX_CATCH_BPS":              "2502",
	"TORN_CRIME_GEAR_MAX_WITNESS_BPS":            "3002",
	"TORN_CRIME_GEAR_MAX_SOLVE_BPS":              "3002",
	"TORN_CRIME_GEAR_MAX_REWARD_BPS":             "5002",
	"TORN_CRIME_GEAR_MAX_NERVE":                  "7",

	"TORN_TRADE_MARKET_ORDER_TTL":               "170h",
	"TORN_TRADE_MARKET_MAX_OPEN_ORDERS":         "22",
	"TORN_TRADE_VILLAGE_STALLS_POST":            "8",
	"TORN_TRADE_VILLAGE_STALLS_HALL":            "22",
	"TORN_TRADE_VILLAGE_STALLS_PER_PLAYER_POST": "5",
	"TORN_TRADE_VILLAGE_STALLS_PER_PLAYER_HALL": "8",
	"TORN_TRADE_MARKET_DAY_EVERY_DAYS":          "9",
	"TORN_TRADE_STALL_KEEPER_SHARE_BPS":         "1200",
	"TORN_TRADE_STALL_KEEPER_SHARE_MIN_BPS":     "520",
	"TORN_TRADE_STALL_KEEPER_SHARE_MAX_BPS":     "2200",
	"TORN_TRADE_STALL_KEEPER_WAGE":              "620",
	"TORN_TRADE_STALL_KEEPER_WAGE_MIN":          "320",
	"TORN_TRADE_STALL_KEEPER_WAGE_MAX":          "1520",
	"TORN_TRADE_MARKET_MAX_QUANTITY":            "10002",
	"TORN_TRADE_MARKET_MAX_PRICE":               "100000002",
	"TORN_TRADE_AUCTION_DURATIONS":              "3h,8h",
	"TORN_TRADE_AUCTION_MAX_RESERVE":            "100000002",
	"TORN_TRADE_AUCTION_STEP_BPS":               "502",
	"TORN_TRADE_AUCTION_MIN_STEP":               "12",
	"TORN_TRADE_AUCTION_MAX_OPEN":               "7",
	"TORN_TRADE_AUCTION_RESERVES_BPS":           "3000,8000",

	"TORN_COMPANY_PERIOD":                     "26h",
	"TORN_COMPANY_MAX_PER_PLAYER":             "4",
	"TORN_COMPANY_NAME_MIN_LENGTH":            "5",
	"TORN_COMPANY_NAME_MAX_LENGTH":            "26",
	"TORN_COMPANY_FOUNDING_SHARES":            "1002",
	"TORN_COMPANY_INSOLVENCY_PERIODS":         "5",
	"TORN_COMPANY_NPC_CITY_PERIOD_CAP":        "50002",
	"TORN_COMPANY_MAX_OPENINGS":               "7",
	"TORN_COMPANY_PRICE_STEP_BPS":             "1002",
	"TORN_COMPANY_CITIZEN_SHIFTS_PER_PERIOD":  "4",
	"TORN_COMPANY_CITIZEN_PRODUCTIVITY_BPS":   "7002",
	"TORN_COMPANY_CITIZEN_LABOUR_SHARE_BPS":   "502",
	"TORN_COMPANY_MAX_RUNNING_ORDERS":         "5",
	"TORN_COMPANY_MAX_DESIGNS":                "22",
	"TORN_COMPANY_MAX_LISTINGS":               "12",
	"TORN_COMPANY_DESIGN_MIN_SKILL":           "3",
	"TORN_COMPANY_QUICK_ORDER_UNITS":          "7",
	"TORN_COMPANY_REVERSE_TIME":               "7h",
	"TORN_COMPANY_IMPROVEMENT_TIME":           "5h",
	"TORN_COMPANY_IMPROVEMENT_COST":           "20002",
	"TORN_COMPANY_RETROFIT_TIME":              "3h",
	"TORN_COMPANY_OBSOLESCENCE_DECAY_BPS":     "802",
	"TORN_COMPANY_OBSOLESCENCE_FLOOR_BPS":     "3002",
	"TORN_COMPANY_RECRUIT_CHECK_EVERY":        "7h",
	"TORN_COMPANY_RECRUIT_CHECKS":             "6",
	"TORN_COMPANY_RECRUIT_MAX_CAMPAIGNS":      "4",
	"TORN_COMPANY_RECRUIT_MAX_POSITIONS":      "7",
	"TORN_COMPANY_RECRUIT_MAX_CANDIDATES":     "8",
	"TORN_COMPANY_RECRUIT_PATIENCE":           "22h",
	"TORN_COMPANY_RECRUIT_MAX_STAFF":          "12",
	"TORN_MILITARY_PERIOD":                    "26h",
	"TORN_MILITARY_READINESS_LOSS_BPS":        "1002",
	"TORN_MILITARY_READINESS_RECOVERY_BPS":    "502",
	"TORN_MILITARY_REFERENCE_RADAR_KM":        "152",
	"TORN_MILITARY_LICENCE_REVOKE_NOTICE":     "26h",
	"TORN_MILITARY_ENDED_LICENCES_SHOWN":      "7",
	"TORN_DIPLOMACY_SANCTION_NOTICE":          "3h",
	"TORN_DIPLOMACY_SANCTION_MIN_DURATION":    "26h",
	"TORN_DIPLOMACY_TREATY_OFFER_TTL":         "74h",
	"TORN_DIPLOMACY_ENDED_SHOWN_FOR":          "170h",
	"TORN_DIPLOMACY_HISTORY_PAGE_SIZE":        "10",
	"TORN_WAR_DECLARATION_NOTICE":             "26h",
	"TORN_WAR_PROPOSAL_TTL":                   "50h",
	"TORN_WAR_ENDED_SHOWN_FOR":                "170h",
	"TORN_WAR_BOARD_OPERATIONS":               "10",
	"TORN_WAR_NOTICE_CAP":                     "202",
	"TORN_MISSIONS_MAX_ACTIVE":                "7",
	"TORN_MISSIONS_PLAYER_DAILY_CAP":          "5002",
	"TORN_MISSIONS_ECONOMY_DAILY_CAP":         "1000002",
	"TORN_FACTIONS_NAME_MIN_LENGTH":           "5",
	"TORN_FACTIONS_NAME_MAX_LENGTH":           "26",
	"TORN_FACTIONS_MAX_MEMBERS":               "32",
	"TORN_FACTIONS_MAX_PENDING":               "22",
	"TORN_FACTIONS_MIN_FOUNDERS":              "23",
	"TORN_FACTIONS_LIST_SIZE":                 "12",
	"TORN_ANTICHEAT_WINDOW":                   "26h",
	"TORN_ANTICHEAT_ONE_WAY_COUNT":            "6",
	"TORN_ANTICHEAT_ONE_WAY_MIN_TOTAL":        "20002",
	"TORN_ANTICHEAT_ONE_WAY_RATIO_BPS":        "9002",
	"TORN_ANTICHEAT_OFF_MARKET_BPS":           "5002",
	"TORN_ANTICHEAT_OFF_MARKET_MIN_VALUE":     "5002",
	"TORN_ANTICHEAT_SINGLE_PARTNER_MIN_COUNT": "8",
	"TORN_ANTICHEAT_SINGLE_PARTNER_SHARE_BPS": "9002",
	"TORN_ANTICHEAT_COMMANDS_PER_MINUTE":      "92",
	"TORN_ANTICHEAT_WASH_TRADE_COUNT":         "5",
	"TORN_ANTICHEAT_HOLD_ABOVE":               "50002",
	"TORN_LEGISLATURE_VOTE_WINDOW":            "50h",
	"TORN_LEGISLATURE_LIST_SIZE":              "10",
	"TORN_CITY_PERIOD":                        "26h",
	"TORN_PROPERTY_FORECLOSURE_PERIODS":       "5",
	"TORN_PROPERTY_EVICTION_PERIODS":          "4",
	"TORN_PROPERTY_MAX_OWNED":                 "7",
	"TORN_PROPERTY_MAX_PRICE":                 "100000002",
	"TORN_PROPERTY_MAX_RENT":                  "1000002",
	"TORN_PROPERTY_REST_COOLDOWN":             "10h",
	"TORN_ACHIEVEMENTS_PLAYER_DAILY_CAP":      "5002",
	"TORN_ACHIEVEMENTS_ECONOMY_DAILY_CAP":     "500002",
	"TORN_PANEL_LISTEN":                       "127.0.0.1:8092",
	"TORN_PANEL_PUBLIC_URL":                   "https://panel2.example.test",
	"TORN_PANEL_TRUSTED_PROXIES":              "172.16.0.0/12,10.0.0.0/8",
	"TORN_PANEL_SESSION_IDLE":                 "32m",
	"TORN_PANEL_SESSION_ABSOLUTE":             "14h",
	"TORN_PANEL_LOGIN_PER_MINUTE":             "12",
	"TORN_PANEL_LOCKOUT_AFTER":                "7",
	"TORN_PANEL_LOCKOUT_BASE":                 "3m",
	"TORN_PANEL_LOCKOUT_MAX":                  "3h",
	"TORN_PANEL_MUTATIONS_PER_MINUTE":         "32",
	"TORN_PANEL_MAX_BODY_BYTES":               "65538",
	"TORN_PANEL_IDEMPOTENCY_TTL":              "26h",
	"TORN_PANEL_PASSWORD_MIN_LENGTH":          "14",
	"TORN_PANEL_TOTP_ISSUER":                  "torncity-env",
	"TORN_PANEL_REQUEST_TIMEOUT":              "4m",
	"TORN_PANEL_READ_TIMEOUT":                 "10s",
	"TORN_PANEL_EXPORT_MAX_ROWS":              "10002",
	"TORN_PANEL_MODERATION_CACHE_TTL":         "32s",
	"TORN_PANEL_SWITCH_CACHE_TTL":             "17s",
	"TORN_PANEL_FEED_INTERVAL":                "5s",
	"TORN_PANEL_KPI_INTERVAL":                 "32s",
	"TORN_PANEL_REALTIME_API_URL":             "http://centrifugo.env:8000/api",
	"TORN_PANEL_REALTIME_WEBSOCKET_URL":       "/ws",
	"TORN_PANEL_REALTIME_TOKEN_TTL":           "12m",
	"TORN_PANEL_NATS_MONITOR_URL":             "http://nats.env:8222",
	"TORN_CLIENT_LISTEN":                      "127.0.0.1:8083",
	"TORN_CLIENT_TRUSTED_PROXIES":             "172.16.0.0/12",
	"TORN_CLIENT_ACCESS_TTL":                  "17m",
	"TORN_CLIENT_REFRESH_TTL":                 "722h",
	"TORN_CLIENT_LINK_CODE_TTL":               "12m",
	"TORN_CLIENT_LINK_CODES_PER_HOUR":         "7",
	"TORN_CLIENT_SIGN_INS_PER_MINUTE":         "12",
	"TORN_CLIENT_MAX_DEVICES":                 "12",
	"TORN_CLIENT_COMMAND_TIMEOUT":             "17s",
	"TORN_CLIENT_COMMANDS_PER_MINUTE":         "122",
	"TORN_CLIENT_MAX_BODY_BYTES":              "16386",
	"TORN_CLIENT_TELEGRAM_AUTH_MAX_AGE":       "62m",
	"TORN_CLIENT_REALTIME_TOKEN_TTL":          "17m",
	"TORN_CLIENT_GROUP_COMMANDS":              "allow",
	"TORN_CLIENT_MINI_APP_URL":                "https://play2.example.test",
	"TORN_CLIENT_LEGACY_TEXT_SCREENS":         "village_overview,labor_board",
	"TORN_CLIENT_CHUNK_CACHE_ENTRIES":         "4098",
	"TORN_CLIENT_CHUNKS_PER_MINUTE":           "1202",
	"TORN_CLIENT_LAYOUTS_PER_MINUTE":          "122",
	"TORN_CLIENT_WORLD_RECHECK_INTERVAL":      "32s",
	"TORN_CLIENT_PHOTO_TTL":                   "8h",
	"TORN_CLIENT_PHOTO_MISSING_TTL":           "32m",
	"TORN_CLIENT_PHOTOS_PER_MINUTE":           "8",
	"TORN_REALTIME_API_URL":                   "http://realtime2.example.test:8000/api",
	"TORN_REALTIME_PUBLISH_TIMEOUT":           "4s",
	"TORN_REALTIME_PRESENCE_TTL":              "32s",
	"TORN_REALTIME_ROSTER_LIMIT":              "202",
	"TORN_REALTIME_SETTLEMENT_EVENT_TTL":      "26h",

	"TORN_STATE_SYNC_ENABLED":           "false",
	"TORN_STATE_SYNC_EPOCH":             "3",
	"TORN_STATE_SYNC_RESET_THRESHOLD":   "2002",
	"TORN_STATE_SYNC_PULL_LIMIT":        "502",
	"TORN_STATE_SYNC_PULLS_PER_MINUTE":  "242",
	"TORN_STATE_SYNC_COMMAND_WAIT":      "302ms",
	"TORN_STATE_SYNC_PUSH_MAX_RECORDS":  "102",
	"TORN_STATE_SYNC_PUSH_MAX_BYTES":    "32770",
	"TORN_STATE_SYNC_MAX_EVENT_AGE":     "12m",
	"TORN_STATE_SYNC_PLAYER_KEYS":       "payer_id",
	"TORN_STATE_SYNC_FANOUT_LIMIT":      "502",
	"TORN_STATE_SYNC_CAUSE_WINDOW":      "202",
	"TORN_STATE_SYNC_NOTICES_KEPT":      "52",
	"TORN_STATE_SYNC_LOCK_TIMEOUT":      "4s",
	"TORN_STATE_SYNC_RETENTION_AGE":     "170h",
	"TORN_STATE_SYNC_RETENTION_RECORDS": "5002",
	"TORN_STATE_SYNC_RETENTION_MIN":     "502",
	"TORN_STATE_SYNC_TRIM_INTERVAL":     "62m",
	"TORN_STATE_SYNC_TRIM_BATCH":        "202",
	"TORN_STATE_SYNC_METRICS_INTERVAL":  "7m",

	"TORN_WORLDGEN_CELL_COUNT":                      "42000",
	"TORN_WORLDGEN_NEIGHBOR_K":                      "8",
	"TORN_WORLDGEN_PLATE_COUNT":                     "18",
	"TORN_WORLDGEN_OCEANIC_PLATE_FRACTION_PERMILLE": "552",
	"TORN_WORLDGEN_LAND_FRACTION_PERMILLE":          "452",
	"TORN_WORLDGEN_NOISE_OCTAVES":                   "8",
	"TORN_WORLDGEN_NOISE_BASE_FREQUENCY":            "2.2",
	"TORN_WORLDGEN_NOISE_PERSISTENCE_PERMILLE":      "522",
	"TORN_WORLDGEN_WARP_AMPLITUDE":                  "0.47",
	"TORN_WORLDGEN_WARP_FREQUENCY":                  "1.12",
	"TORN_WORLDGEN_BOUNDARY_INFLUENCE_STEPS":        "11",
	"TORN_WORLDGEN_MOISTURE_BANDS":                  "92",
	"TORN_WORLDGEN_RIVER_FLOW_THRESHOLD":            "14",
	"TORN_WORLDGEN_LAKE_MIN_DEPTH":                  "42",
	"TORN_WORLDGEN_LAKE_MIN_AREA_CELLS":             "22",
	"TORN_WORLDGEN_PLANET_RADIUS_KM":                "6373",
	"TORN_WORLDGEN_CHUNK_BASE_LOD":                  "12",
	"TORN_WORLDGEN_CHUNK_TILE_EDGE":                 "34",
	"TORN_WORLDGEN_CHUNK_DETAIL_FREQUENCY":          "42",
	"TORN_WORLDGEN_CHUNK_DETAIL_AMPLITUDE":          "302",
	"TORN_WORLDGEN_CHUNK_STREAM_FREQUENCY":          "62",
	"TORN_WORLDGEN_CHUNK_STREAM_AMPLITUDE":          "62",
	"TORN_WORLDGEN_CHUNK_DEPOSIT_TILES_PER_DEPOSIT": "7",

	"TORN_HOUSEHOLDS_RULE_AT":                    "2097-01-01T00:00:00Z",
	"TORN_HOUSEHOLDS_FIREWOOD_ITEM":              "kindling",
	"TORN_HOUSEHOLDS_PER_BATCH":                  "59",
	"TORN_HOUSEHOLDS_CATCHUP_DAYS_MAX":           "12",
	"TORN_HOUSEHOLDS_ARRIVALS_PER_DAY":           "11",
	"TORN_HOUSEHOLDS_VACANCY_MARGIN_BPS":         "1009",
	"TORN_HOUSEHOLDS_ARRIVE_ABOVE_BPS":           "5009",
	"TORN_HOUSEHOLDS_LEAVE_BELOW_BPS":            "3009",
	"TORN_HOUSEHOLDS_LEAVE_AFTER_DAYS":           "14",
	"TORN_HOUSEHOLDS_NOTICE_DAYS":                "11",
	"TORN_HOUSEHOLDS_SATISFACTION_ALPHA_BPS":     "2009",
	"TORN_HOUSEHOLDS_START_SATISFACTION_BPS":     "7009",
	"TORN_HOUSEHOLDS_BIRTH_ABOVE_BPS":            "8009",
	"TORN_HOUSEHOLDS_BIRTH_CHANCE_BPS":           "209",
	"TORN_HOUSEHOLDS_DEPENDANT_WEIGHT_BPS":       "5009",
	"TORN_HOUSEHOLDS_DEPENDANT_TO_EARNER_DAYS":   "69",
	"TORN_HOUSEHOLDS_EARNER_BPS":                 "6009",
	"TORN_HOUSEHOLDS_PLAYER_RESERVE_BPS":         "7009",
	"TORN_HOUSEHOLDS_WALLET_CAP":                 "2009",
	"TORN_HOUSEHOLDS_WELCOME_GRANT":              "309",
	"TORN_HOUSEHOLDS_WELCOME_FLOOR":              "1009",
	"TORN_HOUSEHOLDS_FOOD_POINTS_PER_PERSON_DAY": "13",
	"TORN_HOUSEHOLDS_WATER_MILLI_PER_PERSON":     "1009",
	"TORN_HOUSEHOLDS_FIREWOOD_MILLI_PER_PERSON":  "509",
	"TORN_HOUSEHOLDS_STOCK_DAYS":                 "11",
	"TORN_HOUSEHOLDS_TICK_KEEP_DAYS":             "39",
	"TORN_HOUSEHOLDS_CLINIC_CHANCE_BPS":          "509",
	"TORN_HOUSEHOLDS_WEIGHT_FOOD":                "3009",
	"TORN_HOUSEHOLDS_WEIGHT_WATER":               "2509",
	"TORN_HOUSEHOLDS_WEIGHT_WARMTH":              "1509",
	"TORN_HOUSEHOLDS_WEIGHT_SHELTER":             "1509",
	"TORN_HOUSEHOLDS_WEIGHT_WORK":                "1009",
	"TORN_HOUSEHOLDS_WEIGHT_CARE":                "509",
	"TORN_HOUSEHOLDS_SURPLUS_SPEND_BPS":          "5009",
	"TORN_HOUSEHOLDS_DISCRETIONARY_ITEMS":        "chain",
	"TORN_HOUSEHOLDS_WELCOME_QUIET_DAYS":         "13",
	"TORN_HOUSEHOLDS_MAX_RENT":                   "209",
	"TORN_HOUSEHOLDS_GRACE_DAYS":                 "23",
	"TORN_HOUSEHOLDS_COMPOSITIONS":               "1+0:3",

	"TORN_RAID_RULE_AT":                 "2097-01-01T00:00:00Z",
	"TORN_RAID_GRACE_DAYS":              "2012",
	"TORN_RAID_DEATH_RULE_AT":           "2097-01-01T00:00:00Z",
	"TORN_RAID_DEATH_GRACE_DAYS":        "2026",
	"TORN_RAID_MIN_LEAD_HOURS":          "2033",
	"TORN_RAID_MAX_LEAD_HOURS":          "2040",
	"TORN_RAID_WINDOW_MINUTES":          "2047",
	"TORN_RAID_COOLDOWN_HOURS":          "2054",
	"TORN_RAID_MAX_PER_WEEK":            "2061",
	"TORN_RAID_AFTER_LOSS_SHIELD_HOURS": "2068",
	"TORN_RAID_SHIELD_LOOT_CUT_BPS":     "2075",
	"TORN_RAID_FOUNDING_GRACE_DAYS":     "2082",
	"TORN_RAID_GRACE_RATIO":             "2089",
	"TORN_RAID_TARGET_RATIO_MAX":        "2096",
	"TORN_RAID_MIN_TARGET_VALUE":        "2103",
	"TORN_RAID_FEE":                     "2110",
	"TORN_RAID_LOOT_TREASURY_BPS":       "2117",
	"TORN_RAID_LOOT_STACK_BPS":          "2124",
	"TORN_RAID_DAMAGE_MAX_BPS":          "2131",
	"TORN_RAID_CARRY_PER_RAIDER":        "2138",
	"TORN_RAID_NPC_RAIDER_WAGE":         "2145",
	"TORN_RAID_MAX_NPC_RAIDERS":         "2152",
	"TORN_RAID_SEAT_FIREPOWER":          "2159",
	"TORN_RAID_SEAT_ARMOUR":             "2166",
	"TORN_RAID_ROUNDS":                  "2173",
	"TORN_RAID_DEFENDER_ADVANTAGE_BPS":  "2180",
	"TORN_RAID_BASE_HIT_BPS":            "2187",
	"TORN_RAID_HIT_FLOOR_BPS":           "2194",
	"TORN_RAID_HIT_CEILING_BPS":         "2201",
	"TORN_RAID_DEATH_CASH_BPS":          "2208",
	"TORN_RAID_DEATH_GOODS_BPS":         "2215",
	"TORN_RAID_GEAR_WEAR_BPS":           "2222",
	"TORN_RAID_LOSS_CAP_BPS":            "2229",
	"TORN_RAID_FALL_COOLDOWN_HOURS":     "2236",
	"TORN_RAID_STEALTH_BPS_PER_STEP":    "2243",
	"TORN_RAID_STEALTH_STEPS":           "2250",
	"TORN_RAID_STEALTH_FLOOR_BPS":       "2257",
	"TORN_RAID_NIGHT_PENALTY_BPS":       "2264",
	"TORN_RAID_BEACON_HOP_MINUTES":      "2271",
	"TORN_RAID_RELAY_M":                 "2278",
	"TORN_RAID_SPEED_FOOT_KMH":          "2285",
	"TORN_RAID_SPEED_CART_KMH":          "2292",
	"TORN_RAID_SPEED_HORSE_KMH":         "2299",
	"TORN_RAID_SPEED_RAIL_KMH":          "2306",
	"TORN_RAID_TRIBUTE_MIN":             "2313",

	"TORN_MINING_RULE_AT":                   "2094-01-01T00:00:00Z",
	"TORN_MINING_GRACE_DAYS":                "4007",
	"TORN_MINING_RESERVE_DIVISOR":           "4014",
	"TORN_MINING_SURFACE_SHARE_BPS":         "4021",
	"TORN_MINING_SHAFT1_SHARE_BPS":          "4028",
	"TORN_MINING_SHAFT2_SHARE_BPS":          "4035",
	"TORN_MINING_DEEP_SHARE_BPS":            "4042",
	"TORN_MINING_THIN_BELOW_BPS":            "4049",
	"TORN_MINING_THIN_FLOOR_BPS":            "4056",
	"TORN_MINING_LEAN_BELOW_PERMILLE":       "4063",
	"TORN_MINING_RICH_FROM_PERMILLE":        "4070",
	"TORN_MINING_LEAN_OUT_BPS":              "4077",
	"TORN_MINING_MEDIUM_OUT_BPS":            "4084",
	"TORN_MINING_RICH_OUT_BPS":              "4091",
	"TORN_MINING_SHAFT1_DEPTH_M":            "4098",
	"TORN_MINING_SHAFT2_DEPTH_M":            "4105",
	"TORN_MINING_HOIST_WINDLASS_KG_MIN":     "4112",
	"TORN_MINING_HOIST_GIN_KG_MIN":          "4119",
	"TORN_MINING_HOIST_DEPTH_REF_M":         "4133",
	"TORN_MINING_HAULER_KG_MIN":             "4140",
	"TORN_MINING_UNMADE_WAY_BPS":            "4147",
	"TORN_MINING_ADIT_GRADIENT":             "4154",
	"TORN_MINING_MIN_CONDITION_BPS":         "4161",
	"TORN_MINING_WATER_LIMIT_BPS":           "4168",
	"TORN_MINING_WEAR_PER_SHIFT_BPS":        "4175",
	"TORN_MINING_WEAR_PER_DAY_BPS":          "4182",
	"TORN_MINING_INFLOW_PER_DAY_BPS":        "4189",
	"TORN_MINING_ADIT_OUTFLOW_BPS":          "4196",
	"TORN_MINING_PUMP_GIN_OUTFLOW_BPS":      "4203",
	"TORN_MINING_SET_PROPS_GAIN_BPS":        "4217",
	"TORN_MINING_SET_PROPS_ITEMS":           "4224",
	"TORN_MINING_WORKS_SHIFT_MINUTES":       "4999",
	"TORN_MINING_WORKS_SHIFT_WAGE":          "4231",
	"TORN_MINING_SURVEY_FEE":                "4238",
	"TORN_MINING_MIN_SURFACE_DAYS":          "4245",
	"TORN_MINING_ORE_UNIT_G":                "4252",
	"TORN_MINING_CART_BROKEN_BPS":           "4259",
	"TORN_MINING_CART_SPEED_KMH":            "4266",
	"TORN_MINING_ROAD_LINK_LOT_MINUTES":     "4273",
	"TORN_MINING_ROAD_LINK_LOT_STONE":       "4280",
	"TORN_MINING_ROAD_LINK_LOT_HOURS":       "4287",
	"TORN_MINING_RAIL_WORK_HOUR_WAGE":       "4294",
	"TORN_MINING_RAIL_WORN_BPS":             "4301",
	"TORN_MINING_RAIL_RETRY_MINUTES":        "4998",
	"TORN_MINING_STUCK_HOURS":               "4308",
	"TORN_MINING_TEAM_FEED_WHEAT":           "4315",
	"TORN_MINING_DEFAULT_RATE_PER_TONNE_KM": "4322",
	"TORN_MINING_CHAIN_DEFAULT_PER_HOUR":    "4329",
	"TORN_POLITY_RULE_AT":                   "2096-01-01T00:00:00Z",
	"TORN_POLITY_RATIFY_DAYS":               "6002",
	"TORN_POLITY_EXIT_NOTICE_DAYS":          "6003",
	"TORN_POLITY_COUNTRY_MIN_RESIDENTS":     "6004",
	"TORN_POLITY_LEAGUE_FEE":                "6005",
	"TORN_POLITY_COUNTRY_FEE":               "6006",
	"TORN_POLITY_MAX_DUES_BPS":              "6007",
	"TORN_POLITY_WEIGHT_CAP_BPS":            "6008",
	"TORN_POLITY_MEMBERS_MAJORITY_BPS":      "6009",
	"TORN_POLITY_POPULATION_MAJORITY_BPS":   "6010",
	"TORN_POLITY_VOTE_WINDOW_HOURS":         "6011",
	"TORN_POLITY_EXPEL_AFTER_PERIODS":       "6012",
	"TORN_POLITY_DUES_PERIOD_HOURS":         "6013",
	"TORN_POLITY_TREATY_COOLING_DAYS":       "6014",
	"TORN_POLITY_TENDER_FEE":                "6015",
	"TORN_POLITY_BOND_BPS":                  "6016",
	"TORN_POLITY_RETENTION_BPS":             "6017",
	"TORN_POLITY_DEFECTS_DAYS":              "6018",
	"TORN_POLITY_DEBAR_DAYS":                "6019",
	"TORN_POLITY_COI_REFUSE_FROM_BPS":       "6020",
	"TORN_POLITY_TENDER_MIN_HOURS":          "6021",
	"TORN_POLITY_TENDER_GRACE_HOURS":        "6022",
	"TORN_POLITY_DEBT_DEFAULT_AFTER":        "6023",
	"TORN_POLITY_DEBT_LATE_FEE_BPS":         "6024",
	"TORN_POLITY_DEBT_SERVICE_CAP_BPS":      "6025",
	"TORN_POLITY_DEBT_MAX_INSTALMENTS":      "6026",
	"TORN_POLITY_DEBT_PERIOD_HOURS":         "6027",
	"TORN_POLITY_DEBT_MAX_RATE_BPS":         "6028",
	"TORN_INTEL_SKILL_XP_PER_OP":            "9013",
	"TORN_LICENCE_SHARE_CAP_FROM":           "9011",
	"TORN_LAW_GRACE_DAYS":                   "16001",
	"TORN_LAW_POINTS_PER_STAFF":             "16002",
	"TORN_LAW_POST_BASE_POINTS":             "16003",
	"TORN_LAW_STATION_BASE_POINTS":          "16004",
	"TORN_LAW_PATROL_COST":                  "16005",
	"TORN_LAW_INVESTIGATE_COST":             "16006",
	"TORN_LAW_ARREST_COST":                  "16007",
	"TORN_LAW_BASE_DETECT_BPS":              "16008",
	"TORN_LAW_PATROL_DETECT_BPS":            "16009",
	"TORN_LAW_DETECT_CAP_BPS":               "16010",
	"TORN_LAW_CUSTODY_HOURS":                "16011",
	"TORN_LAW_COMPLAINT_FEE":                "16012",
	"TORN_LAW_WARRANT_THRESHOLD":            "16013",
	"TORN_LAW_WARRANT_DAYS":                 "16014",
	"TORN_LAW_IMPROPER_PENALTY":             "16015",
	"TORN_LAW_EXCLUSION_BPS":                "16016",
	"TORN_LAW_STRENGTH_WITNESS":             "16017",
	"TORN_LAW_STRENGTH_RECORD":              "16018",
	"TORN_LAW_STRENGTH_LEDGER_TRACE":        "16019",
	"TORN_LAW_STRENGTH_SEIZED_ITEM":         "16020",
	"TORN_LAW_STRENGTH_INFORMANT":           "16021",
	"TORN_LAW_STRENGTH_CONFESSION":          "16022",
	"TORN_LAW_STRENGTH_INSPECTION":          "16023",
	"TORN_LAW_STRENGTH_INTEL_TRACE":         "16024",
	"TORN_LAW_WEIGHT_DECAY_BPS":             "16025",
	"TORN_LAW_HEARING_DELAY_HOURS":          "16026",
	"TORN_LAW_NPC_THRESHOLD":                "16027",
	"TORN_LAW_DEFENCE_STRENGTH":             "16028",
	"TORN_LAW_DEFENCE_FEE":                  "16029",
	"TORN_LAW_APPEAL_DAYS":                  "16030",
	"TORN_LAW_APPEAL_FEE":                   "16031",
	"TORN_LAW_BAIL_PER_HOUR":                "16032",
	"TORN_LAW_JAIL_FOOD_PER_DAY":            "16033",
	"TORN_LAW_CELLS_PER_WARDER":             "16034",
	"TORN_LAW_JAIL_CELLS_BASE":              "16035",
	"TORN_LAW_INFORMANT_PAY":                "16036",
	"TORN_LAW_RELIABILITY_START":            "16037",
	"TORN_LAW_RELIABILITY_FLOOR":            "16038",
	"TORN_LAW_RELIABILITY_GAIN":             "16039",
	"TORN_LAW_RELIABILITY_LOSS":             "16040",
	"TORN_LAW_ROTATE_DAYS":                  "16041",
	"TORN_LAW_MAX_INFORMANTS":               "16042",
	"TORN_LAW_RETALIATION_FINE":             "16043",
	"TORN_LAW_RETALIATION_BAN_DAYS":         "16044",
	"TORN_LAW_DEAL_CUT_BPS":                 "16045",
	"TORN_LAW_BRIBE_MIN":                    "16046",
	"TORN_LAW_BRIBE_MAX_EFFECT_BPS":         "16047",
	"TORN_LAW_BRIBE_TERM_DAYS":              "16048",
	"TORN_LAW_AUDIT_COST":                   "16049",
	"TORN_LAW_AUDIT_BASE_BPS":               "16050",
	"TORN_LAW_AUDIT_AGE_BPS_PER_DAY":        "16051",
	"TORN_LAW_AUDIT_CAP_BPS":                "16052",
	"TORN_LAW_CAPTURED_FROM":                "16053",
	"TORN_LAW_STANDING_GAIN":                "16054",
	"TORN_LAW_STANDING_INTRO":               "16055",
	"TORN_LAW_BLACK_PREMIUM_BPS":            "16056",
	"TORN_LAW_ESCROW_FEE_BPS":               "16057",
	"TORN_LAW_ESCROW_TIMEOUT_HOURS":         "16058",
	"TORN_LAW_BROKER_NEW_CAP":               "16059",
	"TORN_LAW_BROKER_CAP_PER_REP":           "16060",
	"TORN_LAW_SCAM_REP_LOSS":                "16061",
	"TORN_LAW_TRACE_BPS":                    "16062",
	"TORN_LAW_BUST_BASE_BPS":                "16063",
	"TORN_LAW_BUST_PATROL_BPS":              "16064",
	"TORN_LAW_LISTING_FEE":                  "16065",
	"TORN_LAW_LAUNDER_FEE_BPS":              "16066",
	"TORN_LAW_LAUNDER_CAP_BPS":              "16067",
	"TORN_LAW_LAUNDER_WINDOW_DAYS":          "16068",
	"TORN_LAW_ANOMALY_BPS":                  "16069",
	"TORN_LAW_LAUNDER_STAGE_HOURS":          "16070",
	"TORN_LAW_LAUNDER_EXPOSURE_BPS":         "16071",
	"TORN_LAW_INSPECT_BASE_BPS":             "16072",
	"TORN_LAW_INSPECT_PER_INSPECTOR_BPS":    "16073",
	"TORN_LAW_INSPECT_SIZE_BPS":             "16074",
	"TORN_LAW_INSPECT_CAP_BPS":              "16075",
	"TORN_LAW_CUSTOMS_PENALTY_BPS":          "16076",
	"TORN_LAW_DUTY_BPS":                     "16077",
	"TORN_LAW_POLICE_SHARE_BPS":             "16078",
	"TORN_LAW_RACKET_CONVICTIONS":           "16079",
	"TORN_LAW_GANG_DISCOVER_BPS":            "16080",
	"TORN_LAW_WITNESS_BPS":                  "16081",
	"TORN_LAW_FACT_WINDOW_DAYS":             "16082",
	"TORN_LAW_CHARGE_THRESHOLD":             "16083",
	"TORN_LAW_JUDGE_WAIT_HOURS":             "16084",
	"TORN_LAW_NPC_SENTENCE_BPS":             "16085",
	"TORN_LAW_APPEAL_MARGIN":                "16086",
	"TORN_LAW_BURN_BPS":                     "16087",
	"TORN_LAW_ASK_BPS":                      "16088",
	"TORN_LAW_GANG_HEAT_PER_CONVICTION":     "16089",
	"TORN_PROJECTS_SHIFT_MINUTES":           "9001",
	"TORN_PROJECTS_SHIFT_HOURS":             "9002",
	"TORN_PROJECTS_WAGE_PER_HOUR":           "9003",
	"TORN_PROJECTS_NPC_WAGE_PER_HOUR":       "9004",
	"TORN_PROJECTS_NPC_HOURS_PER_DAY":       "9005",

	"TORN_PROJECTS_RULE_AT":               "2031-01-02T03:04:05Z",
	"TORN_PROJECTS_GRACE_DAYS":            "7001",
	"TORN_PROJECTS_START_FEE":             "7002",
	"TORN_PROJECTS_PLEDGE_MIN":            "7003",
	"TORN_PROJECTS_CANCEL_BEFORE_STAGE":   "7004",
	"TORN_PROJECTS_MAX_OPEN":              "7007",
	"TORN_INTEL_RULE_AT":                  "2031-01-02T03:04:05Z",
	"TORN_INTEL_SABOTAGE_RULE_AT":         "2031-01-02T03:04:05Z",
	"TORN_LAW_POLICE_RULE_AT":             "2031-01-02T03:04:05Z",
	"TORN_LAW_COURTS_RULE_AT":             "2031-01-02T03:04:05Z",
	"TORN_LAW_BLACK_RULE_AT":              "2031-01-02T03:04:05Z",
	"TORN_LAW_CUSTOMS_RULE_AT":            "2031-01-02T03:04:05Z",
	"TORN_LAW_LAUNDER_RULE_AT":            "2031-01-02T03:04:05Z",
	"TORN_INTEL_GRACE_DAYS":               "7008",
	"TORN_INTEL_REPORT_TTL_DAYS":          "7009",
	"TORN_INTEL_OP_FEE":                   "7010",
	"TORN_INTEL_AGENT_WAGE":               "7011",
	"TORN_INTEL_OPS_PER_DAY":              "7012",
	"TORN_INTEL_COVER_CAP_DAYS":           "7013",
	"TORN_INTEL_COVER_BPS_PER_DAY":        "7014",
	"TORN_INTEL_BASE_CHANCE_BPS":          "7015",
	"TORN_INTEL_SKILL_BPS_PER_LEVEL":      "7016",
	"TORN_INTEL_CHANCE_FLOOR_BPS":         "7017",
	"TORN_INTEL_CHANCE_CAP_BPS":           "7018",
	"TORN_INTEL_COUNTER_POST_BPS":         "7019",
	"TORN_INTEL_COUNTER_VET_BPS":          "7020",
	"TORN_INTEL_COUNTER_SMALL_BPS":        "7021",
	"TORN_INTEL_SMALL_RESIDENTS":          "7022",
	"TORN_INTEL_DETECT_BPS":               "7023",
	"TORN_INTEL_ATTRIBUTION_BPS":          "7024",
	"TORN_INTEL_EXPEL_DAYS":               "7025",
	"TORN_INTEL_SABOTAGE_DELAY_HOURS":     "7026",
	"TORN_INTEL_SABOTAGE_SPOIL_BPS":       "7027",
	"TORN_INTEL_SABOTAGE_BLIND_BPS":       "7028",
	"TORN_INTEL_SABOTAGE_WEEKLY_LIMIT":    "7029",
	"TORN_INTEL_SABOTAGE_PER_TARGET_DAYS": "7030",
	"TORN_INTEL_SABOTAGE_SKIP_DAYS":       "7031",
	"TORN_INTEL_SABOTAGE_COST":            "7032",
	"TORN_LICENCE_RULE_AT":                "2031-01-02T03:04:05Z",
	"TORN_LICENCE_GRACE_DAYS":             "7033",
	"TORN_LICENCE_AUCTION_MIN_HOURS":      "7034",
	"TORN_LICENCE_RESERVE_MIN":            "7035",
	"TORN_LICENCE_TRANSFER_FEE_BPS":       "7036",
	"TORN_LICENCE_TRANSFER_COOLDOWN_DAYS": "7037",
	"TORN_LICENCE_MAX_SHARE_BPS":          "7038",
	"TORN_LICENCE_NOTICE_DAYS":            "7039",
	"TORN_LICENCE_NONUSE_DAYS":            "7040",
	"TORN_LICENCE_COI_SHARE_BPS":          "7041",
	"TORN_LICENCE_LISTING_FEE":            "7042",
	"TORN_LICENCE_TERM_MAX_DAYS":          "7043",

	"TORN_GROWTH_CAPABILITIES":   "shadow",
	"TORN_GROWTH_CACHE_TTL":      "6s",
	"TORN_GROWTH_RUINED_BPS":     "9000",
	"TORN_GROWTH_FLUSH_INTERVAL": "31s",

	"TORN_SETTLEMENT_PROTECTION_WINDOW":                    "170h",
	"TORN_SETTLEMENT_MIN_SPAWN_DISTANCE_KM":                "32",
	"TORN_SETTLEMENT_THREAT_RADIUS_KM":                     "152",
	"TORN_SETTLEMENT_SEARCH_MAX_CELLS":                     "2002",
	"TORN_SETTLEMENT_SEARCH_MAX_ATTEMPTS":                  "202",
	"TORN_SETTLEMENT_SPAWN_CIRCLE_RADIUS_KM":               "302",
	"TORN_SETTLEMENT_SPAWN_CIRCLE_CAPACITY":                "14",
	"TORN_SETTLEMENT_SPAWN_CIRCLE_FILL_BAND_KM":            "122",
	"TORN_SETTLEMENT_SPAWN_CIRCLE_MAX_ADVANCE":             "202",
	"TORN_SETTLEMENT_BUILDING_AREA_PER_CELL":               "8",
	"TORN_SETTLEMENT_BUILDING_STOREY_TIMBER_PER_CELL":      "6",
	"TORN_SETTLEMENT_BUILDING_STOREY_STONE_PER_CELL":       "8",
	"TORN_SETTLEMENT_BUILDING_STOREY_STONE_FROM":           "5",
	"TORN_SETTLEMENT_BUILDING_STOREY_SHIFTS_PER_CELL":      "6",
	"TORN_SETTLEMENT_BUILDING_STOREY_KNOWLEDGE":            "masonry=3",
	"TORN_SETTLEMENT_BUILDING_SALVAGE_BPS":                 "3200",
	"TORN_SETTLEMENT_USE_CHANGE_FEE_BPS":                   "202",
	"TORN_SETTLEMENT_BUILDING_LOOK_REROLLS":                "10",
	"TORN_SETTLEMENT_BUILDING_TEMPLATES_MAX":               "22",
	"TORN_SETTLEMENT_EXCLUDED_BIOMES":                      "polar_ice, ice_sheet",
	"TORN_SETTLEMENT_MAX_ABS_LATITUDE_DEG":                 "64.5",
	"TORN_SETTLEMENT_BIOME_PENALTIES":                      "desert=6, tundra=8",
	"TORN_SETTLEMENT_VILLAGE_GRID_LOTS":                    "7",
	"TORN_SETTLEMENT_MIN_BUILDABLE_LOT_SHARE_BPS":          "6600",
	"TORN_SETTLEMENT_GRID_SHIFT_MAX_LOTS":                  "5",
	"TORN_SETTLEMENT_AUTO_ROAD_COST":                       "12",
	"TORN_SETTLEMENT_LOT_ACCESS_CROSSING_COST":             "62",
	"TORN_SETTLEMENT_LOT_ACCESS_MAX_CROSSING":              "4",
	"TORN_SETTLEMENT_STREET_PITCH":                         "7",
	"TORN_SETTLEMENT_STREET_PLAN_MIN_GRID":                 "14",
	"TORN_SETTLEMENT_ROAD_FRONTAGE_DEPTH_LOTS":             "5",
	"TORN_SETTLEMENT_ROAD_PLAN_MAX_LOTS":                   "1502",
	"TORN_SETTLEMENT_ROAD_OPEN_LOTS_MAX":                   "30002",
	"TORN_SETTLEMENT_ROAD_FOREIGN_BUFFER_TILES":            "5",
	"TORN_SETTLEMENT_ROAD_STEEP_SLOPE_M":                   "32",
	"TORN_SETTLEMENT_ROAD_CORRIDOR_RING_TILES":             "3",
	"TORN_SETTLEMENT_ROAD_TRACK_COST_BPS":                  "15002",
	"TORN_SETTLEMENT_FOUNDING_DRAFT_TTL":                   "32m",
	"TORN_SETTLEMENT_FOUNDING_NAME_MIN":                    "5",
	"TORN_SETTLEMENT_FOUNDING_NAME_MAX":                    "26",
	"TORN_SETTLEMENT_FOUNDING_MOTTO_MAX":                   "62",
	"TORN_SETTLEMENT_FOUNDING_CURRENCY_NAME_MIN":           "5",
	"TORN_SETTLEMENT_FOUNDING_CURRENCY_NAME_MAX":           "26",
	"TORN_SETTLEMENT_FOUNDING_CURRENCY_CODE_LEN":           "5",
	"TORN_SETTLEMENT_FOUNDING_CURRENCY_SYMBOL_MAX":         "5",
	"TORN_SETTLEMENT_TEACH_PERIOD":                         "26h",
	"TORN_SETTLEMENT_RESIDENCE_COOLDOWN":                   "74h",
	"TORN_SETTLEMENT_TIMEZONE_COOLDOWN":                    "170h",
	"TORN_SETTLEMENT_HOME_CITY_CODE":                       "hearth2",
	"TORN_SETTLEMENT_PROPERTY_HUB_MIN_STAGE":               "city",
	"TORN_SETTLEMENT_TEACH_RATE_BPS":                       "1502",
	"TORN_SETTLEMENT_BASE_SCHOOL_CAPACITY_BPS":             "8002",
	"TORN_SETTLEMENT_SCARCITY_K_BPS":                       "10002",
	"TORN_SETTLEMENT_SCARCITY_FLOOR_BPS":                   "3002",
	"TORN_SETTLEMENT_SCARCITY_CAP_BPS":                     "80002",
	"TORN_SETTLEMENT_SELLER_BAND_BPS":                      "502",
	"TORN_SETTLEMENT_DEMOLITION_SALVAGE_BPS":               "2002",
	"TORN_SETTLEMENT_FOUNDING_GRANT":                       "10002",
	"TORN_SETTLEMENT_MATERIAL_MARKUP_BPS":                  "12002",
	"TORN_SETTLEMENT_STOCK_BASE_CAPACITY":                  "62",
	"TORN_GAME_CLOCK_LEGACY_SCALE":                         "63",
	"TORN_GAME_CLOCK_CUTOVER":                              "2026-10-07T00:00:00Z",
	"TORN_GAME_TRAVEL_TIME_SCALE":                          "64",
	"TORN_SETTLEMENT_BUILD_HOMES_PER_CREW":                 "17",
	"TORN_SETTLEMENT_CHARTER_MAX_OFFICES":                  "25",
	"TORN_SETTLEMENT_CHARTER_MAX_SEATS":                    "16",
	"TORN_SETTLEMENT_CHARTER_MAX_PERMISSIONS":              "41",
	"TORN_SETTLEMENT_CHARTER_TITLE_MIN":                    "3",
	"TORN_SETTLEMENT_CHARTER_TITLE_MAX":                    "33",
	"TORN_SETTLEMENT_CHARTER_ELECTION_TERM_DAYS":           "15",
	"TORN_SETTLEMENT_CHARTER_CANDIDACY_HOURS":              "49",
	"TORN_SETTLEMENT_CHARTER_VOTING_HOURS":                 "73",
	"TORN_SETTLEMENT_CHARTER_RECALL_MIN_TENURE_DAYS":       "6",
	"TORN_SETTLEMENT_CHARTER_RECALL_SIGNATURE_BPS":         "2001",
	"TORN_SETTLEMENT_CHARTER_RECALL_MIN_SIGNATURES":        "4",
	"TORN_SETTLEMENT_CHARTER_RECALL_VOTE_HOURS":            "73",
	"TORN_SETTLEMENT_CHARTER_RECALL_COOLDOWN_DAYS":         "15",
	"TORN_SETTLEMENT_CHARTER_AMEND_VOTE_HOURS":             "73",
	"TORN_SETTLEMENT_CHARTER_AMEND_QUORUM_BPS":             "3001",
	"TORN_SETTLEMENT_CHARTER_AMEND_VOTE_MIN_RESIDENTS":     "7",
	"TORN_SETTLEMENT_CHARTER_ACTING_DAYS":                  "8",
	"TORN_SETTLEMENT_CHARTER_ACTING_SPEND_CAP":             "2001",
	"TORN_SETTLEMENT_CHARTER_MIN_RESIDENCY_DAYS":           "4",
	"TORN_SETTLEMENT_STORAGE_SPOIL_KEPT_BPS":               "7",
	"TORN_SETTLEMENT_STORAGE_SPOIL_UNKEPT_BPS":             "32",
	"TORN_SETTLEMENT_STORAGE_KEEPER_RULE_AT":               "2026-12-01T00:00:00Z",
	"TORN_SETTLEMENT_STORAGE_KEEPER_GRACE_DAYS":            "22",
	"TORN_SETTLEMENT_REAL_ITEMS_RULE_AT":                   "2026-12-01T00:00:00Z",
	"TORN_SETTLEMENT_REAL_ITEMS_GRACE_DAYS":                "10",
	"TORN_SETTLEMENT_TOOL_BARE_HANDS_BPS":                  "5600",
	"TORN_SETTLEMENT_SERVICE_RULE_AT":                      "2026-12-02T00:00:00Z",
	"TORN_SETTLEMENT_SERVICE_GRACE_DAYS":                   "11",
	"TORN_SETTLEMENT_PERSONAL_RULE_AT":                     "2026-12-03T00:00:00Z",
	"TORN_SETTLEMENT_PERSONAL_GRACE_DAYS":                  "12",
	"TORN_SETTLEMENT_LAND_RULE_AT":                         "2026-12-04T00:00:00Z",
	"TORN_SETTLEMENT_LAND_GRACE_DAYS":                      "208",
	"TORN_SETTLEMENT_LAND_WOODLAND_RING":                   "204",
	"TORN_SETTLEMENT_LAND_REGROW_HOURS":                    "321",
	"TORN_SETTLEMENT_LAND_SAPLING_HOURS":                   "225",
	"TORN_SETTLEMENT_LAND_FELL_RADIUS":                     "205",
	"TORN_SETTLEMENT_LAND_QUARRY_RADIUS":                   "204",
	"TORN_SETTLEMENT_LAND_ROCK_SHIFTS":                     "203",
	"TORN_SETTLEMENT_ZONE_MAX_CELLS":                       "501",
	"TORN_SETTLEMENT_ZONING_RULE_AT":                       "2026-12-20T00:00:00Z",
	"TORN_SETTLEMENT_ZONING_GRACE_DAYS":                    "401",
	"TORN_SETTLEMENT_SLOPE_RULE_AT":                        "2026-12-21T00:00:00Z",
	"TORN_SETTLEMENT_SLOPE_GRACE_DAYS":                     "402",
	"TORN_SETTLEMENT_SLOPE_FLAT_M":                         "109",
	"TORN_SETTLEMENT_SLOPE_GENTLE_M":                       "121",
	"TORN_SETTLEMENT_SLOPE_UNUSABLE_M":                     "131",
	"TORN_SETTLEMENT_PARCEL_MAX_CELLS":                     "161",
	"TORN_SETTLEMENT_PARCEL_CHANGE_FEE":                    "151",
	"TORN_SETTLEMENT_ZONE_OPEN_FEE":                        "121",
	"TORN_SETTLEMENT_ZONE_NOTICE_MINUTES":                  "125",
	"TORN_SETTLEMENT_FOUNDATION_STONE":                     "105",
	"TORN_SETTLEMENT_FOUNDATION_WAGE":                      "161",
	"TORN_SETTLEMENT_CLAIM_COST":                           "2101",
	"TORN_SETTLEMENT_CLAIM_UPKEEP":                         "106",
	"TORN_SETTLEMENT_CLAIM_MAX_TILES":                      "301",
	"TORN_SETTLEMENT_FARM_RULE_AT":                         "2026-12-05T00:00:00Z",
	"TORN_SETTLEMENT_FARM_GRACE_DAYS":                      "209",
	"TORN_SETTLEMENT_CRAFTING_RULE_AT":                     "2026-12-06T00:00:00Z",
	"TORN_SETTLEMENT_CRAFTING_GRACE_DAYS":                  "210",
	"TORN_SETTLEMENT_CARE_RULE_AT":                         "2026-12-07T00:00:00Z",
	"TORN_SETTLEMENT_CARE_GRACE_DAYS":                      "211",
	"TORN_SETTLEMENT_SURVIVAL_RULE_AT":                     "2026-12-08T00:00:00Z",
	"TORN_SETTLEMENT_SURVIVAL_GRACE_DAYS":                  "212",
	"TORN_SETTLEMENT_TEACHING_RULE_AT":                     "2026-12-09T00:00:00Z",
	"TORN_SETTLEMENT_TEACHING_GRACE_DAYS":                  "213",
	"TORN_SETTLEMENT_TEACHER_BASE_BPS":                     "6200",
	"TORN_SETTLEMENT_TEACHER_PER_LEVEL_BPS":                "152",
	"TORN_SETTLEMENT_TEACHER_XP_PER_CLASS":                 "22",
	"TORN_SETTLEMENT_RESEARCH_FREE_SLOTS":                  "300",
	"TORN_SETTLEMENT_RESEARCH_SPEED_FLOOR_BPS":             "301",
	"TORN_SETTLEMENT_RESEARCH_SCHOLAR_FLOOR_BPS":           "302",
	"TORN_SETTLEMENT_RESEARCH_SKILL_BPS_PER_LEVEL":         "303",
	"TORN_SETTLEMENT_RESEARCH_SCHOLAR_CAP_BPS":             "304",
	"TORN_SETTLEMENT_RESEARCH_NPC_SCHOLAR_LEVEL":           "305",
	"TORN_SETTLEMENT_RESEARCH_LITERACY_BONUS_BPS":          "306",
	"TORN_SETTLEMENT_RESEARCH_CATCH_UP_BPS":                "307",
	"TORN_SETTLEMENT_RESEARCH_ERA_BASE_DEPTH":              "308",
	"TORN_SETTLEMENT_RESEARCH_ERA_SHARE_BPS":               "309",
	"TORN_SETTLEMENT_RESEARCH_AHEAD_PER_STEP_BPS":          "310",
	"TORN_SETTLEMENT_RESEARCH_AHEAD_CAP_BPS":               "311",
	"TORN_SETTLEMENT_RESEARCH_SHARE_PER_PARTNER_BPS":       "312",
	"TORN_SETTLEMENT_RESEARCH_SHARE_CAP_BPS":               "313",
	"TORN_SETTLEMENT_RESEARCH_BREAKTHROUGH_NEED_PER_DEPTH": "314",
	"TORN_SETTLEMENT_RESEARCH_BREAKTHROUGH_MAX_BPS":        "315",
	"TORN_SETTLEMENT_RESEARCH_EXPERIENCE_PER_SHIFT":        "316",
	"TORN_SETTLEMENT_RESEARCH_SCHOLAR_XP":                  "317",
	"TORN_SETTLEMENT_EXPORT_PRICE_BPS":                     "9002",
	"TORN_SETTLEMENT_EXPORT_CAP_BASE":                      "402",
	"TORN_SETTLEMENT_EXPORT_CAP_PER_RESIDENT":              "42",
	"TORN_SETTLEMENT_EXPORT_KEEP_PRESETS":                  "12,27,52",
	"TORN_SETTLEMENT_MATERIAL_BUY_MAX":                     "202",
	"TORN_SETTLEMENT_MATERIAL_BUY_PRESETS":                 "7,22,52",
	"TORN_SETTLEMENT_DONATION_MIN":                         "102",
	"TORN_SETTLEMENT_DONATION_MAX":                         "100002",
	"TORN_SETTLEMENT_DONATION_PRESETS":                     "252,1002,5002",
	"TORN_SETTLEMENT_CITIZEN_LOT_PRICE":                    "402",
	"TORN_SETTLEMENT_CITIZEN_LOT_PRICE_MIN":                "102",
	"TORN_SETTLEMENT_CITIZEN_LOT_PRICE_MAX":                "5002",
	"TORN_SETTLEMENT_CITIZEN_PERMIT_FEE":                   "102",
	"TORN_SETTLEMENT_CITIZEN_PERMIT_FEE_MAX":               "1002",
	"TORN_SETTLEMENT_CITIZEN_TAX_BPS":                      "202",
	"TORN_SETTLEMENT_CITIZEN_TAX_BPS_MAX":                  "502",
	"TORN_SETTLEMENT_CITIZEN_TAX_PERIOD":                   "26h",
	"TORN_SETTLEMENT_CITIZEN_MATERIAL_MARKUP_BPS":          "12002",
	"TORN_SETTLEMENT_CITIZEN_MAX_LOTS_PER_PLAYER":          "8",
	"TORN_SETTLEMENT_CITIZEN_PRIVATE_SHARE_MAX_BPS":        "6002",
	"TORN_SETTLEMENT_CITIZEN_HOME_REST_COOLDOWN":           "8h",
	"TORN_SETTLEMENT_CITIZEN_HOME_REST_HEALTH":             "12",
	"TORN_SETTLEMENT_CITIZEN_HOME_REST_HAPPINESS":          "7",
	"TORN_EDUCATION_TEACHER_WAGE_BPS":                      "7002",
	"TORN_EDUCATION_TEACHER_MIN_WAGE":                      "42",
	"TORN_EDUCATION_TEACHER_MAX_STUDENTS":                  "14",
	"TORN_TRAINING_ENERGY_COST":                            "12",
	"TORN_TRAINING_STAMINA_GAIN":                           "8",
	"TORN_TRAINING_STRENGTH_XP":                            "32",
	"TORN_TRAINING_DIMINISH_STAMINA":                       "402",
	"TORN_TRAINING_STAMINA_PER_MAX_ENERGY":                 "52",
	"TORN_TRAINING_MAX_ENERGY_BONUS_CAP":                   "32",
	"TORN_TRAINING_YARD_BPS":                               "4002",
	"TORN_TRAINING_GROUND_BPS":                             "6002",
	"TORN_TRAINING_GYM_BPS":                                "10002",
	"TORN_TRAINING_GROUND_FEE":                             "22",
	"TORN_TRAINING_GYM_FEE":                                "62",
	"TORN_LABOR_SHIFT_MINUTES":                             "62",
	"TORN_LABOR_SHIFT_REAL_MINUTES":                        "4",
	"TORN_LABOR_REFERENCE_CREW":                            "6",
	"TORN_LABOR_BASE_WAGE":                                 "32",
	"TORN_LABOR_MIN_WAGE":                                  "13",
	"TORN_LABOR_MIN_WAGE_VILLAGE":                          "12",
	"TORN_LABOR_MIN_WAGE_TOWN":                             "17",
	"TORN_LABOR_MIN_WAGE_CITY":                             "27",
	"TORN_LABOR_PARTICIPATION_BPS":                         "6002",
	"TORN_LABOR_BASE_HOUSING":                              "10",
	"TORN_LABOR_NPC_PRODUCTIVITY_BPS":                      "8502",
	"TORN_LABOR_FEE_BPS":                                   "502",
	"TORN_LABOR_BUDGET_SLACK_BPS":                          "5002",
	"TORN_CURRENCY_CHARTER_R0":                             "12",
	"TORN_CURRENCY_CHARTER_FEE":                            "1002",
	"TORN_CURRENCY_CHARTER_MIN_DEPOSIT":                    "5002",
	"TORN_CURRENCY_MINT_FEE_BPS":                           "52",
	"TORN_CURRENCY_AUTO_CHARTER_SHARE_BPS":                 "5004",
	"TORN_CURRENCY_AUTO_CHARTER_FLOOR":                     "502",
	"TORN_CURRENCY_DESK_SLIPPAGE_BPS":                      "102",
	"TORN_CURRENCY_DESK_PRESETS":                           "102,502,2002",
	"TORN_CURRENCY_FX_RESERVE_FEE_BPS":                     "32",
	"TORN_CURRENCY_FX_MAX_MOVE_BPS":                        "2002",
	"TORN_CURRENCY_FX_MIN_TRADES":                          "10",
	"TORN_CURRENCY_FX_WINDOW_PERIODS":                      "9",
	"TORN_CURRENCY_FX_MIN_ORDER_SUP":                       "12",
	"TORN_CURRENCY_FX_BOOK_LIMIT":                          "202",
	"TORN_CURRENCY_FX_MAX_OPEN_ORDERS":                     "22",
	"TORN_CURRENCY_FX_CONVERT_SLIPPAGE_BPS":                "102",
	"TORN_CURRENCY_FX_ORDER_TTL":                           "170h",
	"TORN_CURRENCY_FX_PERIOD":                              "26h",
	"TORN_CURRENCY_FX_UNIT_PRESETS":                        "1002,5002,20002",
	"TORN_CURRENCY_RESERVE_GOLD_HAIRCUT_BPS":               "1002",
	"TORN_CURRENCY_RESERVE_WITHDRAW_NOTICE_HOURS":          "74",
	"TORN_CURRENCY_RESERVE_POLICY_RATE_BPS":                "1202",
	"TORN_CURRENCY_INTERVENTION_CAP_BPS":                   "802",
	"TORN_CURRENCY_INTERVENTION_POT_FLOOR_BPS":             "3002",
	"TORN_CURRENCY_WIND_DOWN_DAYS":                         "62",
	"TORN_CURRENCY_MACRO_M_NORM_BPS":                       "2002",
	"TORN_CURRENCY_MACRO_KAPPA_BPS":                        "202",
	"TORN_CURRENCY_MACRO_PI_MAX_BPS":                       "302",
	"TORN_CURRENCY_MACRO_W_TRADABLE_BPS":                   "6002",
	"TORN_CURRENCY_INTERVENTION_DELAY":                     "26h",
	"TORN_LABOR_NPC_HOURS_PER_SLOT_DAY":                    "12",
	"TORN_LABOR_HUNGRY_OUTPUT_BPS":                         "5002",
	"TORN_LABOR_HUNGRY_SHIFT_HUNGER":                       "7",
	"TORN_LABOR_REPAIR_MATERIAL_SHARE_BPS":                 "2002",
	"TORN_LABOR_REPAIR_SHIFTS_FULL":                        "12",
	"TORN_LABOR_WORN_OUTPUT_BPS":                           "7502",
	"TORN_LABOR_CLOSED_BPS":                                "2502",
	"TORN_LABOR_WORN_BPS":                                  "5004",
	"TORN_LABOR_REPAIR_BELOW_BPS":                          "7002",
	"TORN_LABOR_DECAY_BPS_PER_DAY":                         "52",
	"TORN_LABOR_JOURNEYMAN_SHIFTS":                         "8",
	"TORN_LABOR_MASTER_SHIFTS":                             "32",
	"TORN_LABOR_APPRENTICE_BPS":                            "7002",
	"TORN_LABOR_JOURNEYMAN_BPS":                            "10002",
	"TORN_LABOR_MASTER_BPS":                                "13002",
	"TORN_LABOR_TIGHT_BALANCED_BPS":                        "5002",
	"TORN_LABOR_TIGHT_TIGHT_BPS":                           "10002",
	"TORN_LABOR_TIGHT_SHORT_BPS":                           "20002",
	"TORN_LABOR_WAGE_SLACK_BPS":                            "7002",
	"TORN_LABOR_WAGE_BALANCED_BPS":                         "10002",
	"TORN_LABOR_WAGE_TIGHT_BPS":                            "15002",
	"TORN_LABOR_WAGE_SHORT_BPS":                            "25002",
	"TORN_LABOR_HIRE_PRESETS":                              "3,4,6",
	"TORN_LABOR_WAGE_PRESETS":                              "102,127,152,202",
	"TORN_GAME_CLOCK_EPOCH":                                "2026-03-01T00:00:00Z",
	"TORN_BAG_CARRY_BASE":                                  "10",
	"TORN_BAG_BASE_COMFORT_KG":                             "9",
	"TORN_BAG_BASE_HARD_KG":                                "22",
	"TORN_BAG_FULL_SHARE_BPS":                              "5002",
	"TORN_BAG_TORN_SPACE_BPS":                              "5002",
	"TORN_BAG_REPAIR_SHARE_BPS":                            "2502",
	"TORN_BAG_WEAR_PER_DAY":                                "3",
	"TORN_MERCHANT_RESTOCK_HOUR":                           "8",
	"TORN_MERCHANT_MARKUP_MIN_BPS":                         "10002",
	"TORN_MERCHANT_MARKUP_MAX_BPS":                         "15002",
	"TORN_MERCHANT_STOCK_DAYS":                             "4",
	"TORN_MERCHANT_FOOD_SHARE_BPS":                         "4002",
	"TORN_MERCHANT_OTHER_SHARE_BPS":                        "3002",
	"TORN_MERCHANT_PLAYER_DAY_FOOD":                        "5",
	"TORN_MERCHANT_PLAYER_DAY_OTHER":                       "4",
	"TORN_MERCHANT_SUPPLY_VALUE_PER_RESIDENT_DAY":          "62",
	"TORN_MERCHANT_BUILDING_BOOST_BPS":                     "15002",
	"TORN_MERCHANT_CAP_PRESETS":                            "10002,11002",
	"TORN_MERCHANT_BUY_PRESETS":                            "3,5",
	"TORN_MERCHANT_TAX_DEFAULT_BPS":                        "302",
	"TORN_MERCHANT_TAX_MAX_BPS":                            "1502",
	"TORN_MERCHANT_TAX_PRESETS":                            "2,302",
	"TORN_MERCHANT_OUTPUT_DAYS":                            "9",
	"TORN_PREMIUM_NIL_UNIT_SUP":                            "102",
	"TORN_PREMIUM_NIL_EXAMPLES":                            "12,112",
}

// clearEnv removes any TORN_ override the surrounding shell happens to carry,
// so a developer's environment cannot make a test pass or fail by accident.
func clearEnv(t *testing.T) {
	t.Helper()

	for _, s := range settings {
		if _, ok := os.LookupEnv(s.envName()); ok {
			t.Setenv(s.envName(), "")
			os.Unsetenv(s.envName())
		}
	}
}

// changedFields lists the leaf fields of Config where a and b differ.
func changedFields(a, b *Config) map[string]bool {
	changed := map[string]bool{}

	av, bv := reflect.ValueOf(*a), reflect.ValueOf(*b)
	for i := 0; i < av.NumField(); i++ {
		section := av.Type().Field(i).Name
		as, bs := av.Field(i), bv.Field(i)

		for j := 0; j < as.NumField(); j++ {
			name := section + "." + as.Type().Field(j).Name
			if !reflect.DeepEqual(as.Field(j).Interface(), bs.Field(j).Interface()) {
				changed[name] = true
			}
		}
	}
	return changed
}

// allFields lists every leaf field of Config.
func allFields() []string {
	var names []string

	v := reflect.ValueOf(Config{})
	for i := 0; i < v.NumField(); i++ {
		section := v.Type().Field(i).Name
		for j := 0; j < v.Field(i).NumField(); j++ {
			names = append(names, section+"."+v.Field(i).Type().Field(j).Name)
		}
	}
	return names
}

// TestEveryFieldIsReachableFromTheFile fails if a field exists on Config but
// no yaml key fills it, which would make it hardcoded again in all but name.
func TestEveryFieldIsReachableFromTheFile(t *testing.T) {
	clearEnv(t)

	cfg := mustLoad(t, fullFile)
	changed := changedFields(cfg, Defaults())

	for _, name := range allFields() {
		if !changed[name] {
			t.Errorf("%s did not change: no yaml key reaches it, so it is still effectively hardcoded", name)
		}
	}
}

// TestEveryFieldIsReachableFromTheEnvironment is the same guarantee for the
// override path, which is the one that matters during an incident.
func TestEveryFieldIsReachableFromTheEnvironment(t *testing.T) {
	clearEnv(t)

	// The table and the fixture must cover each other, so adding a setting
	// without an override test fails here rather than going unnoticed.
	if len(envOverrides) != len(settings) {
		t.Fatalf("the fixture holds %d overrides but there are %d settings", len(envOverrides), len(settings))
	}
	for _, s := range settings {
		if _, ok := envOverrides[s.envName()]; !ok {
			t.Fatalf("%s has no override in the fixture; its variable is %s", s.name(), s.envName())
		}
	}

	for name, value := range envOverrides {
		t.Setenv(name, value)
	}

	cfg, err := Load(repoConfig)
	if err != nil {
		t.Fatalf("Load() with every override set: %v", err)
	}

	changed := changedFields(cfg, Defaults())
	for _, name := range allFields() {
		if !changed[name] {
			t.Errorf("%s did not change: no TORN_ variable reaches it", name)
		}
	}
}

// TestEnvBeatsFile pins the precedence rule. The file is the shared truth; the
// environment is how one deployment differs from it without a fork.
func TestEnvBeatsFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("TORN_WORKER_BATCH_SIZE", "7")

	cfg := mustLoad(t, "worker:\n  batch_size: 100\n")

	if cfg.Worker.BatchSize != 7 {
		t.Errorf("BatchSize = %d, want 7: the environment must win over the file", cfg.Worker.BatchSize)
	}
}

func TestEnvOverEmptyFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("TORN_NATS_BACKOFF", "2s,4s,8s")

	cfg := mustLoad(t, "")

	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	if !reflect.DeepEqual(cfg.NATS.Backoff, want) {
		t.Errorf("Backoff = %v, want %v", cfg.NATS.Backoff, want)
	}
}

func TestEnvRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
	}{
		{"duration without a unit", "TORN_GATEWAY_POLL_TIMEOUT", "30"},
		{"not a number", "TORN_WORKER_BATCH_SIZE", "lots"},
		{"one bad entry in a list", "TORN_NATS_BACKOFF", "1s,5s,soon"},
		{"set but empty", "TORN_DEDUP_TTL", ""},
		{"money with a fraction", "TORN_ECONOMY_STARTING_CASH", "5000.5"},
		{"money in float notation", "TORN_ECONOMY_STARTING_CASH", "5e3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tc.env, tc.value)

			_, err := Load(repoConfig)
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("error = %v, want one wrapping ErrInvalidValue", err)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Errorf("the error does not name the variable %q: %v", tc.env, err)
			}
		})
	}
}

// TestEnvNamingRule documents the rule by checking it, so the package doc and
// the code cannot drift apart.
func TestEnvNamingRule(t *testing.T) {
	tests := []struct {
		section string
		key     string
		want    string
	}{
		{"gateway", "poll_timeout", "TORN_GATEWAY_POLL_TIMEOUT"},
		{"worker", "batch_size", "TORN_WORKER_BATCH_SIZE"},
		{"ratelimit", "default_burst", "TORN_RATELIMIT_DEFAULT_BURST"},
		{"nats", "duplicate_window", "TORN_NATS_DUPLICATE_WINDOW"},
		{"player", "default_language", "TORN_PLAYER_DEFAULT_LANGUAGE"},
		{"economy", "starting_cash", "TORN_ECONOMY_STARTING_CASH"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := setting{section: tc.section, key: tc.key}.envName()
			if got != tc.want {
				t.Errorf("envName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValidate covers every rejection Validate is responsible for. Each case
// mutates one field of an otherwise valid configuration, so a failure names
// exactly one cause.
func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(*Config)
		want   error
	}{
		{
			name:   "a residence cool-down of zero",
			break_: func(c *Config) { c.Settlement.ResidenceCooldown = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "no home city to return to",
			break_: func(c *Config) { c.Settlement.HomeCityCode = " " },
			want:   ErrEmpty,
		},
		{
			name:   "a latitude cap of zero",
			break_: func(c *Config) { c.Settlement.MaxAbsLatitudeDeg = 0 },
			want:   ErrInvalidValue,
		},
		{
			name:   "a latitude cap past the pole",
			break_: func(c *Config) { c.Settlement.MaxAbsLatitudeDeg = 91 },
			want:   ErrInvalidValue,
		},
		{
			name:   "a city location that is not lat,lon",
			break_: func(c *Config) { c.Travel.CityLocations = []string{"support=95:20"} },
			want:   ErrInvalidValue,
		},
		{
			name:   "a world reach without a distance",
			break_: func(c *Config) { c.Travel.WorldReach = []string{"walk"} },
			want:   ErrInvalidValue,
		},
		{
			name:   "a biome penalty without a number",
			break_: func(c *Config) { c.Settlement.BiomePenalties = []string{"desert"} },
			want:   ErrInvalidValue,
		},
		{
			name:   "a negative biome penalty",
			break_: func(c *Config) { c.Settlement.BiomePenalties = []string{"desert=-1"} },
			want:   ErrInvalidValue,
		},
		{
			name:   "a biome both excluded and penalised",
			break_: func(c *Config) { c.Settlement.BiomePenalties = []string{"polar_ice=3"} },
			want:   ErrInvalidValue,
		},
		{
			name:   "a zero duration is not 'no limit'",
			break_: func(c *Config) { c.Gateway.PollTimeout = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "a negative duration",
			break_: func(c *Config) { c.Worker.PollInterval = -1 * time.Second },
			want:   ErrNotPositive,
		},
		{
			name:   "a zero limit does nothing at all",
			break_: func(c *Config) { c.Worker.BatchSize = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "a negative limit",
			break_: func(c *Config) { c.RateLimit.DefaultRate = -5 },
			want:   ErrNotPositive,
		},
		{
			name:   "no starting cash",
			break_: func(c *Config) { c.Economy.StartingCash = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "negative starting cash",
			break_: func(c *Config) { c.Economy.StartingCash = -1 },
			want:   ErrNotPositive,
		},
		{
			name:   "no bank minimum",
			break_: func(c *Config) { c.Economy.BankMinAmount = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "a bank minimum above the maximum",
			break_: func(c *Config) { c.Economy.BankMinAmount, c.Economy.BankMaxAmount = 10, 5 },
			want:   ErrBankLimitsInverted,
		},
		{
			name:   "an empty language",
			break_: func(c *Config) { c.Player.DefaultLanguage = "  " },
			want:   ErrEmpty,
		},
		{
			name:   "no backoff schedule",
			break_: func(c *Config) { c.NATS.Backoff = nil },
			want:   ErrEmptyList,
		},
		{
			name:   "a backoff entry at zero",
			break_: func(c *Config) { c.NATS.Backoff = []time.Duration{time.Second, 0} },
			want:   ErrNotPositive,
		},
		{
			name: "a flat backoff schedule",
			break_: func(c *Config) {
				c.NATS.Backoff = []time.Duration{5 * time.Second, 5 * time.Second}
			},
			want: ErrNotIncreasing,
		},
		{
			name: "a shrinking backoff schedule",
			break_: func(c *Config) {
				c.NATS.Backoff = []time.Duration{5 * time.Second, time.Second}
			},
			want: ErrNotIncreasing,
		},
		{
			name:   "renewing exactly at the ttl is not renewing",
			break_: func(c *Config) { c.Lease.RenewDivisor = 1 },
			want:   ErrRenewDivisorTooSmall,
		},
		{
			name: "the request timeout swallows the long poll",
			break_: func(c *Config) {
				c.Telegram.RequestTimeout = 200 * time.Second
			},
			want: ErrRequestTimeoutTooLong,
		},
		{
			name: "the request timeout exactly equals the polling timeout",
			break_: func(c *Config) {
				c.Telegram.RequestTimeout = c.Telegram.MaxPollTimeout + c.Telegram.PollTimeoutGrace
			},
			want: ErrRequestTimeoutTooLong,
		},
		{
			name: "a poll longer than the client accepts",
			break_: func(c *Config) {
				c.Gateway.PollTimeout = c.Telegram.MaxPollTimeout + time.Second
			},
			want: ErrPollTimeoutTooLong,
		},
		{
			name: "an idempotency key that expires mid-retry",
			break_: func(c *Config) {
				c.Game.IdempotencyTTL = time.Second
			},
			want: ErrIdempotencyTTLTooShort,
		},
		{
			name: "a claim lease that expires inside the batch budget",
			break_: func(c *Config) {
				c.Scheduler.ClaimTimeout = c.Scheduler.ShutdownTimeout
			},
			want: ErrClaimTimeoutTooShort,
		},
		{
			name:   "a claim lease of zero",
			break_: func(c *Config) { c.Scheduler.ClaimTimeout = 0 },
			want:   ErrNotPositive,
		},
		{
			name: "a notice delivery that outlasts the ack wait",
			break_: func(c *Config) {
				c.Notifier.SendBudget = c.NATS.AckWait
			},
			want: ErrSendBudgetTooLong,
		},
		{
			name: "a receipt margin that leaves the gateway no time to send",
			break_: func(c *Config) {
				c.Notifier.ReceiptMargin = c.Notifier.SendBudget
			},
			want: ErrReceiptMarginTooLong,
		},
		{
			name:   "a notice that is never too old",
			break_: func(c *Config) { c.Notifier.MaxAge = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "a game clock that never scales",
			break_: func(c *Config) { c.Game.TimeScale = 0 },
			want:   ErrNotPositive,
		},
		{
			name:   "a game clock faster than a day a second",
			break_: func(c *Config) { c.Game.TimeScale = 86_401 },
			want:   ErrInvalidTimeScale,
		},
		{
			name:   "a time zone nobody knows",
			break_: func(c *Config) { c.Player.DefaultTimezone = "Mars/Olympus" },
			want:   ErrUnknownTimezone,
		},
		{
			name:   "no time zone",
			break_: func(c *Config) { c.Player.DefaultTimezone = " " },
			want:   ErrEmpty,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.break_(cfg)

			err := cfg.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want one wrapping %v", err, tc.want)
			}
		})
	}
}

// TestValidateRunsOnLoad makes sure an invalid file is refused rather than
// returned. A caller that got a Config back would have no reason to re-check.
func TestValidateRunsOnLoad(t *testing.T) {
	clearEnv(t)

	_, err := Load(write(t, "lease:\n  renew_divisor: 1\n"))
	if !errors.Is(err, ErrRenewDivisorTooSmall) {
		t.Fatalf("Load() = %v, want one wrapping ErrRenewDivisorTooSmall", err)
	}
}

// TestPollHTTPTimeout pins the accessor the polling client must be given. Its
// whole purpose is that reaching for the right value is easier than reaching
// for RequestTimeout, which is the mistake that silently stops a bot.
func TestPollHTTPTimeout(t *testing.T) {
	cfg := Defaults()

	want := 135 * time.Second
	if got := cfg.Telegram.PollHTTPTimeout(); got != want {
		t.Errorf("PollHTTPTimeout() = %s, want %s", got, want)
	}
	if cfg.Telegram.RequestTimeout >= cfg.Telegram.PollHTTPTimeout() {
		t.Error("the polling timeout must exceed the ordinary request timeout")
	}
}

func TestRenewEvery(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		div  int
		want time.Duration
	}{
		{"the shipped default", 30 * time.Second, 3, 10 * time.Second},
		{"two attempts inside one ttl", 30 * time.Second, 2, 15 * time.Second},
		{"a long ttl", 2 * time.Minute, 4, 30 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := Lease{TTL: tc.ttl, RenewDivisor: tc.div}

			if got := l.RenewEvery(); got != tc.want {
				t.Errorf("RenewEvery() = %s, want %s", got, tc.want)
			}
			if l.RenewEvery() >= l.TTL {
				t.Error("a renewal at or after the ttl means the lease expires in the gap")
			}
		})
	}
}

func TestRedeliveryWindow(t *testing.T) {
	tests := []struct {
		name    string
		deliver int
		backoff []time.Duration
		want    time.Duration
	}{
		{
			name:    "the shipped schedule",
			deliver: 5,
			backoff: []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 60 * time.Second},
			want:    81 * time.Second,
		},
		{
			name:    "more deliveries than intervals reuses the last",
			deliver: 4,
			backoff: []time.Duration{time.Second, 2 * time.Second},
			want:    5 * time.Second,
		},
		{
			name:    "a single delivery never retries",
			deliver: 1,
			backoff: []time.Duration{time.Second},
			want:    0,
		},
		{
			name:    "no schedule at all",
			deliver: 5,
			backoff: nil,
			want:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := NATS{MaxDeliver: tc.deliver, Backoff: tc.backoff}

			if got := n.RedeliveryWindow(); got != tc.want {
				t.Errorf("RedeliveryWindow() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestCommentsAndQuotingAreAccepted keeps the committed file's own style
// working: the yaml is heavily commented, and one value is quoted.
func TestCommentsAndQuotingAreAccepted(t *testing.T) {
	cfg := mustLoad(t, `
# a leading comment

player:
  # an indented comment
  default_language: "en"   # a trailing comment
worker:
  batch_size: 42  # and another
`)

	if cfg.Player.DefaultLanguage != "en" {
		t.Errorf("DefaultLanguage = %q, want \"en\"", cfg.Player.DefaultLanguage)
	}
	if cfg.Worker.BatchSize != 42 {
		t.Errorf("BatchSize = %d, want 42", cfg.Worker.BatchSize)
	}
}

// The game clock moved from travel.time_scale to game.time_scale. A file or
// an environment that still writes the old key keeps working; where both are
// written the current key wins.
func TestLegacyTimeScaleKey(t *testing.T) {
	clearEnv(t)
	cfg := mustLoad(t, "travel:\n  time_scale: 30\n")
	if cfg.Game.TimeScale != 30 {
		t.Errorf("legacy key: TimeScale = %d, want 30", cfg.Game.TimeScale)
	}
	cfg = mustLoad(t, "game:\n  time_scale: 45\ntravel:\n  time_scale: 30\n")
	if cfg.Game.TimeScale != 45 {
		t.Errorf("both keys: TimeScale = %d, want game.time_scale's 45", cfg.Game.TimeScale)
	}
	t.Setenv("TORN_TRAVEL_TIME_SCALE", "20")
	cfg, err := Load(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Game.TimeScale != 20 {
		t.Errorf("legacy variable: TimeScale = %d, want 20", cfg.Game.TimeScale)
	}
}

// The committed default zone is Tehran, and it resolves from the embedded
// zone database whatever the host has installed.
func TestDefaultTimezone(t *testing.T) {
	cfg := Defaults()
	loc := cfg.Player.Location()
	if loc.String() != "Asia/Tehran" {
		t.Fatalf("Location() = %s, want Asia/Tehran", loc)
	}
	at := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC).In(loc)
	if at.Hour() != 13 || at.Minute() != 30 {
		t.Errorf("10:00 UTC in Tehran = %s, want 13:30", at.Format("15:04"))
	}
}
