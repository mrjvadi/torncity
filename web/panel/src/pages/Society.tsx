import { useState } from 'react';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Code, Money, PageHeader, Ref, Status, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import type { Rec } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['missions', 'society.tab_missions'],
  ['achievements', 'society.tab_achievements'],
  ['factions', 'society.tab_factions'],
  ['leaderboards', 'society.tab_leaderboards'],
];

export function SocietyPage({ route }: { route: Route }) {
  const { t } = useI18n();
  if (route.segments[0] === 'factions' && route.segments[1]) return <Faction code={route.segments[1].toUpperCase()} />;
  const tab = route.segments[0] === 'factions' ? 'factions' : (route.segments[1] ?? 'missions');
  return <Society tab={tab} t={t} />;
}

function Society({ tab, t }: { tab: string; t: ReturnType<typeof useI18n>['t'] }) {
  const [days, setDays] = useState(30);
  return (
    <>
      <PageHeader title={t('society.title')} subtitle={t('society.subtitle')} crumbs={[{ label: t('nav.society') }]}
        actions={tab === 'missions' || tab === 'achievements' ? <RangePicker days={days} setDays={setDays} /> : undefined} />
      <Tabs active={tab} hrefOf={(id) => href(['society', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'missions' && (
        <>
          <SeriesChart name="missions.ended" title={t('society.missions_ended')} days={days} kind="bar" stacked />
          <DataView view="mission.stats" />
          <DataView view="missions" />
        </>
      )}
      {tab === 'achievements' && (
        <>
          <SeriesChart name="achievements.awarded" title={t('society.achievements_awarded')} days={days} kind="bar" />
          <DataView view="achievement.stats" />
          <DataView view="achievements" />
        </>
      )}
      {tab === 'factions' && (
        <>
          <DataView view="factions" defaultFilters={{ status: 'active' }} />
          <DataView view="faction.operations" />
          <DataView view="faction.requests" defaultFilters={{ status: 'pending' }} />
        </>
      )}
      {tab === 'leaderboards' && <DataView view="leaderboards" pageSize={50} />}
    </>
  );
}

function Faction({ code }: { code: string }) {
  const { t, n } = useI18n();
  const load = useLoad<{ faction: Rec }>(`/api/dossier/factions/${encodeURIComponent(code)}`);
  const f = load.data?.faction;
  const v = (k: string) => (f && f[k] !== null && f[k] !== undefined ? String(f[k]) : '');
  const scope = { faction: code };
  return (
    <>
      <PageHeader crumbs={[{ label: t('nav.society'), to: '#/society/factions' }, { label: <Code>{code}</Code> }]}
        title={f ? <bdi>{v('name')}</bdi> : <Code>{code}</Code>} badge={f && <Status value={v('status')} />}
        subtitle={f && (
          <span className="meta-line">
            <Code>{code}</Code> <Ref kind="city" code={v('city')} /> <span>{t('society.leader')}: <Ref kind="player" code={v('leader')} /></span>
          </span>
        )} />
      <Async load={load}>
        {() => (
          <StatGrid>
            <Stat label={t('society.members')} value={n(Number(f?.members ?? 0))} />
            <Stat label={t('society.treasury')} value={<Money v={Number(f?.treasury ?? 0)} compact />} />
            <Stat label={t('society.pending')} value={n(Number(f?.pending ?? 0))} />
            <Stat label={t('society.founded')} value={<When iso={v('founded_at')} rel />} />
          </StatGrid>
        )}
      </Async>
      <DataView view="faction.members" scope={scope} hide={['faction']} />
      <DataView view="faction.requests" scope={scope} hide={['faction']} />
      <DataView view="faction.operations" scope={scope} hide={['faction']} />
      <DataView view="faction.crew" scope={scope} hide={['faction']} />
      <DataView view="ledger" scope={scope} hide={['owner', 'account']} />
    </>
  );
}
