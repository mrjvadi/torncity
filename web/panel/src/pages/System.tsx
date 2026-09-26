import { useState } from 'react';
import { Action, Select } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Card, Code, Grid, KV, PageHeader, Status, Table, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { useLive } from '../lib/live.tsx';
import { post } from '../lib/api.ts';
import type { Rec, SwitchesView, SwitchHistoryEntry, SwitchStatus, SystemStatus } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';
import { RequeueButton } from './Players.tsx';

const TABS: [string, Key][] = [
  ['health', 'system.tab_health'],
  ['switches', 'system.tab_switches'],
  ['actions', 'system.tab_actions'],
  ['outbox', 'system.tab_outbox'],
  ['clocks', 'system.tab_clocks'],
  ['bots', 'system.tab_bots'],
];

const nOf = (r: Rec | undefined, k: string) => (r && typeof r[k] === 'number' ? (r[k] as number) : 0);
const s = (r: Rec | undefined, k: string) => (r && r[k] !== null && r[k] !== undefined ? String(r[k]) : '');

export function SystemPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'health';
  const action = route.query.get('action') ?? undefined;
  const reference = route.query.get('reference') ?? undefined;
  return (
    <>
      <PageHeader title={t('system.title')} subtitle={t('system.subtitle')} crumbs={[{ label: t('nav.system') }]} />
      <Tabs active={tab} hrefOf={(id) => href(['system', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'health' && <Health />}
      {tab === 'switches' && <Switches />}
      {tab === 'actions' && (
        <>
          <DataView view="action.backlog" />
          {action || reference ? (
            <DataView view="actions" scope={{ action, reference }} live={['change']} rowActions={(r) => <RequeueButton row={r} reload={() => {}} />} />
          ) : (
            <DataView view="actions" title={t('system.failed_actions')} defaultFilters={{ status: 'failed' }} live={['change']}
              rowActions={(r) => <RequeueButton row={r} reload={() => {}} />} />
          )}
        </>
      )}
      {tab === 'outbox' && <DataView view="outbox" defaultFilters={{ status: 'pending' }} />}
      {tab === 'clocks' && <DataView view="clocks" pageSize={100} />}
      {tab === 'bots' && (
        <>
          <DataView view="bots" />
          <DataView view="migrations" pageSize={100} />
        </>
      )}
    </>
  );
}

function Health() {
  const { t, n, nc } = useI18n();
  const live = useLive();
  const load = useLoad<SystemStatus>('/api/system');
  return (
    <Async load={load} rows={6}>
      {(sys) => {
        const h = sys.health;
        return (
          <>
            <StatGrid>
              <Stat label={t('system.database')} value={sys.db_ok ? <Status value="ok" /> : <Status value="down" />} hint={t('system.ms', { n: sys.db_ms })} />
              <Stat label={t('system.live')} value={<Status value={live.status === 'connected' ? 'ok' : live.status === 'off' ? 'expired' : 'lagging'} />}
                hint={t(`live.${live.status}` as Key)} />
              <Stat label={t('system.outbox_pending')} value={nc(nOf(h, 'outbox_pending'))} tone={nOf(h, 'outbox_failed') > 0 ? 'bad' : undefined}
                hint={t('system.outbox_failed', { n: nOf(h, 'outbox_failed') })} href={href(['system', 'outbox'])} />
              <Stat label={t('system.actions_overdue')} value={n(nOf(h, 'actions_overdue'))} tone={nOf(h, 'actions_overdue') > 0 ? 'warn' : undefined}
                href={href(['system', 'actions'])} />
              <Stat label={t('system.actions_stuck')} value={n(nOf(h, 'actions_stuck'))} tone={nOf(h, 'actions_stuck') > 0 ? 'bad' : undefined} />
              <Stat label={t('system.actions_failed')} value={n(nOf(h, 'actions_failed'))} tone={nOf(h, 'actions_failed') > 0 ? 'bad' : undefined}
                href={href(['system', 'actions'])} />
            </StatGrid>
            {sys.db_error && <p className="error">{sys.db_error}</p>}
            <Grid>
              <Card title={t('system.versions')}>
                <KV rows={[
                  [t('system.content_version'), sys.content ? <span key="c">{n(nOf(sys.content, 'version'))} · <When iso={s(sys.content, 'loaded_at')} rel /></span> : '—'],
                  [t('system.schema'), <Code key="s">{s(sys.schema, 'version')}</Code>],
                  [t('system.postgres'), <Code key="p">{s(sys.postgres, 'version')}</Code>],
                  [t('system.db_size'), `${nc(nOf(sys.postgres, 'size_bytes'))}B`],
                  [t('system.connections'), n(nOf(sys.postgres, 'connections'))],
                  [t('system.outbox_oldest'), <When key="o" iso={s(h, 'outbox_oldest')} rel />],
                ]} />
              </Card>
              <Card title={t('system.nats')} subtitle={sys.nats?.url}>
                {sys.nats?.error ? (
                  <p className="warn-text">{sys.nats.error}</p>
                ) : (
                  <Table rows={(sys.nats?.streams ?? []).flatMap((st) => (st.consumers.length ? st.consumers : [{ name: '—', pending: 0, ack_pending: 0, redelivered: 0 }]).map((c) => ({ st, c })))}
                    rowKey={(x) => `${x.st.name}/${x.c.name}`} cols={[
                      { label: t('system.stream'), cell: (x) => <Code>{x.st.name}</Code> },
                      { label: t('system.messages'), cell: (x) => nc(x.st.messages), num: true },
                      { label: t('system.consumer'), cell: (x) => <Code>{x.c.name}</Code> },
                      { label: t('system.pending'), cell: (x) => n(x.c.pending), num: true },
                      { label: t('system.ack_pending'), cell: (x) => n(x.c.ack_pending), num: true },
                      { label: t('system.redelivered'), cell: (x) => n(x.c.redelivered), num: true },
                    ]} />
                )}
              </Card>
            </Grid>
            <SeriesChart name="audit.actions" title={t('system.audit_chart')} days={30} kind="bar" stacked />
          </>
        );
      }}
    </Async>
  );
}

// Switches is System > Switches (migrations/0041_runtime_switches): the
// operator's runtime switches, the value the gateway is actually acting on
// and its cache age, the web game's Mini App URL (read-only from
// configs/config.yml), and the history of past changes.
function Switches() {
  const { t } = useI18n();
  const load = useLoad<SwitchesView>('/api/switches');
  const history = useLoad<SwitchHistoryEntry[]>('/api/switches/history');
  const reload = () => {
    load.reload();
    history.reload();
  };
  return (
    <Async load={load}>
      {(view) => (
        <>
          <StatGrid>
            {view.switches
              .filter((s) => s.key === 'telegram_play' || s.key === 'telegram_notices')
              .map((s) => (
                <Stat key={s.key} label={switchLabel(t, s.key)} value={<Status value={s.effective} />}
                  hint={s.changed_at ? t('switch.changed_hint', { by: s.changed_by }) : t('switch.never_set')} />
              ))}
          </StatGrid>
          {view.mini_app_url_missing && <p className="warning-box">{t('switch.mini_app_missing')}</p>}
          <Card title={t('switch.mini_app_url')} subtitle={t('switch.mini_app_url_hint')}>
            <KV rows={[[t('switch.mini_app_url'), view.mini_app_url ? <Code key="u">{view.mini_app_url}</Code> : t('switch.mini_app_missing_short')]]} />
          </Card>
          <Card title={t('system.tab_switches')}>
            <Table rows={view.switches} rowKey={(s) => s.key} cols={[
              { label: t('switch.name'), cell: (s) => <Code>{s.key}</Code> },
              { label: t('switch.effective'), cell: (s) => <Status value={s.effective} /> },
              { label: t('switch.cache_age'), cell: (s) => cacheAgeLabel(s, t) },
              { label: t('common.reason'), cell: (s) => s.reason || '—' },
              { label: t('switch.changed_at'), cell: (s) => (s.changed_at ? <When iso={s.changed_at} rel /> : '—') },
              { label: t('switch.changed_by'), cell: (s) => s.changed_by || '—' },
              { label: '', cell: (s) => <SwitchChange s={s} reload={reload} /> },
            ]} />
          </Card>
          <Card title={t('switch.history')}>
            <Async load={history}>
              {(rows) => (
                <Table rows={rows} rowKey={(r) => `${r.key}-${r.at}`} cols={[
                  { label: t('switch.changed_at'), cell: (r) => <When iso={r.at} rel /> },
                  { label: t('switch.name'), cell: (r) => <Code>{r.key}</Code> },
                  { label: t('switch.effective'), cell: (r) => <Status value={r.value} /> },
                  { label: t('switch.changed_by'), cell: (r) => r.actor },
                  { label: t('common.reason'), cell: (r) => r.reason },
                ]} />
              )}
            </Async>
          </Card>
        </>
      )}
    </Async>
  );
}

type Translate = ReturnType<typeof useI18n>['t'];

// switchLabel is a switch's name in words, when this page knows it, else the
// raw key — so a future switch reused off this table is still readable.
function switchLabel(t: Translate, key: string): string {
  const named: Record<string, Key> = { telegram_play: 'switch.name_telegram_play', telegram_notices: 'switch.name_telegram_notices' };
  return named[key] ? t(named[key]) : key;
}

function cacheAgeLabel(s: SwitchStatus, t: Translate): string {
  if (s.cache_age_seconds === undefined) return t('switch.cache_unknown');
  return t('switch.cache_age_value', { n: Math.round(s.cache_age_seconds) });
}

// SWITCH_OPTIONS names the values each known switch takes, in the order
// they are offered. A switch not listed here (a future one, reused off this
// same generic table) falls back to its own current value as the only
// option, so the row can still be read even though this page does not yet
// know its shape.
const SWITCH_OPTIONS: Record<string, [string, Key][]> = {
  telegram_play: [
    ['on', 'switch.value_on'],
    ['groups_off', 'switch.value_groups_off'],
    ['off', 'switch.value_off'],
  ],
  telegram_notices: [
    ['on', 'switch.value_on'],
    ['off', 'switch.value_off'],
  ],
};

// SwitchChange is one switch's "change" button: a value picker behind the
// same reason-plus-typed-confirm dialog every other console change uses,
// typed to the switch's own key so a hasty click cannot flip the wrong one.
function SwitchChange({ s, reload }: { s: SwitchStatus; reload: () => void }) {
  const { t } = useI18n();
  const [value, setValue] = useState(s.value);
  const known = SWITCH_OPTIONS[s.key];
  const options: [string, string][] = known ? known.map(([v, label]) => [v, t(label)]) : [[s.value, s.value]];
  return (
    <Action small label={t('switch.change')} icon="server" typed={s.key} lead={t('switch.change_lead')}
      run={(reason, key, confirm) => post(`/api/switches/${encodeURIComponent(s.key)}`, { value, reason, confirm }, key)}
      done={() => {
        reload();
        return t('toast.done');
      }}>
      <Select label={t('switch.value')} value={value} onChange={setValue} options={options} />
    </Action>
  );
}
