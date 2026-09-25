import { useState } from 'react';
import { Action, Field } from '../components/Action.tsx';
import { Chart } from '../components/Chart.tsx';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Badge, Card, Code, ErrorBox, Grid, KV, Loading, Money, PageHeader, Table, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { foldSeries, sum } from '../lib/chart.ts';
import { post } from '../lib/api.ts';
import type { EconomySeries, Overview, Verification } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseAmount } from '../lib/validate.ts';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['dashboard', 'economy.tab_dashboard'],
  ['reasons', 'economy.tab_reasons'],
  ['ledger', 'economy.tab_ledger'],
  ['accounts', 'economy.tab_accounts'],
  ['verify', 'economy.tab_verify'],
  ['grant', 'economy.tab_grant'],
];

export function EconomyPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'dashboard';
  return (
    <>
      <PageHeader title={t('economy.title')} subtitle={t('economy.subtitle')} crumbs={[{ label: t('nav.economy') }]} />
      <Tabs active={tab} hrefOf={(id) => href(['economy', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'dashboard' && <Dashboard />}
      {tab === 'reasons' && <DataView view="ledger.reasons" pageSize={100} />}
      {tab === 'ledger' && <Ledger route={route} />}
      {tab === 'accounts' && <DataView view="accounts" pageSize={50} />}
      {tab === 'verify' && <Verify />}
      {tab === 'grant' && <Grant />}
    </>
  );
}

function Dashboard() {
  const { t, pct, val } = useI18n();
  const [days, setDays] = useState(30);
  const load = useLoad<EconomySeries>(`/api/series/economy?days=${days}`);
  const ov = useLoad<Overview>(`/api/overview?days=${days}`);
  const e = load.data;
  const label = (k: string) => (k === 'other' ? t('chart.other') : (val(k) ?? k));
  const priced = (e?.price_index_bps ?? []).filter((v) => v > 0);
  return (
    <>
      <div className="section-head">
        <RangePicker days={days} setDays={setDays} />
      </div>
      {load.error ? (
        <ErrorBox error={load.error} retry={load.reload} />
      ) : !e ? (
        <Loading rows={6} />
      ) : (
        <>
          <StatGrid>
            <Stat label={t('economy.supply')} value={<Money v={e.total} compact />} trend={e.supply} before={e.supply[0]} />
            <Stat label={t('overview.minted')} value={<Money v={sum(e.minted)} compact />} trend={e.minted} />
            <Stat label={t('overview.burned')} value={<Money v={sum(e.burned)} compact />} trend={e.burned} />
            <Stat label={t('overview.net')} value={<Money v={sum(e.minted) - sum(e.burned)} compact />} />
            <Stat label={t('kpi.price_index')} value={priced.length ? pct(priced[priced.length - 1] ?? 0) : '—'} trend={priced}
              before={priced[0]} good="down" hint={t('economy.inflation_hint')} />
          </StatGrid>
          <Grid>
            <Card title={t('overview.supply_over_time')}>
              <Chart title={t('overview.supply_over_time')} days={e.days} kind="area" unit="money"
                series={[{ key: 'supply', label: t('economy.supply'), values: e.supply }]} />
            </Card>
            <Card title={t('economy.price_index_chart')}>
              <Chart title={t('economy.price_index_chart')} days={e.days} kind="line" unit="bps"
                series={[{ key: 'index', label: t('kpi.price_index'), values: e.price_index_bps }]} />
            </Card>
          </Grid>
          <Card title={t('overview.faucets_drains')}>
            <Chart title={t('overview.faucets_drains')} days={e.days} kind="bar" unit="money"
              series={[
                { key: 'minted', label: t('overview.minted'), values: e.minted },
                { key: 'burned', label: t('overview.burned'), values: e.burned },
              ]} />
          </Card>
          <Grid>
            <Card title={t('overview.faucets_by_reason')}>
              {e.faucets.length === 0 ? <p className="muted">{t('chart.no_data')}</p> : (
                <Chart title={t('overview.faucets_by_reason')} days={e.days} kind="bar" stacked unit="money"
                  series={foldSeries(e.faucets, 7, 'other').map((l) => ({ key: l.key, label: label(l.key), values: l.values }))} />
              )}
            </Card>
            <Card title={t('overview.drains_by_reason')}>
              {e.drains.length === 0 ? <p className="muted">{t('chart.no_data')}</p> : (
                <Chart title={t('overview.drains_by_reason')} days={e.days} kind="bar" stacked unit="money"
                  series={foldSeries(e.drains, 7, 'other').map((l) => ({ key: l.key, label: label(l.key), values: l.values }))} />
              )}
            </Card>
          </Grid>
        </>
      )}
      <Grid>
        <Card title={t('overview.supply')}>
          <Async load={ov}>
            {(o) => (
              <Table rows={o.supply ?? []} rowKey={(f) => f.reason} cols={[
                { label: t('economy.account_kind'), cell: (f) => val(f.reason) ?? <Code>{f.reason}</Code> },
                { label: t('watch.amount'), cell: (f) => <Money v={f.amount} />, num: true },
                { label: '%', cell: (f) => pct(o.total ? (f.amount * 10000) / o.total : 0), num: true },
              ]} />
            )}
          </Async>
        </Card>
        <SeriesChart name="work.wages" title={t('economy.wages_chart')} days={days} kind="bar" stacked />
      </Grid>
      <p className="muted small">{t('economy.days_note', { n: days })}</p>
    </>
  );
}

// Ledger is the ledger explorer: every entry, or one transaction's, one
// account's, one player's…, with a drill-down from any transaction id.
function Ledger({ route }: { route: Route }) {
  const { t } = useI18n();
  const scope: Record<string, string | undefined> = {};
  for (const k of ['tx', 'account', 'reference', 'player', 'company', 'city', 'country', 'faction']) {
    const v = route.query.get(k);
    if (v) scope[k] = v;
  }
  const scoped = Object.entries(scope);
  return (
    <>
      {scoped.length > 0 && (
        <div className="scope-bar">
          {scoped.map(([k, v]) => (
            <Badge key={k} tone="info">
              {t(`scope.${k}` as Key)}: <Code>{v}</Code>
            </Badge>
          ))}
          <a className="btn small" href={href(['economy', 'ledger'])}>
            {t('table.clear')}
          </a>
        </div>
      )}
      {scope.account && <DataView view="accounts" scope={{ account: scope.account }} />}
      <DataView view="ledger" scope={scope} pageSize={50} />
    </>
  );
}

function Verify() {
  const { t, n } = useI18n();
  const [run, setRun] = useState(false);
  const verify = useLoad<Verification>(run ? '/api/economy/verify' : null);
  return (
    <Card title={t('economy.verify')} actions={
      <button type="button" className="btn primary" onClick={() => (run ? verify.reload() : setRun(true))}>
        {t('economy.run_verify')}
      </button>
    }>
      {!run && <p className="muted">{t('economy.verify_lead')}</p>}
      {run && (
        <Async load={verify}>
          {(v) => (
            <>
              <p className={v.ok ? 'ok-text' : 'error'} role="status">
                {v.ok ? t('economy.all_hold') : t('economy.some_fail')}
              </p>
              <KV cols={2} rows={[
                [t('economy.accounts'), n(v.accounts)],
                [t('economy.transactions'), n(v.transactions)],
                [t('economy.entries'), n(v.entries)],
                [t('economy.supply'), <Code key="s">{v.money_supply}</Code>],
              ]} />
              <ul className="checks">
                {v.checks.map((c, i) => (
                  <li key={i} className={c.ok ? 'pass' : 'fail'}>
                    <Badge tone={c.ok ? 'ok' : 'bad'}>{c.ok ? t('economy.pass') : t('economy.fail')}</Badge> <bdi dir="ltr">{c.text}</bdi>
                    {c.details && (
                      <ul>
                        {c.details.map((d) => (
                          <li key={d}>
                            <bdi dir="ltr">{d}</bdi>
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </Async>
      )}
    </Card>
  );
}

function Grant() {
  const { t } = useI18n();
  const [player, setPlayer] = useState('');
  const [amount, setAmount] = useState('');
  return (
    <>
      <Card title={t('economy.grant')}>
        <p className="warning-box">{t('economy.grant_warning')}</p>
        <div className="form-grid">
          <Field label={t('economy.grant_player')} value={player} onChange={setPlayer} dir="ltr" />
          <Field label={t('economy.grant_amount')} value={amount} onChange={setAmount} dir="ltr" inputMode="numeric" />
        </div>
        <Action label={t('economy.grant')} danger icon="coins" typed={player.trim().toUpperCase() || undefined}
          check={() => (!player.trim() || parseAmount(amount) === null ? t('common.invalid') : null)}
          run={(reason, key) => post('/api/economy/grant', { player: player.trim(), amount: parseAmount(amount), reason }, key)}
          done={() => t('toast.done')}>
          <p>
            <Code>{player.trim()}</Code> · <Money v={parseAmount(amount)} />
          </p>
        </Action>
      </Card>
      <DataView view="grants" defaultFilters={{ source: 'admin' }} live={['change']} />
    </>
  );
}
