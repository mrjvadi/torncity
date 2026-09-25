import { useState } from 'react';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart } from '../components/Stat.tsx';
import { Grid, PageHeader, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['banks', 'finance.tab_banks'],
  ['loans', 'finance.tab_loans'],
  ['credit', 'finance.tab_credit'],
  ['insurance', 'finance.tab_insurance'],
  ['savings', 'finance.tab_savings'],
];

export function FinancePage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'banks';
  const [days, setDays] = useState(30);
  return (
    <>
      <PageHeader title={t('finance.title')} subtitle={t('finance.subtitle')} crumbs={[{ label: t('nav.finance') }]}
        actions={<RangePicker days={days} setDays={setDays} />} />
      <Tabs active={tab} hrefOf={(id) => href(['finance', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'banks' && (
        <>
          <DataView view="countries" title={t('finance.national_pools')} hide={['cities', 'readiness_bps', 'forces', 'wars']} />
          <DataView view="bank.fundings" />
          <DataView view="accounts" title={t('finance.pool_accounts')} defaultFilters={{ kind: 'national_bank' }} />
        </>
      )}
      {tab === 'loans' && (
        <>
          <Grid>
            <SeriesChart name="loans.opened" title={t('finance.loans_opened')} days={days} kind="bar" stacked />
            <SeriesChart name="credit.events" title={t('finance.credit_events')} days={days} kind="bar" stacked />
          </Grid>
          <DataView view="loans" defaultFilters={{ status: 'active' }} rowActions={(r) => <a className="btn small" href={href(['finance', 'loans'], { loan: String(r.no) })}>{t('finance.instalments')}</a>} />
          <DataView view="loans" title={t('finance.defaults')} defaultFilters={{ status: 'defaulted' }} />
          {route.query.get('loan') ? <DataView view="loan.periods" scope={{ loan: route.query.get('loan') ?? undefined }} /> : <p className="muted">{t('finance.pick_loan')}</p>}
        </>
      )}
      {tab === 'credit' && <DataView view="credit.events" pageSize={50} />}
      {tab === 'insurance' && (
        <>
          <DataView view="accounts" title={t('finance.insurance_funds')} defaultFilters={{ kind: 'insurance_fund' }} />
          <DataView view="insurance.policies" defaultFilters={{ status: 'active' }} />
          <DataView view="insurance.claims" />
        </>
      )}
      {tab === 'savings' && (
        <>
          <DataView view="savings" />
          <DataView view="savings.interest" />
        </>
      )}
    </>
  );
}
