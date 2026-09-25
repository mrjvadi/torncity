import { DataView } from '../components/DataView.tsx';
import { SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Card, Code, Grid, KV, PageHeader, Status, Table, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { useLive } from '../lib/live.tsx';
import type { Rec, SystemStatus } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';
import { RequeueButton } from './Players.tsx';

const TABS: [string, Key][] = [
  ['health', 'system.tab_health'],
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
