import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useI18n, type Key } from '../i18n/index.tsx';
import { ApiError } from '../lib/api.ts';
import { href } from '../router.ts';
import { Icon } from './Icon.tsx';

export function Card({ title, actions, children, className, subtitle }: {
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`card ${className ?? ''}`}>
      {(title || actions) && (
        <header className="card-head">
          <div>
            {title && <h2>{title}</h2>}
            {subtitle && <p className="muted small">{subtitle}</p>}
          </div>
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

export interface Crumb {
  label: ReactNode;
  to?: string;
}

// PageHeader is a page's breadcrumbs, title and actions.
export function PageHeader({ title, subtitle, crumbs, actions, badge }: {
  title: ReactNode;
  subtitle?: ReactNode;
  crumbs?: Crumb[];
  actions?: ReactNode;
  badge?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="page-head">
      {crumbs && crumbs.length > 0 && (
        <nav aria-label={t('nav.breadcrumbs')} className="crumbs">
          <ol>
            {crumbs.map((c, i) => (
              <li key={i}>
                {c.to ? <a href={c.to}>{c.label}</a> : <span aria-current="page">{c.label}</span>}
              </li>
            ))}
          </ol>
        </nav>
      )}
      <div className="page-title">
        <div className="page-title-text">
          <h1>
            {title} {badge}
          </h1>
          {subtitle && <p className="muted">{subtitle}</p>}
        </div>
        {actions && <div className="page-actions">{actions}</div>}
      </div>
    </div>
  );
}

// PageTitle is kept for the simpler pages.
export function PageTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return <PageHeader title={children} actions={actions} />;
}

// KV is a list of labelled values.
export function KV({ rows, cols }: { rows: [string, ReactNode][]; cols?: 1 | 2 | 3 }) {
  return (
    <dl className={`kv kv-${cols ?? 1}`}>
      {rows.map(([k, v]) => (
        <div key={k} className="kv-row">
          <dt>{k}</dt>
          <dd>{v === '' || v === null || v === undefined ? '—' : v}</dd>
        </div>
      ))}
    </dl>
  );
}

export interface Column<T> {
  label: string;
  cell: (row: T) => ReactNode;
  num?: boolean;
}

// Table is a simple fixed table; lists from the server use DataView.
export function Table<T>({ rows, cols, rowKey, onRow }: {
  rows: T[];
  cols: Column<T>[];
  rowKey: (row: T) => string;
  onRow?: (row: T) => void;
}) {
  if (rows.length === 0) return <Empty />;
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            {cols.map((c) => (
              <th key={c.label} className={c.num ? 'num' : undefined} scope="col">
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr
              key={rowKey(r)}
              className={onRow ? 'clickable' : undefined}
              onClick={onRow ? () => onRow(r) : undefined}
              onKeyDown={onRow ? (e) => (e.key === 'Enter' ? onRow(r) : undefined) : undefined}
              tabIndex={onRow ? 0 : undefined}
            >
              {cols.map((c) => (
                <td key={c.label} data-label={c.label} className={c.num ? 'num' : undefined}>
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Empty({ text }: { text?: string }) {
  const { t } = useI18n();
  return (
    <div className="empty">
      <Icon name="box" size={28} />
      <p>{text ?? t('common.none')}</p>
    </div>
  );
}

export function Loading({ rows }: { rows?: number }) {
  const { t } = useI18n();
  return (
    <div role="status" aria-live="polite" className="loading">
      <span className="sr-only">{t('common.loading')}</span>
      {Array.from({ length: rows ?? 3 }, (_, i) => (
        <div key={i} className="skeleton" />
      ))}
    </div>
  );
}

// errorText is what an operator reads of a failed request.
export function useErrorText(): (e: unknown) => string {
  const { t } = useI18n();
  return (e) => {
    if (e instanceof ApiError) {
      const key = `err.${e.code}` as Key;
      const base = t(key) === key ? t('err.internal') : t(key);
      return e.detail ? `${base} ${e.detail}` : base;
    }
    return t('err.internal');
  };
}

export function ErrorBox({ error, retry }: { error: unknown; retry?: () => void }) {
  const { t } = useI18n();
  const text = useErrorText();
  return (
    <div className="error" role="alert">
      <Icon name="alert" />
      <span>{text(error)}</span>
      {retry && (
        <button type="button" className="btn small" onClick={retry}>
          {t('common.retry')}
        </button>
      )}
    </div>
  );
}

// Async renders a load's three states.
export function Async<T>({ load, children, rows }: {
  load: { data: T | null; error: unknown; loading: boolean; reload: () => void };
  children: (data: T) => ReactNode;
  rows?: number;
}) {
  if (load.error) return <ErrorBox error={load.error} retry={load.reload} />;
  if (load.data === null) return <Loading rows={rows} />;
  return <>{children(load.data)}</>;
}

export type Tone = 'ok' | 'bad' | 'warn' | 'info' | 'neutral';

export function Badge({ tone, children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={`badge ${tone ?? ''}`} title={title}>
      {children}
    </span>
  );
}

const TONES: Record<string, Tone> = {
  active: 'ok', open: 'info', succeeded: 'ok', completed: 'ok', done: 'ok', filled: 'ok', sold: 'ok', solved: 'ok',
  repaid: 'ok', accepted: 'ok', arrived: 'ok', passed: 'ok', elected: 'ok', held: 'warn', released: 'ok', owned: 'ok',
  stationed: 'ok', ready: 'ok', final: 'ok', published: 'ok', current: 'ok', ok: 'ok', counted: 'ok', peace: 'ok', lifted: 'neutral',
  pending: 'warn', scheduled: 'info', running: 'info', in_progress: 'info', in_transit: 'info', moving: 'info', working: 'info',
  investigating: 'info', serving: 'warn', admitted: 'warn', gathering: 'info', launched: 'warn', proposed: 'info',
  revoking: 'warn', draft: 'neutral', standing: 'bad', ceasefire: 'warn', declared: 'bad', lagging: 'warn', degraded: 'bad', down: 'bad',
  failed: 'bad', caught: 'bad', defaulted: 'bad', banned: 'bad', ban: 'bad', mute: 'warn', dissolved: 'bad', rejected: 'bad',
  declined: 'bad', revoked: 'bad', destroyed: 'bad', damaged: 'warn', repossessed: 'bad', expired: 'neutral', cancelled: 'neutral',
  withdrawn: 'neutral', abandoned: 'neutral', unsold: 'neutral', unsolved: 'neutral', ended: 'neutral', lapsed: 'neutral',
  escaped: 'warn', terminated: 'bad', called_off: 'neutral', deleted: 'bad', vacant: 'warn', cleared: 'neutral', returned: 'neutral',
  insolvent: 'bad', missed: 'bad', on_time: 'ok', default: 'bad',
  on: 'ok', off: 'bad', groups_off: 'warn',
};

// Status is a status-like value as a badge, in words where there are some.
export function Status({ value }: { value: string | null | undefined }) {
  const { val } = useI18n();
  if (value === null || value === undefined || value === '') return <span className="muted">—</span>;
  const words = val(value);
  return (
    <Badge tone={TONES[value] ?? 'neutral'} title={value}>
      {words ?? <bdi dir="ltr">{value}</bdi>}
    </Badge>
  );
}

// Code is an identifier: always left-to-right, even inside Persian text.
export function Code({ children }: { children: ReactNode }) {
  return (
    <bdi className="code" dir="ltr">
      {children}
    </bdi>
  );
}

export function Money({ v, compact }: { v: number | null | undefined; compact?: boolean }) {
  const { n, nc } = useI18n();
  const neg = v !== null && v !== undefined && v < 0;
  return (
    <span className={`money ${neg ? 'neg' : ''}`} title={compact && v !== null && v !== undefined ? n(v) : undefined}>
      {compact ? nc(v) : n(v)}
    </span>
  );
}

export function Link({ to, children }: { to: string; children: ReactNode }) {
  return <a href={`#${to}`}>{children}</a>;
}

// Ref links a thing by kind and code: a player, company, city, country or
// faction.
const REF_ROUTE: Record<string, string> = {
  player: 'players', company: 'companies', city: 'cities', country: 'countries', faction: 'factions', war: 'wars',
};

export function Ref({ kind, code, label }: { kind: string; code: string | null | undefined; label?: ReactNode }) {
  if (!code) return <span className="muted">—</span>;
  const route = REF_ROUTE[kind];
  if (!route) return <Code>{code}</Code>;
  return (
    <a className="ref" href={href([route, code])}>
      <Code>{label ?? code}</Code>
    </a>
  );
}

// When is an instant: the date, and how long ago in its title.
export function When({ iso, rel }: { iso: string | null | undefined; rel?: boolean }) {
  const { at, ago } = useI18n();
  if (!iso) return <span className="muted">—</span>;
  return (
    <time dateTime={iso} title={rel ? at(iso) : ago(iso)}>
      {rel ? ago(iso) : at(iso)}
    </time>
  );
}

export function CopyButton({ text }: { text: string }) {
  const { t } = useI18n();
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={t('common.copy')}
      title={t('common.copy')}
      onClick={(e) => {
        e.stopPropagation();
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          window.setTimeout(() => setDone(false), 1200);
        });
      }}
    >
      <Icon name={done ? 'check' : 'copy'} size={14} />
    </button>
  );
}

export interface TabDef {
  id: string;
  label: ReactNode;
  count?: number | null;
}

// Tabs is a tab list; the panel shown is the caller's (a route segment).
export function Tabs({ tabs, active, hrefOf }: { tabs: TabDef[]; active: string; hrefOf: (id: string) => string }) {
  const { n } = useI18n();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest', inline: 'center' });
  }, [active]);
  return (
    <div className="tabs" role="tablist" ref={ref}>
      {tabs.map((tab) => (
        <a key={tab.id} role="tab" aria-selected={tab.id === active} href={hrefOf(tab.id)} className="tab">
          {tab.label}
          {tab.count !== undefined && tab.count !== null && tab.count > 0 && <span className="tab-count">{n(tab.count)}</span>}
        </a>
      ))}
    </div>
  );
}

// Modal is a dialog with a title and a close button.
export function Modal({ title, open, onClose, children, wide }: {
  title: ReactNode;
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  const { t } = useI18n();
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog ref={ref} className={`dialog ${wide ? 'wide' : ''}`} onClose={onClose}>
      {open && (
        <>
          <div className="dialog-head">
            <h2>{title}</h2>
            <button type="button" className="icon-btn" onClick={onClose} aria-label={t('common.close')}>
              <Icon name="x" />
            </button>
          </div>
          {children}
        </>
      )}
    </dialog>
  );
}

// JsonView is a value as indented JSON.
export function JsonView({ value }: { value: unknown }) {
  return (
    <pre className="json" dir="ltr">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

export function Grid({ children, cols }: { children: ReactNode; cols?: 2 | 3 | 4 }) {
  return <div className={`grid grid-${cols ?? 2}`}>{children}</div>;
}
