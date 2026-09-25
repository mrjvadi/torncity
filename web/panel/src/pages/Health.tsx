import { useState } from 'react';
import { Action } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart } from '../components/Stat.tsx';
import { PageHeader } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { Route } from '../router.ts';

export function HealthPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const [days, setDays] = useState(30);
  void route;
  return (
    <>
      <PageHeader title={t('health.title')} subtitle={t('health.subtitle')} crumbs={[{ label: t('nav.health') }]}
        actions={<RangePicker days={days} setDays={setDays} />} />
      <SeriesChart name="hospital.admissions" title={t('health.admissions')} days={days} kind="bar" stacked />
      <DataView view="hospital.stays" defaultFilters={{ status: 'admitted' }} live={['change']}
        rowActions={(r) =>
          r.status === 'admitted' ? (
            <Action small label={t('player.discharge')} lead={t('player.discharge_lead')}
              run={(reason, key) => post(`/api/players/${encodeURIComponent(String(r.player))}/discharge`, { reason }, key)}
              done={() => t('toast.done')} />
          ) : null
        } />
      <DataView view="clinics" />
      <DataView view="hospital.treatments" />
    </>
  );
}
