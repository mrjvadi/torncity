import { useState, type ReactNode } from 'react';
import { Action, Field, Select } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { Icon } from '../components/Icon.tsx';
import { SeriesChart } from '../components/Stat.tsx';
import { Async, Badge, Card, Code, CopyButton, Grid, KV, Money, PageHeader, Ref, Status, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { PlayerDossier, Rec } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseAmount } from '../lib/validate.ts';
import { href, type Route } from '../router.ts';

export function PlayersPage({ route }: { route: Route }) {
  const code = route.segments[1];
  return code ? <Player code={code.toUpperCase()} tab={route.segments[2] ?? 'summary'} /> : <PlayerList route={route} />;
}

function PlayerList({ route }: { route: Route }) {
  const { t } = useI18n();
  const city = route.query.get('city') ?? undefined;
  return (
    <>
      <PageHeader title={t('players.title')} subtitle={t('players.subtitle')} crumbs={[{ label: t('nav.players') }]} />
      <DataView view="players" scope={{ city }} hide={['telegram_id', 'language', 'place', 'bank']} pageSize={25} />
    </>
  );
}

const TABS: [string, Key][] = [
  ['summary', 'player.tab_summary'],
  ['money', 'player.tab_money'],
  ['ledger', 'player.tab_ledger'],
  ['items', 'player.tab_items'],
  ['property', 'player.tab_property'],
  ['work', 'player.tab_work'],
  ['business', 'player.tab_business'],
  ['civic', 'player.tab_civic'],
  ['crime', 'player.tab_crime'],
  ['health', 'player.tab_health'],
  ['missions', 'player.tab_missions'],
  ['life', 'player.tab_life'],
  ['activity', 'player.tab_activity'],
  ['watch', 'player.tab_watch'],
  ['audit', 'player.tab_audit'],
];

const str = (r: Rec | null | undefined, k: string): string => (r && r[k] !== null && r[k] !== undefined ? String(r[k]) : '');
const numOf = (r: Rec | null | undefined, k: string): number | null => (r && typeof r[k] === 'number' ? (r[k] as number) : null);

function Player({ code, tab }: { code: string; tab: string }) {
  const { t } = useI18n();
  const load = useLoad<PlayerDossier>(`/api/dossier/players/${encodeURIComponent(code)}`);
  const d = load.data;
  const p = d?.player;
  const counts = d?.counts;
  const scope = { player: code };
  const count = (k: string) => numOf(counts, k);
  return (
    <>
      <PageHeader
        crumbs={[{ label: t('nav.players'), to: '#/players' }, { label: <Code>{code}</Code> }]}
        title={p ? <bdi>{str(p, 'name')}</bdi> : <Code>{code}</Code>}
        subtitle={p ? <PlayerLine p={p} /> : undefined}
        badge={
          <>
            {(d?.moderation ?? []).map((m) => <Status key={str(m, 'no')} value={str(m, 'kind')} />)}
            {p && str(p, 'status') !== 'active' && <Status value={str(p, 'status')} />}
          </>
        }
        actions={d ? <PlayerActions code={code} d={d} reload={load.reload} /> : undefined}
      />
      <Tabs
        active={tab}
        hrefOf={(id) => href(['players', code, id])}
        tabs={TABS.map(([id, label]) => ({
          id,
          label: t(label),
          count:
            id === 'watch' ? (count('open_flags') ?? 0) + (count('held_payments') ?? 0) : id === 'activity' ? numOf(d?.now, 'stuck_actions') : undefined,
        }))}
      />
      {tab === 'summary' && (
        <Async load={load} rows={6}>
          {(dd) => <Summary d={dd} />}
        </Async>
      )}
      {tab === 'money' && (
        <>
          <Async load={load}>{(dd) => <Accounts d={dd} />}</Async>
          <SeriesChart name="player.cash" title={t('player.money_flow')} days={30} kind="bar" scope={scope} />
          <DataView view="loans" scope={scope} hide={['borrower', 'borrower_kind', 'fees_due', 'fees_paid', 'recovered']} />
          <DataView view="loan.periods" scope={scope} />
          <DataView view="credit.events" scope={scope} hide={['player']} />
          <DataView view="insurance.policies" scope={scope} hide={['player']} />
          <DataView view="insurance.claims" scope={scope} hide={['player']} />
          <DataView view="savings.interest" scope={scope} hide={['player']} />
          <DataView view="shareholders" scope={scope} hide={['player']} />
          <DataView view="share.trades" scope={scope} />
          <DataView view="dividend.payments" scope={scope} hide={['player']} />
          <DataView view="gold.trades" scope={scope} hide={['player']} />
          <DataView view="grants" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'ledger' && <DataView view="ledger" scope={scope} pageSize={50} hide={['account']} />}
      {tab === 'items' && (
        <>
          <DataView view="inventory" scope={scope} hide={['player']} />
          <DataView view="pieces" scope={scope} hide={['owner']} />
          <DataView view="movements" scope={scope} />
          <DataView view="market.orders" scope={scope} hide={['owner']} />
          <DataView view="market.trades" scope={scope} />
          <DataView view="auctions" scope={scope} hide={['seller']} />
          <DataView view="auction.bids" scope={scope} hide={['bidder']} />
          <DataView view="shop.sales" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'property' && (
        <>
          <DataView view="properties" scope={scope} hide={['owner']} />
          <DataView view="leases" scope={scope} />
          <DataView view="property.listings" scope={scope} />
          <DataView view="property.charges" scope={scope} hide={['owner']} />
        </>
      )}
      {tab === 'work' && (
        <>
          <DataView view="employments" scope={scope} hide={['player']} />
          <DataView view="shifts" scope={scope} hide={['player']} />
          <DataView view="shift.sessions" scope={scope} hide={['player']} />
          <DataView view="enrollments" scope={scope} hide={['player']} />
          <DataView view="certificates" scope={scope} hide={['player']} />
          <DataView view="skills" scope={scope} hide={['player']} />
          <DataView view="company.applications" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'business' && (
        <>
          <DataView view="companies" scope={scope} hide={['rating_bps', 'price_bps', 'total_shares', 'closed_at']} />
          <DataView view="share.orders" scope={scope} hide={['owner']} />
          <DataView view="company.sales" scope={scope} />
        </>
      )}
      {tab === 'civic' && (
        <>
          <Async load={load}>{(dd) => <Offices d={dd} />}</Async>
          <DataView view="seats" scope={scope} hide={['holder']} />
          <DataView view="election.candidates" scope={scope} hide={['player']} />
          <DataView view="election.voters" scope={scope} />
          <DataView view="proposals" scope={scope} hide={['proposed_by', 'body', 'value_json']} />
          <DataView view="proposal.votes" scope={scope} hide={['player']} />
          <DataView view="policy.changes" scope={scope} hide={['set_by']} />
          <DataView view="factions" scope={scope} />
          <DataView view="faction.members" scope={scope} hide={['player']} />
          <DataView view="faction.crew" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'crime' && (
        <>
          <DataView view="criminals" scope={scope} hide={['player']} />
          <DataView view="crimes" scope={scope} />
          <DataView view="jail" scope={scope} hide={['player']} />
          <DataView view="crime.reports" scope={scope} />
        </>
      )}
      {tab === 'health' && (
        <>
          <DataView view="hospital.stays" scope={scope} hide={['player']} />
          <DataView view="hospital.treatments" scope={scope} hide={['player']} />
          <DataView view="sleeps" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'missions' && (
        <>
          <DataView view="missions" scope={scope} hide={['player']} />
          <DataView view="achievements" scope={scope} hide={['player']} />
          <DataView view="achievement.progress" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'life' && (
        <>
          <DataView view="life" scope={scope} hide={['player']} pageSize={50} />
          <DataView view="friends" scope={scope} />
          <DataView view="bots.links" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'activity' && (
        <>
          <Async load={load}>{(dd) => <NowCard d={dd} />}</Async>
          <DataView view="actions" scope={scope} live={["change"]} hide={['player', 'actor_type', 'claimed_by']} rowActions={(r) => <RequeueButton row={r} reload={load.reload} />} />
          <DataView view="travels" scope={scope} hide={['player']} />
          <DataView view="walks" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'watch' && (
        <>
          <DataView view="flags" scope={scope} />
          <DataView view="holds" scope={scope} />
          <DataView view="moderation" scope={scope} hide={['player']} />
        </>
      )}
      {tab === 'audit' && <DataView view="audit" scope={scope} />}
    </>
  );
}

function PlayerLine({ p }: { p: Rec }) {
  const { t } = useI18n();
  return (
    <span className="meta-line">
      <Code>{str(p, 'code')}</Code>
      {str(p, 'username') && <Code>@{str(p, 'username')}</Code>}
      <span>
        {t('player.telegram')} <Code>{str(p, 'telegram_id')}</Code> <CopyButton text={str(p, 'telegram_id')} />
      </span>
      {str(p, 'city') && (
        <span>
          <Icon name="city" size={14} /> <Ref kind="city" code={str(p, 'city')} />
          {str(p, 'place') && <Code>/{str(p, 'place')}</Code>}
        </span>
      )}
      <span>
        {t('players.last_active')}: <When iso={str(p, 'last_active_at')} rel />
      </span>
    </span>
  );
}

// PlayerActions are the operator's changes to a player.
function PlayerActions({ code, d, reload }: { code: string; d: PlayerDossier; reload: () => void }) {
  const { t } = useI18n();
  const [amount, setAmount] = useState('');
  const [hours, setHours] = useState('24');
  const [kind, setKind] = useState<'mute' | 'ban'>('mute');
  const standing = new Set((d.moderation ?? []).map((m) => str(m, 'kind')));
  const done = () => {
    reload();
    return t('toast.done');
  };
  return (
    <div className="action-bar">
      <Action label={t('player.grant')} icon="coins" small lead={t('economy.grant_warning')} danger
        check={() => (parseAmount(amount) === null ? t('common.invalid') : null)}
        run={(reason, key) => post('/api/economy/grant', { player: code, amount: parseAmount(amount), reason }, key)} done={done}>
        <Field label={t('economy.grant_amount')} value={amount} onChange={setAmount} inputMode="numeric" dir="ltr" />
      </Action>
      <Action label={t('moderation.impose')} icon="mute" small danger lead={t('moderation.lead')} typed={kind === 'ban' ? code : undefined}
        check={() => (!/^\d{1,5}$/.test(hours) ? t('common.invalid') : null)}
        run={(reason, key, confirm) => post(`/api/players/${encodeURIComponent(code)}/moderation`, { kind, hours: Number(hours), reason, confirm }, key)}
        done={done}>
        <Select label={t('moderation.kind')} value={kind} onChange={(v) => setKind(v === 'ban' ? 'ban' : 'mute')}
          options={[['mute', t('moderation.mute')], ['ban', t('moderation.ban')]]} />
        <Field label={t('moderation.hours')} value={hours} onChange={setHours} inputMode="numeric" dir="ltr" hint={t('moderation.hours_hint')} />
      </Action>
      {[...standing].map((k) => (
        <Action key={k} small label={t(k === 'ban' ? 'moderation.lift_ban' : 'moderation.lift_mute')} icon="check"
          run={(reason, key) => post(`/api/players/${encodeURIComponent(code)}/moderation/lift`, { kind: k, reason }, key)} done={done} />
      ))}
      {d.now?.jail ? (
        <Action small label={t('player.release')} icon="handcuffs" lead={t('player.release_lead')}
          run={(reason, key) => post(`/api/players/${encodeURIComponent(code)}/release`, { reason }, key)} done={done} />
      ) : null}
      {d.now?.hospital ? (
        <Action small label={t('player.discharge')} icon="heart" lead={t('player.discharge_lead')}
          run={(reason, key) => post(`/api/players/${encodeURIComponent(code)}/discharge`, { reason }, key)} done={done} />
      ) : null}
      <a className="btn small" href={href(['messages'], { only: code })}>
        <Icon name="message" size={16} />
        {t('player.message')}
      </a>
    </div>
  );
}

function Bar({ value, max, label, tone }: { value: number | null; max: number; label: string; tone?: 'good-high' | 'good-low' }) {
  const { n } = useI18n();
  if (value === null) return null;
  const ratio = Math.max(0, Math.min(1, value / (max || 1)));
  const bad = tone === 'good-low' ? ratio > 0.7 : ratio < 0.3;
  return (
    <div className="meter">
      <div className="meter-head">
        <span>{label}</span>
        <span className="num">
          {n(value)} / {n(max)}
        </span>
      </div>
      <div className="meter-track" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={max} aria-valuenow={value}>
        <div className={`meter-fill ${bad ? 'bad' : ''}`} style={{ width: `${ratio * 100}%` }} />
      </div>
    </div>
  );
}

function Summary({ d }: { d: PlayerDossier }) {
  const { t, n } = useI18n();
  const p = d.player;
  const c = d.counts;
  const cnt = (k: string) => n(numOf(c, k));
  return (
    <>
      <NowCard d={d} />
      <Grid cols={3}>
        <Card title={t('player.stats')}>
          <KV rows={[
            [t('player.level'), n(numOf(p, 'level'))],
            [t('player.xp'), n(numOf(p, 'xp'))],
            [t('player.reputation'), n(numOf(p, 'reputation'))],
            [t('player.stamina'), n(numOf(p, 'stamina'))],
          ]} />
          <Bar label={t('player.health')} value={numOf(p, 'health')} max={numOf(p, 'max_health') ?? 100} />
          <Bar label={t('player.energy')} value={numOf(p, 'energy')} max={numOf(p, 'max_energy') ?? 100} />
          <Bar label={t('player.happiness')} value={numOf(p, 'happiness')} max={100} />
        </Card>
        <Card title={t('player.needs')}>
          <Bar label={t('player.hunger')} value={numOf(p, 'hunger')} max={100} tone="good-low" />
          <Bar label={t('player.sleep')} value={numOf(p, 'sleep')} max={100} tone="good-low" />
          <Bar label={t('player.stress')} value={numOf(p, 'stress')} max={100} tone="good-low" />
          <KV rows={[
            [t('player.age'), d.age !== null ? `${n(d.age)}${d.stage ? ` · ${d.stage}` : ''}` : '—'],
            [t('player.intelligence'), n(numOf(p, 'intelligence'))],
            [t('player.rank'), str(p, 'rank') ? <Code>{str(p, 'rank')}</Code> : '—'],
            [t('player.net_worth'), <Money key="nw" v={numOf(p, 'net_worth')} />],
            [t('player.last_sleep'), <When key="ls" iso={str(p, 'last_sleep_at')} rel />],
          ]} />
        </Card>
        <Card title={t('player.identity')}>
          <KV rows={[
            [t('players.status'), <Status key="s" value={str(p, 'status')} />],
            [t('player.language'), <Code key="l">{str(p, 'language')}</Code>],
            [t('player.joined'), <When key="j" iso={str(p, 'created_at')} />],
            [t('player.residence'), <Ref key="r" kind="city" code={str(p, 'residence')} />],
            [t('player.faction'), str(p, 'faction') ? <Ref key="f" kind="faction" code={str(p, 'faction')} label={`${str(p, 'faction')} · ${str(p, 'faction_rank')}`} /> : '—'],
            [t('player.heat'), n(numOf(p, 'heat'))],
            [t('player.crimes'), `${cnt('crimes')} · ${t('player.convictions')} ${n(numOf(p, 'convictions'))}`],
          ]} />
        </Card>
      </Grid>
      <Grid cols={2}>
        <Accounts d={d} />
        <Card title={t('player.at_a_glance')}>
          <KV cols={2} rows={[
            [t('player.companies_owned'), cnt('companies_owned')],
            [t('player.companies_managed'), cnt('companies_managed')],
            [t('player.holdings'), cnt('holdings')],
            [t('player.properties'), cnt('properties')],
            [t('player.renting'), cnt('renting')],
            [t('player.loans_active'), cnt('loans_active')],
            [t('player.loans_outstanding'), <Money key="lo" v={numOf(c, 'loans_outstanding')} />],
            [t('player.instalments'), `${cnt('instalments_on_time')} / ${cnt('instalments_missed')}`],
            [t('player.policies'), cnt('policies')],
            [t('player.achievements'), cnt('achievements')],
            [t('player.missions'), `${cnt('missions_active')} / ${cnt('missions_done')}`],
            [t('player.friends'), cnt('friends')],
            [t('player.certificates'), cnt('certificates')],
            [t('player.pieces'), cnt('pieces')],
            [t('player.gold'), n(numOf(p, 'gold_grams'))],
            [t('player.portfolio'), <Money key="pf" v={numOf(p, 'portfolio_value')} />],
            [t('player.equity'), <Money key="eq" v={numOf(p, 'equity')} />],
            [t('player.hospital_stays'), cnt('hospital_stays')],
          ]} />
          {numOf(p, 'unpaid_fines') ? <p className="warning-box">{t('player.unpaid_fines', { n: numOf(p, 'unpaid_fines') ?? 0 })}</p> : null}
        </Card>
      </Grid>
      <Offices d={d} />
    </>
  );
}

function Accounts({ d }: { d: PlayerDossier }) {
  const { t, val } = useI18n();
  const total = d.accounts.reduce((a, b) => a + b.balance, 0);
  return (
    <Card title={t('player.balances')}>
      <KV rows={[
        ...d.accounts.map((a): [string, ReactNode] => [val(a.kind) ?? a.kind, <a key={a.account} href={href(['economy', 'ledger'], { account: a.account })}><Money v={a.balance} /></a>]),
        [t('overview.total'), <strong key="t"><Money v={total} /></strong>],
      ]} />
    </Card>
  );
}

function Offices({ d }: { d: PlayerDossier }) {
  const { t } = useI18n();
  if (d.offices.length === 0) return null;
  return (
    <Card title={t('player.offices')}>
      <ul className="chip-list">
        {d.offices.map((o, i) => (
          <li key={i}>
            <Badge tone="info">
              <Code>{`${str(o, 'office')}#${str(o, 'seat')}`}</Code> · <Code>{`${str(o, 'place_kind')}:${str(o, 'place')}`}</Code>
            </Badge>{' '}
            <span className="muted small">
              {t('gov.term_ends')}: <When iso={str(o, 'term_ends_at')} />
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

// NowCard is what the player is doing at this moment.
function NowCard({ d }: { d: PlayerDossier }) {
  const { t, n } = useI18n();
  const now = d.now ?? {};
  const item = (k: string, icon: string, body: (r: Rec) => ReactNode) => {
    const r = now[k] as Rec | null | undefined;
    if (!r) return null;
    return (
      <li key={k}>
        <Icon name={icon} />
        <div>
          <strong>{t(`now.${k}` as Key)}</strong> {body(r)}
        </div>
      </li>
    );
  };
  const ends = (r: Rec, k: string) => (
    <span className="muted small">
      {t('now.until')} <When iso={str(r, k)} rel />
    </span>
  );
  const list = [
    item('jail', 'handcuffs', (r) => <>{ends(r, 'ends_at')} <Code>{str(r, 'city')}</Code></>),
    item('hospital', 'heart', (r) => <>{ends(r, 'ends_at')} <Status value={str(r, 'cause')} /></>),
    item('travel', 'globe', (r) => <><Ref kind="city" code={str(r, 'from')} /> → <Ref kind="city" code={str(r, 'to')} /> <Code>{str(r, 'mode')}</Code> {ends(r, 'arrives_at')}</>),
    item('walk', 'activity', (r) => <><Code>{str(r, 'from')}</Code> → <Code>{str(r, 'to')}</Code> {ends(r, 'arrives_at')}</>),
    item('shift', 'briefcase', (r) => <><Ref kind="company" code={str(r, 'company')} /> {ends(r, 'ends_at')}</>),
    item('study', 'file', (r) => <><Code>{str(r, 'course')}</Code> {r.paused_at ? <Badge tone="warn">{t('now.paused')}</Badge> : ends(r, 'completes_at')}</>),
    item('crime', 'target', (r) => <><Code>{str(r, 'crime')}</Code> {ends(r, 'resolves_at')}</>),
    item('job', 'briefcase', (r) => <><Code>{str(r, 'career')}</Code> · {t('company.tier')} {n(numOf(r, 'tier'))} · <Money v={numOf(r, 'rate')} /> {str(r, 'company') && <Ref kind="company" code={str(r, 'company')} />}</>),
  ].filter(Boolean);
  const stuck = numOf(now, 'stuck_actions') ?? 0;
  return (
    <Card title={t('now.title')}>
      {list.length === 0 ? <p className="muted">{t('now.idle')}</p> : <ul className="now-list">{list}</ul>}
      {stuck > 0 && (
        <p className="warning-box">
          <Icon name="alert" /> <a href={href(['players', str(d.player, 'code'), 'activity'])}>{t('now.stuck', { n: stuck })}</a>
        </p>
      )}
    </Card>
  );
}

// RequeueButton hands a failed or stuck timed action back to the scheduler.
export function RequeueButton({ row, reload }: { row: Rec; reload: () => void }) {
  const { t } = useI18n();
  const status = String(row.status ?? '');
  if (status !== 'failed' && status !== 'running') return null;
  return (
    <Action small label={t('actions.requeue')} icon="refresh" lead={t('actions.requeue_lead')}
      run={(reason, key) => post(`/api/actions/${encodeURIComponent(String(row.id))}/requeue`, { reason }, key)}
      done={() => {
        reload();
        return t('toast.done');
      }}>
      <p>
        <Code>{String(row.type)}</Code> · <Status value={status} />
        {row.last_error ? (
          <>
            <br />
            <span className="muted small">
              <bdi dir="ltr">{String(row.last_error)}</bdi>
            </span>
          </>
        ) : null}
      </p>
    </Action>
  );
}
