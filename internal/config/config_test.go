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
  time_scale: 61
travel:
  arrival_xp: 26
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
  reverse_time: 5h
military:
  period: 25h
  readiness_loss_bps: 1001
  readiness_recovery_bps: 501
  reference_radar_km: 151
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
  hold_above: 50001
input:
  ttl: 7m
  cooldown: 4s
  max_length: 33
announce:
  window: 2m
  max_per_window: 7
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
`

// envOverrides is the same exercise through the environment. Every entry is a
// different value again, so a field reachable from the file but not from the
// environment is caught too.
var envOverrides = map[string]string{
	"TORN_GATEWAY_POLL_TIMEOUT":       "12s",
	"TORN_GATEWAY_POLL_ERROR_BACKOFF": "4s",
	"TORN_GATEWAY_SHUTDOWN_TIMEOUT":   "23s",
	"TORN_GATEWAY_SEND_ATTEMPTS":      "5",

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

	"TORN_GAME_SHUTDOWN_TIMEOUT":        "24s",
	"TORN_GAME_IDEMPOTENCY_TTL":         "22h",
	"TORN_GAME_CONTENT_RELOAD_INTERVAL": "32s",

	"TORN_TRAVEL_ARRIVAL_XP": "27",
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

	"TORN_TRADE_MARKET_ORDER_TTL":       "170h",
	"TORN_TRADE_MARKET_MAX_OPEN_ORDERS": "22",
	"TORN_TRADE_MARKET_MAX_QUANTITY":    "10002",
	"TORN_TRADE_MARKET_MAX_PRICE":       "100000002",
	"TORN_TRADE_AUCTION_DURATIONS":      "3h,8h",
	"TORN_TRADE_AUCTION_MAX_RESERVE":    "100000002",
	"TORN_TRADE_AUCTION_STEP_BPS":       "502",
	"TORN_TRADE_AUCTION_MIN_STEP":       "12",
	"TORN_TRADE_AUCTION_MAX_OPEN":       "7",
	"TORN_TRADE_AUCTION_RESERVES_BPS":   "3000,8000",

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
	"TORN_COMPANY_REVERSE_TIME":               "7h",
	"TORN_MILITARY_PERIOD":                    "26h",
	"TORN_MILITARY_READINESS_LOSS_BPS":        "1002",
	"TORN_MILITARY_READINESS_RECOVERY_BPS":    "502",
	"TORN_MILITARY_REFERENCE_RADAR_KM":        "152",
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
