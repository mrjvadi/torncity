import { Action } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Card, Code, Grid, KV, Money, PageHeader, Ref, Status, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { Rec } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';

export function CompaniesPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const code = route.segments[1];
  if (code) return <Company code={code.toUpperCase()} tab={route.segments[2] ?? 'summary'} />;
  return (
    <>
      <PageHeader title={t('companies.title')} subtitle={t('companies.subtitle')} crumbs={[{ label: t('nav.companies') }]} />
      <DataView view="companies" hide={['manager', 'price_bps', 'rating_bps', 'total_shares', 'closed_at', 'close_reason']} defaultFilters={{ status: 'active' }} />
    </>
  );
}

const TABS: [string, Key][] = [
  ['summary', 'company.tab_summary'],
  ['periods', 'company.tab_periods'],
  ['staff', 'company.tab_staff'],
  ['production', 'company.tab_production'],
  ['stock', 'company.tab_stock'],
  ['sales', 'company.tab_sales'],
  ['shares', 'company.tab_shares'],
  ['finance', 'company.tab_finance'],
  ['licences', 'company.tab_licences'],
  ['audit', 'company.tab_audit'],
];

const s = (r: Rec | undefined, k: string) => (r && r[k] !== null && r[k] !== undefined ? String(r[k]) : '');
const nOf = (r: Rec | undefined, k: string) => (r && typeof r[k] === 'number' ? (r[k] as number) : null);

function Company({ code, tab }: { code: string; tab: string }) {
  const { t, n, pct } = useI18n();
  const load = useLoad<{ company: Rec; counts: Rec }>(`/api/dossier/companies/${encodeURIComponent(code)}`);
  const c = load.data?.company;
  const k = load.data?.counts;
  const scope = { company: code };
  const done = () => {
    load.reload();
    return t('toast.done');
  };
  return (
    <>
      <PageHeader
        crumbs={[{ label: t('nav.companies'), to: '#/companies' }, { label: <Code>{code}</Code> }]}
        title={c ? <bdi>{s(c, 'name')}</bdi> : <Code>{code}</Code>}
        subtitle={
          c && (
            <span className="meta-line">
              <Code>{code}</Code> <Code>{s(c, 'type')}</Code> <Ref kind="city" code={s(c, 'city')} />
              <span>
                {t('companies.owner')}: <Ref kind="player" code={s(c, 'owner')} />
              </span>
              {s(c, 'manager') && (
                <span>
                  {t('company.manager')}: <Ref kind="player" code={s(c, 'manager')} />
                </span>
              )}
            </span>
          )
        }
        badge={c && <Status value={s(c, 'status')} />}
        actions={
          c && (
            <div className="action-bar">
              <Action small label={t('company.grant_licence')} icon="shield" run={(reason, key) => post(`/api/companies/${encodeURIComponent(code)}/defence/grant`, { reason }, key)} done={done} />
              <Action small danger label={t('company.revoke_licence')} run={(reason, key) => post(`/api/companies/${encodeURIComponent(code)}/defence/revoke`, { reason }, key)} done={done} />
              {s(c, 'status') === 'active' && (
                <Action small danger label={t('company.dissolve')} icon="ban" lead={t('company.dissolve_lead')} typed={code}
                  run={(reason, key, confirm) => post(`/api/companies/${encodeURIComponent(code)}/dissolve`, { reason, confirm }, key)} done={done} />
              )}
            </div>
          )
        }
      />
      <Tabs active={tab} hrefOf={(id) => href(['companies', code, id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'summary' && (
        <Async load={load} rows={5}>
          {() => (
            <>
              <StatGrid>
                <Stat label={t('companies.treasury')} value={<Money v={nOf(c, 'treasury')} compact />} />
                <Stat label={t('companies.debt')} value={<Money v={nOf(c, 'debt')} compact />} tone={(nOf(c, 'debt') ?? 0) > 0 ? 'warn' : undefined} />
                <Stat label={t('companies.staff')} value={n(nOf(k, 'staff'))} hint={t('company.working_now', { n: nOf(k, 'working') ?? 0 })} />
                <Stat label={t('company.revenue_total')} value={<Money v={nOf(k, 'revenue_total')} compact />} hint={t('company.periods_n', { n: nOf(k, 'periods') ?? 0 })} />
                <Stat label={t('company.stock_units')} value={n(nOf(k, 'stock_units'))} />
                <Stat label={t('company.shareholders_n')} value={n(nOf(k, 'shareholders'))} />
              </StatGrid>
              <Grid>
                <Card title={t('company.books')}>
                  <KV rows={[
                    [t('company.reserved'), <Money key="r" v={nOf(c, 'reserved_wages')} />],
                    [t('company.arrears'), n(nOf(c, 'arrears'))],
                    [t('company.price'), pct(nOf(c, 'price_bps'))],
                    [t('company.rating'), pct(nOf(c, 'rating_bps'))],
                    [t('company.total_shares'), n(nOf(c, 'total_shares'))],
                    [t('company.last_share_price'), <Money key="p" v={nOf(c, 'last_share_price')} />],
                    [t('company.founded'), <When key="f" iso={s(c, 'founded_at')} />],
                    [t('company.listed'), <When key="l" iso={s(c, 'listed_at')} />],
                    [t('company.closed'), c && c.closed_at ? <span key="c"><When iso={s(c, 'closed_at')} /> <Status value={s(c, 'close_reason')} /></span> : '—'],
                    [t('company.defence_licence'), <Status key="d" value={s(c, 'defence_licence')} />],
                  ]} />
                </Card>
                <Card title={t('company.activity')}>
                  <KV cols={2} rows={[
                    [t('company.openings'), n(nOf(k, 'openings'))],
                    [t('company.applications'), n(nOf(k, 'applications'))],
                    [t('company.designs'), n(nOf(k, 'designs'))],
                    [t('company.techs'), n(nOf(k, 'techs'))],
                    [t('company.orders_running'), n(nOf(k, 'orders_running'))],
                    [t('company.listings'), n(nOf(k, 'listings'))],
                    [t('company.pieces'), n(nOf(k, 'pieces'))],
                    [t('company.loans'), n(nOf(k, 'loans'))],
                  ]} />
                </Card>
              </Grid>
              <SeriesChart name="company.books" title={t('company.books_chart')} days={90} kind="line" scope={scope} />
            </>
          )}
        </Async>
      )}
      {tab === 'periods' && <DataView view="company.periods" scope={scope} hide={['company', 'city', 'ended_at', 'settled_at']} />}
      {tab === 'staff' && (
        <>
          <DataView view="employments" scope={scope} hide={['company', 'city']} defaultFilters={{ state: 'current' }} />
          <DataView view="npc.staff" scope={scope} hide={['company']} />
          <DataView view="recruit.campaigns" scope={scope} hide={['company']} />
          <DataView view="recruit.candidates" scope={scope} hide={['company']} />
          <DataView view="shift.sessions" scope={scope} hide={['company']} />
          <DataView view="shifts" scope={scope} hide={['company']} />
          <DataView view="company.openings" scope={scope} hide={['company']} />
          <DataView view="company.applications" scope={scope} hide={['company']} />
        </>
      )}
      {tab === 'production' && (
        <>
          <DataView view="company.designs" scope={scope} hide={['company']} />
          <DataView view="company.orders" scope={scope} hide={['company', 'consumed']} />
          <DataView view="company.research" scope={scope} hide={['company']} />
          <DataView view="company.techs" scope={scope} hide={['company']} />
          <DataView view="company.licences" scope={scope} />
          <DataView view="company.reverse" scope={scope} hide={['company']} />
        </>
      )}
      {tab === 'stock' && (
        <>
          <DataView view="warehouse" scope={scope} hide={['owner']} />
          <DataView view="pieces" scope={scope} hide={['owner']} />
          <DataView view="company.supplies" scope={scope} hide={['company']} />
          <DataView view="movements" scope={scope} />
        </>
      )}
      {tab === 'sales' && (
        <>
          <DataView view="company.listings" scope={scope} hide={['company']} />
          <DataView view="company.sales" scope={scope} hide={['company']} />
          <DataView view="procurements" scope={scope} hide={['company']} />
          <DataView view="hospital.treatments" scope={scope} hide={['company']} />
        </>
      )}
      {tab === 'shares' && (
        <>
          <SeriesChart name="stock.price" title={t('company.share_price')} days={90} kind="line" scope={scope} />
          <DataView view="stocks" scope={scope} hide={['company', 'name']} />
          <DataView view="shareholders" scope={scope} hide={['company']} />
          <DataView view="share.orders" scope={scope} hide={['company']} />
          <DataView view="share.trades" scope={scope} hide={['company']} />
          <DataView view="dividends" scope={scope} hide={['company']} />
          <DataView view="dividend.payments" scope={scope} hide={['company']} />
        </>
      )}
      {tab === 'finance' && (
        <>
          <DataView view="accounts" scope={scope} hide={['owner']} />
          <DataView view="loans" scope={scope} hide={['borrower', 'borrower_kind']} />
          <DataView view="ledger" scope={scope} hide={['owner', 'account']} pageSize={50} />
        </>
      )}
      {tab === 'licences' && <DataView view="defence.licences" scope={scope} hide={['company']} />}
      {tab === 'audit' && <DataView view="audit" scope={scope} />}
    </>
  );
}
