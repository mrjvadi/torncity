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
