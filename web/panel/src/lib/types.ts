// The API's shapes (internal/panel/backend.go, internal/infrastructure/
// postgres/panel_reads.go and panel_lists.go).

export interface Flow {
  reason: string;
  amount: number;
}

export interface Counts {
  players: number;
  active_15m: number;
  active_24h: number;
  companies: number;
  cities: number;
  open_flags: number;
  held_payments: number;
}

export interface Overview {
  days: number;
  since: string;
  supply: Flow[] | null;
  total: number;
  faucets: Flow[] | null;
  drains: Flow[] | null;
  price_index_bps: number;
  prior_index_bps: number;
  counts: Counts;
}

export interface PlayerHit {
  code: string;
  name: string;
  username: string;
  telegram_id: number;
  status: string;
  city: string;
  last_active: string | null;
}

export interface LifeEntry {
  kind: string;
  at: string;
  public: boolean;
  backfilled: boolean;
  data: Record<string, unknown>;
}

export interface FlagLine {
  no: number;
  rule: string;
  player: string;
  other: string;
  score: number;
  hits: number;
  status: string;
  evidence?: Record<string, number>;
  created_at: string;
  updated_at: string;
  cleared_at?: string;
  cleared_by?: string;
  note?: string;
}

export interface Licence {
  no: number;
  company: string;
  kind: string;
  basis: string;
  status: string;
  decided_office: string;
  decided_at: string | null;
  revoked_at: string | null;
  effective_at: string | null;
}

export interface PlayerDetail {
  id: string;
  code: string;
  name: string;
  created_at: string;
  city: string;
  place: string;
  residence: string;
  state: 'here' | 'jail' | 'hospital' | 'travelling';
  job: string;
  renting: string;
  balances: Flow[] | null;
  companies: string[] | null;
  properties: string[] | null;
  offices: string[] | null;
  achievements: number;
  open_flags: number;
  extra: {
    telegram_id: number;
    username: string;
    status: string;
    language: string;
    last_active: string | null;
    history: LifeEntry[] | null;
    flags: FlagLine[] | null;
    licences: Licence[] | null;
  };
}

export interface CityLine {
  code: string;
  name: string;
  country: string;
  treasury: number;
  residents: number;
  present: number;
  groups: number;
}

export interface Seat {
  jurisdiction_kind: string;
  jurisdiction_code: string;
  office: string;
  seat: number;
  holder: string;
  acquired_by: string;
  since: string | null;
  term_ends_at: string | null;
}

export interface Group {
  chat_id: number;
  language: string;
  linked_by: string;
  linked_at: string;
}

export interface CityDetail {
  code: string;
  name: string;
  country: string;
  treasury: number;
  residents: number;
  present: number;
  companies: number;
  properties: number;
  damage_bps: number;
  allocation_bps: Record<string, number> | null;
  last_budget: number | null;
  last_budget_lines: Flow[] | null;
  groups: Group[] | null;
  seats: Seat[] | null;
}

export interface CompanyLine {
  code: string;
  name: string;
  kind: string;
  city: string;
  status: string;
  owner: string;
  treasury: number;
  debt: number;
  staff: number;
}

export interface CompanyDetail extends CompanyLine {
  founded_at: string;
  closed_at: string | null;
  close_reason: string;
  manager: string;
  price_bps: number;
  rating_bps: number;
  arrears: number;
  reserved_wages: number;
  total_shares: number;
  shares: { player: string; shares: number }[] | null;
  staff_list: { player: string; career: string; tier: number; wage: number; shifts: number; working: boolean }[] | null;
  periods: { no: number; revenue: number; wages: number; upkeep: number; balance: number; insolvent: boolean }[] | null;
  licences: Licence[] | null;
}

export interface Lever {
  code: string;
  type: string;
  supported: boolean;
  value: number;
  min: number;
  max: number;
  source: string;
  set_by: string;
  since: string | null;
  pending: number | null;
  pending_at: string | null;
  held_by: string;
  decision: string;
  decided_by: string[] | null;
  clamped: boolean;
  cooldown: string;
  notice: string;
}

export interface PolicyPlace {
  kind: string;
  code: string;
  name: string;
  levers: Lever[] | null;
}

export interface ElectionLine {
  no: number;
  office: string;
  jurisdiction_kind: string;
  jurisdiction_code: string;
  seats: number;
  status: string;
  opens_at: string;
  candidacy_ends_at: string;
  voting_ends_at: string;
  counted_at: string | null;
  votes_cast: number | null;
  candidates: number;
}

export interface Check {
  ok: boolean;
  text: string;
  details?: string[];
}

export interface Verification {
  ok: boolean;
  accounts: number;
  transactions: number;
  entries: number;
  money_supply: string;
  checks: Check[];
}

export interface ContentStatus {
  version: number;
  version_id: string;
  loaded_at: string;
  loaded_by: string;
  reason: string;
  checksum: string;
  local_checksum: string;
  local_error: string;
  matches: boolean;
  counts: Record<string, number> | null;
  warnings: string[] | null;
}

export interface HoldLine {
  no: number;
  payer: string;
  payee: string;
  method: string;
  amount: number;
  status: string;
  created_at: string;
}

export interface AuditLine {
  id: number;
  actor: string;
  action: string;
  target_type: string;
  new_value: unknown;
  reason: string;
  at: string;
}

// The console's lists (GET /api/views/{name}).
export interface ViewCol {
  key: string;
  type: string;
  sort: boolean;
}

export interface ViewFilter {
  key: string;
  options?: string[];
  prefix?: boolean;
}

export interface ViewPage {
  name: string;
  columns: ViewCol[];
  filters: ViewFilter[];
  scopes: string[];
  search: boolean;
  time: boolean;
  order: string;
  desc: boolean;
  rows: unknown[][];
  more: boolean;
  offset: number;
  limit: number;
}

export type ViewRow = Record<string, unknown>;

export type Rec = Record<string, unknown>;

// A chart's data (GET /api/series/{name}).
export interface SeriesLine {
  key: string;
  values: number[];
  total: number;
}

export interface Series {
  name: string;
  unit: string;
  days: string[];
  lines: SeriesLine[];
}

export interface EconomySeries {
  days: string[];
  supply: number[];
  minted: number[];
  burned: number[];
  faucets: SeriesLine[];
  drains: SeriesLine[];
  price_index_bps: number[];
  total: number;
}

export interface SearchHit {
  kind: string;
  code: string;
  name: string;
  extra: string;
}

export interface PlayerDossier {
  player: Rec;
  accounts: { account: string; kind: string; balance: number }[];
  now: Rec;
  counts: Rec;
  offices: Rec[];
  moderation: Rec[];
  age: number | null;
  stage: string;
}

export interface SectionDiff {
  name: string;
  local: number;
  active: number;
  added: string[];
  removed: string[];
  changed: string[];
}

export interface ContentDiff {
  version: number;
  local_error: string;
  sections: SectionDiff[];
}

export interface NATSStream {
  name: string;
  messages: number;
  bytes: number;
  consumers: { name: string; pending: number; ack_pending: number; redelivered: number }[];
}

export interface NATSStatus {
  url: string;
  error?: string;
  streams: NATSStream[];
}

export interface SystemStatus {
  db_ok: boolean;
  db_ms: number;
  db_error?: string;
  health?: Rec;
  content?: Rec;
  schema?: Rec;
  postgres?: Rec;
  nats?: NATSStatus;
}

// SwitchStatus is one operator switch's row (migrations/0041), plus the
// value the gateway is actually acting on and how stale that cached answer
// is (undefined: nothing cached to report, or the panel has no Redis of its
// own — the row's own value is then what the gateway falls back to anyway).
export interface SwitchStatus {
  key: string;
  value: string;
  changed_by: string;
  changed_at: string;
  reason: string;
  effective: string;
  cache_age_seconds?: number;
}

export interface SwitchesView {
  switches: SwitchStatus[];
  mini_app_url: string;
  mini_app_url_missing: boolean;
}

export interface SwitchHistoryEntry {
  at: string;
  actor: string;
  reason: string;
  key: string;
  value: string;
}

export interface SessionLine {
  id: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  client_ip: string;
  user_agent: string;
  current: boolean;
}

export interface Me {
  username: string;
  totp_enabled: boolean;
  last_login_at: string | null;
  created_at: string;
  created_by: string;
  password_min_length: number;
  session_expires_at: string;
}
