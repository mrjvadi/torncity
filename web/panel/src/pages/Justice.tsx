import { useState } from 'react';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart } from '../components/Stat.tsx';
import { Grid, PageHeader, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import { Action } from '../components/Action.tsx';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['stats', 'justice.tab_stats'],
  ['crimes', 'justice.tab_crimes'],
  ['jail', 'justice.tab_jail'],
  ['cases', 'justice.tab_cases'],
  ['criminals', 'justice.tab_criminals'],
];

export function JusticePage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'stats';
  const [days, setDays] = useState(30);
  return (
    <>
      <PageHeader title={t('justice.title')} subtitle={t('justice.subtitle')} crumbs={[{ label: t('nav.justice') }]}
        actions={<RangePicker days={days} setDays={setDays} />} />
      <Tabs active={tab} hrefOf={(id) => href(['justice', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'stats' && (
        <>
          <Grid>
            <SeriesChart name="crimes.outcomes" title={t('justice.outcomes')} days={days} kind="bar" stacked />
            <SeriesChart name="crimes.categories" title={t('justice.categories')} days={days} kind="bar" stacked />
          </Grid>
          <SeriesChart name="jail.sentences" title={t('justice.sentences')} days={days} kind="bar" stacked />
          <DataView view="crime.stats" pageSize={50} />
        </>
      )}
      {tab === 'crimes' && <DataView view="crimes" pageSize={50} />}
      {tab === 'jail' && (
        <DataView view="jail" defaultFilters={{ status: 'serving' }} live={['change']}
          rowActions={(r) =>
            r.status === 'serving' ? (
              <Action small label={t('player.release')} lead={t('player.release_lead')}
                run={(reason, key) => post(`/api/players/${encodeURIComponent(String(r.player))}/release`, { reason }, key)}
                done={() => t('toast.done')} />
            ) : null
          } />
      )}
      {tab === 'cases' && <DataView view="crime.reports" />}
      {tab === 'criminals' && <DataView view="criminals" pageSize={50} />}
    </>
  );
}
