import { useEffect, useId, useMemo, useState, type ReactNode } from 'react';
import { useI18n } from '../i18n/index.tsx';
import { download, q as query } from '../lib/api.ts';
import { useLiveReload } from '../lib/live.tsx';
import { loadPref, savePref } from '../lib/prefs.ts';
import { useLoad } from '../lib/useLoad.ts';
import type { ViewCol, ViewPage, ViewRow } from '../lib/types.ts';
import { href } from '../router.ts';
import { Icon } from './Icon.tsx';
import { useToast } from './Toast.tsx';
import { Code, CopyButton, Empty, ErrorBox, JsonView, Loading, Modal, Money, Ref, Status, useErrorText, When } from './ui.tsx';

// DataView shows one list of the server's catalogue (GET /api/views/{name})
// with its search, filters, time range, sorting, paging, column choice and
// CSV export. Scope narrows it to one thing's rows (a player's, a
// company's…); rowActions adds a cell of buttons to each row.

const RANGES: [string, number][] = [
  ['24h', 1],
  ['7d', 7],
  ['30d', 30],
  ['90d', 90],
];

function ymd(d: Date): string {
  return d.toISOString().slice(0, 10);
}

export function DataView({ view, scope, title, pageSize, hide, rowActions, live, defaultFilters, actions, empty }: {
  view: string;
  scope?: Record<string, string | undefined>;
  title?: ReactNode;
  pageSize?: number;
  hide?: string[];
  rowActions?: (row: ViewRow) => ReactNode;
  live?: string[];
  defaultFilters?: Record<string, string>;
  actions?: ReactNode;
  empty?: string;
}) {
  const { t, n, view: viewTitle, col, val } = useI18n();
  const toast = useToast();
  const errorText = useErrorText();
  const uid = useId();
  const [text, setText] = useState('');
  const [search, setSearch] = useState('');
  const [filters, setFilters] = useState<Record<string, string>>(defaultFilters ?? {});
  const [range, setRange] = useState<{ from?: string; to?: string; key: string }>({ key: 'all' });
  const [sort, setSort] = useState<{ key: string; dir: 'asc' | 'desc' } | null>(null);
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(() => Number(loadPref(`size.${view}`)) || pageSize || 25);
  const [openFilters, setOpenFilters] = useState(false);
  const [json, setJson] = useState<unknown>(undefined);
  const [hidden, setHidden] = useState<Set<string>>(() => {
    const saved = loadPref(`cols.${view}`);
    return new Set(saved ? (JSON.parse(saved) as string[]) : (hide ?? []));
  });

  useEffect(() => {
    const id = window.setTimeout(() => setSearch(text.trim()), 350);
    return () => window.clearTimeout(id);
  }, [text]);
  const scopeKey = JSON.stringify(scope ?? {});
  useEffect(() => setOffset(0), [search, filters, range, sort, scopeKey, limit]);

  const params = useMemo(() => {
    const p: Record<string, string | number | undefined> = { ...(scope ?? {}), q: search, limit, offset };
    for (const [k, v] of Object.entries(filters)) if (v) p[`f_${k}`] = v;
    if (range.from) p.from = range.from;
    if (range.to) p.to = range.to;
    if (sort) {
      p.sort = sort.key;
      p.dir = sort.dir;
    }
    return p;
  }, [scope, search, limit, offset, filters, range, sort]);
  const path = `/api/views/${encodeURIComponent(view)}${query(params)}`;
  const load = useLoad<ViewPage>(path);
  useLiveReload(live ?? [], load.reload);

  const page = load.data;
  const cols = useMemo(() => page?.columns ?? [], [page]);
  const shown = cols.filter((c) => !hidden.has(c.key));
  const rows: ViewRow[] = useMemo(
    () => (page?.rows ?? []).map((r) => Object.fromEntries(cols.map((c, i) => [c.key, r[i]]))),
    [page, cols],
  );
  const activeSort = sort ?? (page ? { key: page.order, dir: page.desc ? 'desc' : 'asc' } : null);

  const toggleCol = (key: string) => {
    setHidden((h) => {
      const next = new Set(h);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      savePref(`cols.${view}`, JSON.stringify([...next]));
      return next;
    });
  };
  const setSortOn = (c: ViewCol) => {
    if (!c.sort) return;
    setSort((s) => (s && s.key === c.key ? { key: c.key, dir: s.dir === 'desc' ? 'asc' : 'desc' } : { key: c.key, dir: 'desc' }));
  };
  const pickRange = (key: string, days: number) => {
    if (key === 'all') return setRange({ key });
    const from = new Date(Date.now() - days * 86400_000);
    setRange({ key, from: from.toISOString() });
  };
  const exportCSV = async () => {
    const p: Record<string, string | number | undefined> = { ...params, format: 'csv', limit: undefined, offset: undefined };
    try {
      await download(`/api/views/${encodeURIComponent(view)}${query(p)}`, `${view}.csv`);
    } catch (e) {
      toast('bad', errorText(e));
    }
  };
  const filterCount = Object.values(filters).filter(Boolean).length + (range.key !== 'all' ? 1 : 0);

  return (
    <section className="card dataview" aria-labelledby={`${uid}-title`}>
      <header className="card-head">
        <div>
          <h2 id={`${uid}-title`}>{title ?? viewTitle(view)}</h2>
          {page && (
            <p className="muted small">
              {rows.length === 0
                ? t('table.no_rows')
                : t('table.showing', { from: offset + 1, to: offset + rows.length }) + (page.more ? ` ${t('table.more')}` : '')}
            </p>
          )}
        </div>
        <div className="card-actions">
          {actions}
          <button type="button" className="icon-btn" onClick={load.reload} aria-label={t('common.refresh')} title={t('common.refresh')}>
            <Icon name="refresh" className={load.loading ? 'spin' : undefined} />
          </button>
          <details className="menu">
            <summary className="icon-btn" aria-label={t('table.columns')} title={t('table.columns')}>
              <Icon name="columns" />
            </summary>
            <div className="menu-body" role="group" aria-label={t('table.columns')}>
              {cols.map((c) => (
                <label key={c.key} className="check">
                  <input type="checkbox" checked={!hidden.has(c.key)} onChange={() => toggleCol(c.key)} />
                  {col(c.key)}
                </label>
              ))}
            </div>
          </details>
          <button type="button" className="icon-btn" onClick={() => void exportCSV()} aria-label={t('table.export')} title={t('table.export')}>
            <Icon name="download" />
          </button>
        </div>
      </header>
      {page && (page.search || page.filters.length > 0 || page.time) && (
        <div className="toolbar-row">
          {page.search && (
            <div className="search-box">
              <Icon name="search" size={16} />
              <label className="sr-only" htmlFor={`${uid}-q`}>
                {t('common.search')}
              </label>
              <input id={`${uid}-q`} type="search" value={text} dir="auto" placeholder={t('common.search')} onChange={(e) => setText(e.target.value)} />
            </div>
          )}
          {(page.filters.length > 0 || page.time) && (
            <button type="button" className={`btn small ${filterCount ? 'primary-soft' : ''}`} aria-expanded={openFilters} onClick={() => setOpenFilters((o) => !o)}>
              <Icon name="filter" size={16} /> {t('table.filters')}
              {filterCount > 0 && <span className="tab-count">{n(filterCount)}</span>}
            </button>
          )}
        </div>
      )}
      {page && openFilters && (
        <div className="filters">
          {page.filters.map((f) => (
            <div className="field" key={f.key}>
              <label className="field-label" htmlFor={`${uid}-f-${f.key}`}>
                {col(f.key)}
              </label>
              {f.options ? (
                <select id={`${uid}-f-${f.key}`} value={filters[f.key] ?? ''} onChange={(e) => setFilters((x) => ({ ...x, [f.key]: e.target.value }))}>
                  <option value="">{t('table.any')}</option>
                  {f.options.map((o) => (
                    <option key={o} value={o}>
                      {optionLabel(o)}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  id={`${uid}-f-${f.key}`}
                  dir="ltr"
                  value={filters[f.key] ?? ''}
                  placeholder={f.prefix ? t('table.starts_with') : t('table.equals')}
                  onChange={(e) => setFilters((x) => ({ ...x, [f.key]: e.target.value.trim() }))}
                />
              )}
            </div>
          ))}
          {page.time && (
            <div className="field range">
              <span className="field-label">{t('table.range')}</span>
              <div className="chips" role="group" aria-label={t('table.range')}>
                <button type="button" className="chip" aria-pressed={range.key === 'all'} onClick={() => pickRange('all', 0)}>
                  {t('table.all_time')}
                </button>
                {RANGES.map(([key, days]) => (
                  <button key={key} type="button" className="chip" aria-pressed={range.key === key} onClick={() => pickRange(key, days)}>
                    {t(`range.${key}` as 'range.7d')}
                  </button>
                ))}
              </div>
              <div className="dates">
                <label>
                  <span className="sr-only">{t('table.from')}</span>
                  <input type="date" value={range.from ? range.from.slice(0, 10) : ''} max={ymd(new Date())}
                    onChange={(e) => setRange((r) => ({ ...r, key: 'custom', from: e.target.value || undefined }))} aria-label={t('table.from')} />
                </label>
                <span aria-hidden="true">–</span>
                <label>
                  <span className="sr-only">{t('table.to')}</span>
                  <input type="date" value={range.to ? range.to.slice(0, 10) : ''}
                    onChange={(e) => setRange((r) => ({ ...r, key: 'custom', to: e.target.value || undefined }))} aria-label={t('table.to')} />
                </label>
              </div>
            </div>
          )}
          <button type="button" className="btn small" onClick={() => { setFilters({}); setRange({ key: 'all' }); }}>
            {t('table.clear')}
          </button>
        </div>
      )}
      {load.error ? (
        <ErrorBox error={load.error} retry={load.reload} />
      ) : !page ? (
        <Loading rows={4} />
      ) : rows.length === 0 ? (
        <Empty text={empty} />
      ) : (
        <div className={`table-wrap ${load.loading ? 'stale' : ''}`}>
          <table className="table">
            <thead>
              <tr>
                {shown.map((c) => {
                  const on = activeSort?.key === c.key;
                  const aria = on ? (activeSort?.dir === 'asc' ? 'ascending' : 'descending') : undefined;
                  return (
                    <th key={c.key} scope="col" className={isNum(c) ? 'num' : undefined} aria-sort={aria}>
                      {c.sort ? (
                        <button type="button" className="sort" onClick={() => setSortOn(c)}>
                          {col(c.key)}
                          <span className="sort-mark" aria-hidden="true">
                            {on ? (activeSort?.dir === 'asc' ? '▲' : '▼') : '↕'}
                          </span>
                        </button>
                      ) : (
                        col(c.key)
                      )}
                    </th>
                  );
                })}
                {rowActions && <th scope="col" className="sr-only-th">{t('table.actions')}</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>
                  {shown.map((c) => (
                    <td key={c.key} data-label={col(c.key)} className={isNum(c) ? 'num' : undefined}>
                      <Cell col={c} value={r[c.key]} onJson={setJson} />
                    </td>
                  ))}
                  {rowActions && <td className="row-actions">{rowActions(r)}</td>}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {page && (offset > 0 || page.more) && (
        <nav className="pager" aria-label={t('table.pages')}>
          <button type="button" className="btn small" disabled={offset === 0} onClick={() => setOffset(Math.max(offset - limit, 0))}>
            {t('table.prev')}
          </button>
          <span className="muted small">{t('table.page', { n: Math.floor(offset / limit) + 1 })}</span>
          <button type="button" className="btn small" disabled={!page.more} onClick={() => setOffset(offset + limit)}>
            {t('table.next')}
          </button>
          <label className="page-size">
            <span className="sr-only">{t('table.page_size')}</span>
            <select value={limit} onChange={(e) => { setLimit(Number(e.target.value)); savePref(`size.${view}`, e.target.value); }} aria-label={t('table.page_size')}>
              {[10, 25, 50, 100, 200].map((s) => (
                <option key={s} value={s}>
                  {n(s)}
                </option>
              ))}
            </select>
          </label>
        </nav>
      )}
      <Modal title={t('table.json')} open={json !== undefined} onClose={() => setJson(undefined)} wide>
        <JsonView value={json} />
      </Modal>
    </section>
  );

  function optionLabel(o: string): string {
    return val(o) ?? o;
  }
}

function isNum(c: ViewCol): boolean {
  return c.type === 'int' || c.type === 'money' || c.type === 'bps' || c.type === 'seconds';
}

const ID_LINKS: Record<string, (v: string) => string> = {
  tx: (v) => href(['economy', 'ledger'], { tx: v }),
  account: (v) => href(['economy', 'ledger'], { account: v }),
  action: (v) => href(['system', 'actions'], { action: v }),
  reference: (v) => href(['economy', 'ledger'], { reference: v }),
  target: (v) => href(['system', 'actions'], { reference: v }),
};

export function Cell({ col, value, onJson }: { col: ViewCol; value: unknown; onJson?: (v: unknown) => void }) {
  const { n, pct, dur, t } = useI18n();

  if (value === null || value === undefined || value === '') return <span className="muted">—</span>;
  switch (col.type) {
    case 'player':
    case 'company':
    case 'city':
    case 'country':
    case 'faction':
      return <Ref kind={col.type} code={String(value)} />;
    case 'ref': {
      const [kind = '', code = ''] = String(value).split(':', 2);
      return kind === 'system' ? <Status value={code} /> : <Ref kind={kind} code={code} />;
    }
    case 'money':
      return <Money v={Number(value)} />;
    case 'int':
      if (col.key === 'war') return <a href={href(['wars', String(value)])}>{n(Number(value))}</a>;
      return <>{n(Number(value))}</>;
    case 'bps':
      return <>{pct(Number(value))}</>;
    case 'seconds':
      return <>{dur(Number(value))}</>;
    case 'time':
      return <When iso={String(value)} />;
    case 'bool':
      return value ? <span className="yes">{t('common.yes')}</span> : <span className="muted">{t('common.no')}</span>;
    case 'status':
      return <Status value={String(value)} />;
    case 'list': {
      const list = Array.isArray(value) ? value : [value];
      return (
        <span className="chips-inline">
          {list.map((x, i) => (
            <Code key={i}>{typeof x === 'object' ? JSON.stringify(x) : String(x)}</Code>
          ))}
        </span>
      );
    }
    case 'json':
      return (
        <button type="button" className="btn tiny" onClick={() => onJson?.(value)}>
          {'{…}'}
        </button>
      );
    case 'id': {
      const s = String(value);
      const link = ID_LINKS[col.key];
      return (
        <span className="id-cell">
          {link ? (
            <a href={link(s)}>
              <Code>{s.slice(0, 8)}</Code>
            </a>
          ) : (
            <Code>{s.length > 12 ? s.slice(0, 8) : s}</Code>
          )}
          <CopyButton text={s} />
        </span>
      );
    }
    case 'code':
    case 'item':
      return <Code>{String(value)}</Code>;
    default:
      return <bdi>{String(value)}</bdi>;
  }
}
