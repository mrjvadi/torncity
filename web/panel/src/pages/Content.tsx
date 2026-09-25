import { useState } from 'react';
import { Action } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { Async, Badge, Card, Code, Grid, JsonView, KV, PageHeader, Table, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { ContentDiff, ContentStatus } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['status', 'content.tab_status'],
  ['diff', 'content.tab_diff'],
  ['browse', 'content.tab_browse'],
  ['versions', 'content.tab_versions'],
];

export function ContentPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'status';
  return (
    <>
      <PageHeader title={t('content.title')} subtitle={t('content.subtitle')} crumbs={[{ label: t('nav.content') }]} />
      <Tabs active={tab} hrefOf={(id) => href(['content', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'status' && <Status />}
      {tab === 'diff' && <Diff />}
      {tab === 'browse' && <Browse section={route.segments[2]} />}
      {tab === 'versions' && <DataView view="content.versions" />}
    </>
  );
}

function Status() {
  const { t, n } = useI18n();
  const load = useLoad<ContentStatus>('/api/content');
  return (
    <Async load={load}>
      {(c) => (
        <Grid>
          <Card title={t('content.version')}>
            {c.version === 0 ? (
              <p className="muted">{t('content.not_loaded')}</p>
            ) : (
              <KV rows={[
                [t('content.version'), n(c.version)],
                [t('content.loaded_at'), <When key="a" iso={c.loaded_at} />],
                [t('content.loaded_by'), <bdi key="b">{c.loaded_by}</bdi>],
                [t('content.reason'), <bdi key="r">{c.reason}</bdi>],
                [t('content.checksum'), <Code key="c">{c.checksum.slice(0, 16)}…</Code>],
              ]} />
            )}
            {c.counts && <KV cols={3} rows={Object.entries(c.counts).map(([k, v]): [string, string] => [k, n(v)])} />}
          </Card>
          <Card title={t('content.local_checksum')} actions={
            <>
              <button type="button" className="btn small" onClick={load.reload}>
                {t('content.validate')}
              </button>
              <Action small label={t('content.load')} danger disabled={!!c.local_error || !c.local_checksum || c.matches}
                lead={<>{t('content.load_lead')} <Code>{c.local_checksum}</Code></>}
                run={(reason, key) => post('/api/content/load', { confirm_checksum: c.local_checksum, reason }, key)}
                done={() => {
                  load.reload();
                  return t('toast.done');
                }} />
            </>
          }>
            {c.local_error ? (
              <p className="error">
                {t('content.local_error')}: <bdi dir="ltr">{c.local_error}</bdi>
              </p>
            ) : (
              <>
                <p className={c.matches ? 'ok-text' : 'warn-text'}>{c.matches ? t('content.matches') : t('content.differs')}</p>
                <KV rows={[[t('content.local_checksum'), <Code key="l">{c.local_checksum.slice(0, 16)}…</Code>]]} />
                {!c.matches && <a href={href(['content', 'diff'])}>{t('content.see_diff')}</a>}
              </>
            )}
            {(c.warnings ?? []).length > 0 && (
              <>
                <h3>{t('content.warnings')}</h3>
                <ul>
                  {(c.warnings ?? []).map((w) => (
                    <li key={w}>
                      <bdi dir="ltr">{w}</bdi>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </Card>
        </Grid>
      )}
    </Async>
  );
}

function Diff() {
  const { t, n } = useI18n();
  const load = useLoad<ContentDiff>('/api/content/diff');
  const [all, setAll] = useState(false);
  return (
    <Async load={load}>
      {(d) => {
        const rows = all ? d.sections : d.sections.filter((s) => s.added.length + s.removed.length + s.changed.length > 0);
        return (
          <Card title={t('content.diff_title', { n: d.version })} actions={
            <label className="check">
              <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} /> {t('content.show_same')}
            </label>
          }>
            {d.local_error && <p className="error"><bdi dir="ltr">{d.local_error}</bdi></p>}
            {rows.length === 0 ? (
              <p className="ok-text">{t('content.no_changes')}</p>
            ) : (
              <Table rows={rows} rowKey={(s) => s.name} cols={[
                { label: t('content.section'), cell: (s) => <a href={href(['content', 'browse', s.name])}><Code>{s.name}</Code></a> },
                { label: t('content.active_n'), cell: (s) => n(s.active), num: true },
                { label: t('content.local_n'), cell: (s) => n(s.local), num: true },
                { label: t('content.added'), cell: (s) => <Names list={s.added} tone="ok" /> },
                { label: t('content.removed'), cell: (s) => <Names list={s.removed} tone="bad" /> },
                { label: t('content.changed'), cell: (s) => <Names list={s.changed} tone="warn" /> },
              ]} />
            )}
          </Card>
        );
      }}
    </Async>
  );
}

function Names({ list, tone }: { list: string[]; tone: 'ok' | 'bad' | 'warn' }) {
  if (list.length === 0) return <span className="muted">—</span>;
  return (
    <span className="chips-inline">
      {list.slice(0, 12).map((x) => (
        <Badge key={x} tone={tone}>
          <bdi dir="ltr">{x || '∗'}</bdi>
        </Badge>
      ))}
      {list.length > 12 && <span className="muted">+{list.length - 12}</span>}
    </span>
  );
}

const SECTIONS = ['cities', 'routes', 'skills', 'levels', 'jurisdictions', 'levers', 'offices', 'careers', 'courses', 'transport_modes',
  'crime_tiers', 'venues', 'crime_categories', 'crimes', 'payment_services', 'components', 'archetypes', 'items', 'shops',
  'elections', 'company_types', 'company_markets', 'company_demand', 'method_profiles', 'technologies', 'suppliers'];

function Browse({ section }: { section?: string }) {
  const { t } = useI18n();
  const diff = useLoad<ContentDiff>('/api/content/diff');
  const names = diff.data ? diff.data.sections.map((s) => s.name) : SECTIONS;
  const load = useLoad<unknown>(section ? `/api/content/sections/${encodeURIComponent(section)}` : null);
  const [text, setText] = useState('');
  return (
    <div className="browse">
      <nav className="section-list" aria-label={t('content.sections')}>
        {names.map((s) => (
          <a key={s} href={href(['content', 'browse', s])} aria-current={s === section ? 'page' : undefined}>
            <Code>{s}</Code>
          </a>
        ))}
      </nav>
      <div className="section-body">
        {!section ? (
          <p className="muted">{t('content.pick_section')}</p>
        ) : (
          <Card title={<Code>{section}</Code>} actions={
            <input type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder={t('common.search')} dir="ltr" aria-label={t('common.search')} />
          }>
            <Async load={load}>{(v) => <JsonView value={filterJSON(v, text)} />}</Async>
          </Card>
        )}
      </div>
    </div>
  );
}

// filterJSON keeps the entries of a list whose JSON mentions the text.
function filterJSON(v: unknown, text: string): unknown {
  const s = text.trim().toLowerCase();
  if (!s || !Array.isArray(v)) return v;
  return v.filter((x) => JSON.stringify(x).toLowerCase().includes(s));
}
