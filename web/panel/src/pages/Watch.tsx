import { useState } from 'react';
import { Action, Field, TextArea } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { Card, Code, Grid, Money, PageHeader, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { ViewRow } from '../lib/types.ts';
import { href, currentRoute, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['flags', 'watch.flags'],
  ['holds', 'watch.holds'],
  ['moderation', 'watch.moderation'],
];

export function WatchPage({ route }: { route: Route }) {
  const { t, n } = useI18n();
  const tab = route.segments[1] ?? 'flags';
  const [tick, setTick] = useState(0);
  const done = () => {
    setTick((x) => x + 1);
    return t('toast.done');
  };
  const settle = (h: ViewRow, release: boolean) => (
    <Action small label={release ? t('watch.release') : t('watch.return')} danger={!release}
      run={(reason, key) => post(`/api/watch/holds/${String(h.no)}/${release ? 'release' : 'return'}`, { reason }, key)} done={done}>
      <p>
        #{n(Number(h.no))} · <Code>{String(h.payer)}</Code> → <Code>{String(h.payee)}</Code> · <Money v={Number(h.amount)} />
      </p>
    </Action>
  );
  return (
    <>
      <PageHeader title={t('watch.title')} subtitle={t('watch.subtitle')} crumbs={[{ label: t('nav.watch') }]} />
      <Tabs active={tab} hrefOf={(id) => href(['watch', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'flags' && (
        <DataView key={tick} view="flags" defaultFilters={{ status: 'open' }} live={['flag']}
          rowActions={(f) =>
            f.status === 'open' ? (
              <Action small label={t('watch.clear')} run={(reason, key) => post(`/api/watch/flags/${String(f.no)}/clear`, { reason }, key)} done={done}>
                <p>
                  #{n(Number(f.no))} · <Code>{String(f.rule)}</Code> · <Code>{String(f.player)}</Code>
                </p>
              </Action>
            ) : null
          } />
      )}
      {tab === 'holds' && (
        <DataView key={tick} view="holds" defaultFilters={{ status: 'held' }} live={['hold']}
          rowActions={(h) =>
            h.status === 'held' ? (
              <div className="inline">
                {settle(h, true)}
                {settle(h, false)}
              </div>
            ) : null
          } />
      )}
      {tab === 'moderation' && <DataView view="moderation" defaultFilters={{ state: 'standing' }} live={['change']} />}
    </>
  );
}

const MAX_MESSAGE = 3000;

export function MessagesPage() {
  const { t } = useI18n();
  const [text, setText] = useState('');
  const [textEN, setTextEN] = useState('');
  const [btext, setBText] = useState('');
  const [btextEN, setBTextEN] = useState('');
  const [only, setOnly] = useState(() => currentRoute().query.get('only') ?? '');
  const tooLong = (a: string, b: string) => [...a].length > MAX_MESSAGE || [...b].length > MAX_MESSAGE;
  const check = (a: string, b: string) => (!a.trim() ? t('common.invalid') : tooLong(a, b) ? t('messages.too_long') : null);
  const Counter = ({ v }: { v: string }) => (
    <span className={`counter ${[...v].length > MAX_MESSAGE ? 'over' : ''}`}>
      {t('messages.count', { n: [...v].length, max: MAX_MESSAGE })}
    </span>
  );
  return (
    <>
      <PageHeader title={t('messages.title')} subtitle={t('messages.subtitle')} crumbs={[{ label: t('nav.messages') }]} />
      <Grid>
        <Card title={t('messages.announce')} subtitle={t('messages.announce_lead')}>
          <TextArea label={t('messages.text')} value={text} onChange={setText} dir="rtl" />
          <Counter v={text} />
          <TextArea label={t('messages.text_en')} value={textEN} onChange={setTextEN} dir="ltr" />
          <Preview fa={text} en={textEN} />
          <Action<{ cities: number }> label={t('messages.send')} danger icon="message" check={() => check(text, textEN)}
            run={(reason, key) => post('/api/announce', { text: text.trim(), text_en: textEN.trim(), reason }, key)}
            done={(r) => t('messages.sent_groups', { n: r.cities })}>
            <Preview fa={text} en={textEN} />
          </Action>
        </Card>
        <Card title={t('messages.broadcast')} subtitle={t('messages.broadcast_lead')}>
          <TextArea label={t('messages.text')} value={btext} onChange={setBText} dir="rtl" />
          <Counter v={btext} />
          <TextArea label={t('messages.text_en')} value={btextEN} onChange={setBTextEN} dir="ltr" />
          <Field label={t('messages.only')} value={only} onChange={setOnly} dir="ltr" hint={t('messages.only_hint')} />
          <Preview fa={btext} en={btextEN} />
          <Action<{ players: number }> label={only.trim() ? t('messages.send_preview') : t('messages.send_all')} danger={!only.trim()} icon="message"
            check={() => check(btext, btextEN)}
            run={(reason, key) => post('/api/broadcast', { text: btext.trim(), text_en: btextEN.trim(), only: only.trim(), reason }, key)}
            done={(r) => t('messages.sent_players', { n: r.players })}>
            <Preview fa={btext} en={btextEN} />
            {only.trim() && (
              <p>
                {t('messages.only')}: <Code>{only.trim()}</Code>
              </p>
            )}
          </Action>
        </Card>
      </Grid>
      <DataView view="audit" title={t('messages.history')} defaultFilters={{ action: 'admin.' }} hide={['target_type', 'target', 'old_value', 'id']} live={['audit']} />
    </>
  );
}

// Preview shows a message the way a player's chat bubble would.
function Preview({ fa, en }: { fa: string; en: string }) {
  const { t } = useI18n();
  if (!fa.trim() && !en.trim()) return null;
  return (
    <div className="preview-pane" aria-label={t('messages.preview')}>
      {fa.trim() && (
        <div className="bubble" dir="rtl" lang="fa">
          {fa}
        </div>
      )}
      {en.trim() && (
        <div className="bubble" dir="ltr" lang="en">
          {en}
        </div>
      )}
    </div>
  );
}

export function AuditPage() {
  const { t } = useI18n();
  return (
    <>
      <PageHeader title={t('audit.title')} subtitle={t('audit.subtitle')} crumbs={[{ label: t('nav.audit') }]} />
      <DataView view="audit" pageSize={50} live={['audit']} hide={['target', 'old_value']} />
    </>
  );
}
