import { DataView } from '../components/DataView.tsx';
import { SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Card, Code, Grid, KV, Money, PageHeader, Ref, Status, Table, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import type { Rec } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';
import { PolicyView } from './Governance.tsx';

const s = (r: Rec | undefined, k: string) => (r && r[k] !== null && r[k] !== undefined ? String(r[k]) : '');
const nOf = (r: Rec | undefined, k: string) => (r && typeof r[k] === 'number' ? (r[k] as number) : null);

export function CountriesPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const code = route.segments[1];
  if (code) return <Country code={code.toLowerCase()} tab={route.segments[2] ?? 'summary'} />;
  return (
    <>
      <PageHeader title={t('countries.title')} subtitle={t('countries.subtitle')} crumbs={[{ label: t('nav.countries') }]} />
      <DataView view="countries" />
      <DataView view="forces" pageSize={50} defaultFilters={{ status: 'stationed' }} />
      <DataView view="procurements" />
    </>
  );
}

const TABS: [string, Key][] = [
  ['summary', 'country.tab_summary'],
  ['forces', 'country.tab_forces'],
  ['war', 'country.tab_war'],
  ['diplomacy', 'country.tab_diplomacy'],
  ['finance', 'country.tab_finance'],
  ['governance', 'country.tab_governance'],
  ['cities', 'country.tab_cities'],
];

function Country({ code, tab }: { code: string; tab: string }) {
  const { t, n, pct } = useI18n();
  const load = useLoad<{ country: Rec; branches: Rec[] }>(`/api/dossier/countries/${encodeURIComponent(code)}`);
  const c = load.data?.country;
  const scope = { country: code };
  return (
    <>
      <PageHeader crumbs={[{ label: t('nav.countries'), to: '#/countries' }, { label: <Code>{code}</Code> }]}
        title={c ? <bdi>{s(c, 'name')}</bdi> : <Code>{code}</Code>} subtitle={<Code>{code}</Code>} />
      <Tabs active={tab} hrefOf={(id) => href(['countries', code, id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'summary' && (
        <Async load={load} rows={4}>
          {(d) => (
            <>
              <StatGrid>
                <Stat label={t('country.treasury')} value={<Money v={nOf(c, 'treasury')} compact />} />
                <Stat label={t('country.defence_fund')} value={<Money v={nOf(c, 'defence_fund')} compact />} />
                <Stat label={t('country.national_bank')} value={<Money v={nOf(c, 'national_bank')} compact />} />
                <Stat label={t('country.insurance_fund')} value={<Money v={nOf(c, 'insurance_fund')} compact />} />
                <Stat label={t('country.readiness')} value={pct(nOf(c, 'readiness_bps'))} />
                <Stat label={t('country.wars_open')} value={n(nOf(c, 'wars_open'))} tone={(nOf(c, 'wars_open') ?? 0) > 0 ? 'warn' : undefined} />
              </StatGrid>
              <Grid>
                <Card title={t('country.facts')}>
                  <KV rows={[
                    [t('country.cities'), n(nOf(c, 'cities'))],
                    [t('country.loans_active'), n(nOf(c, 'loans_active'))],
                    [t('country.loans_outstanding'), <Money key="o" v={nOf(c, 'loans_outstanding')} />],
                    [t('country.loans_defaulted'), n(nOf(c, 'loans_defaulted'))],
                    [t('country.policies_active'), n(nOf(c, 'policies_active'))],
                    [t('country.sanctions'), n(nOf(c, 'sanctions'))],
                    [t('country.treaties'), n(nOf(c, 'treaties'))],
                    [t('country.next_military_period'), <When key="m" iso={s(c, 'next_military_period_at')} rel />],
                  ]} />
                </Card>
                <Card title={t('country.branches')}>
                  <Table rows={d.branches} rowKey={(r) => `${s(r, 'branch')}:${s(r, 'status')}`} cols={[
                    { label: t('country.branch'), cell: (r) => <Code>{s(r, 'branch')}</Code> },
                    { label: t('players.status'), cell: (r) => <Status value={s(r, 'status')} /> },
                    { label: t('country.pieces'), cell: (r) => n(nOf(r, 'pieces')), num: true },
                  ]} />
                </Card>
              </Grid>
              <SeriesChart name="military.readiness" title={t('country.readiness_chart')} days={90} kind="line" scope={scope} />
            </>
          )}
        </Async>
      )}
      {tab === 'forces' && (
        <>
          <DataView view="forces" scope={scope} hide={['country']} pageSize={50} />
          <DataView view="military.periods" scope={scope} hide={['country', 'ended_at']} />
          <DataView view="military.moves" scope={scope} hide={['country']} />
          <DataView view="procurements" scope={scope} hide={['country']} />
          <DataView view="warehouse" scope={scope} hide={['owner']} />
        </>
      )}
      {tab === 'war' && (
        <>
          <DataView view="wars" scope={scope} />
          <DataView view="war.operations" scope={scope} hide={['report']} />
          <DataView view="war.proposals" scope={scope} />
          <DataView view="war.events" scope={scope} />
          <DataView view="city.control" scope={scope} />
        </>
      )}
      {tab === 'diplomacy' && (
        <>
          <DataView view="sanctions" scope={scope} />
          <DataView view="treaties" scope={scope} />
          <DataView view="diplomacy" scope={scope} />
          <DataView view="tariffs" scope={scope} />
        </>
      )}
      {tab === 'finance' && (
        <>
          <DataView view="accounts" scope={scope} hide={['owner']} />
          <DataView view="bank.fundings" scope={scope} hide={['country']} />
          <DataView view="loans" scope={scope} hide={['country']} />
          <DataView view="insurance.policies" scope={scope} hide={['country']} />
          <DataView view="insurance.claims" scope={scope} />
          <DataView view="savings.interest" scope={scope} hide={['country']} />
          <DataView view="ledger" scope={scope} hide={['account']} />
        </>
      )}
      {tab === 'governance' && (
        <>
          <PolicyView kind="country" code={code} />
          <DataView view="seats" scope={scope} />
          <DataView view="proposals" scope={scope} hide={['body', 'value_json']} />
          <DataView view="elections" scope={scope} />
          <DataView view="policy.changes" scope={scope} />
        </>
      )}
      {tab === 'cities' && <DataView view="cities" scope={scope} hide={['country']} />}
    </>
  );
}

export function WarsPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const no = route.segments[1];
  if (no) return <War no={no} />;
  return (
    <>
      <PageHeader title={t('wars.title')} subtitle={t('wars.subtitle')} crumbs={[{ label: t('nav.wars') }]} />
      <DataView view="wars" rowActions={(r) => <a className="btn small" href={href(['wars', String(r.no)])}>{t('common.open')}</a>} />
      <DataView view="war.operations" hide={['report']} />
      <DataView view="sanctions" />
      <DataView view="treaties" />
    </>
  );
}

function War({ no }: { no: string }) {
  const { t, n } = useI18n();
  const load = useLoad<{ war: Rec }>(`/api/dossier/wars/${encodeURIComponent(no)}`);
  const w = load.data?.war;
  const scope = { war: no };
  return (
    <>
      <PageHeader crumbs={[{ label: t('nav.wars'), to: '#/wars' }, { label: `#${no}` }]} title={t('wars.war_no', { n: Number(no) })}
        badge={w && <Status value={s(w, 'status')} />}
        subtitle={w && (
          <span className="meta-line">
            <Ref kind="country" code={s(w, 'attacker')} /> ⚔ <Ref kind="country" code={s(w, 'defender')} /> · <Code>{s(w, 'ground')}</Code>
          </span>
        )} />
      <Async load={load}>
        {() => (
          <StatGrid>
            <Stat label={t('wars.operations')} value={n(nOf(w, 'operations'))} />
            <Stat label={t('wars.attacker_lost')} value={n(nOf(w, 'attacker_lost'))} />
            <Stat label={t('wars.defender_lost')} value={n(nOf(w, 'defender_lost'))} />
            <Stat label={t('wars.captures')} value={n(nOf(w, 'captures'))} />
            <Stat label={t('wars.declared')} value={<When iso={s(w, 'declared_at')} rel />} hint={s(w, 'declared_by')} />
          </StatGrid>
        )}
      </Async>
      <DataView view="war.parties" scope={scope} hide={['war']} />
      <DataView view="war.operations" scope={scope} hide={['war']} />
      <DataView view="war.proposals" scope={scope} hide={['war']} />
      <DataView view="war.events" scope={scope} hide={['war']} />
    </>
  );
}
