import type { ReactNode } from 'react';
import { useI18n, type Key } from '../i18n/index.tsx';
import { ApiError } from '../lib/api.ts';

export function Card({ title, actions, children }: { title?: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <header className="card-head">
          {title && <h2>{title}</h2>}
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

export function PageTitle({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-title">
      <h1>{children}</h1>
      {actions}
    </div>
  );
}

// KV is a list of labelled values.
export function KV({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="kv">
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

// Table becomes a list of cards on a narrow screen: every cell carries its
// column's label for that layout.
export function Table<T>({ rows, cols, rowKey, onRow }: {
  rows: T[];
  cols: Column<T>[];
  rowKey: (row: T) => string;
  onRow?: (row: T) => void;
}) {
  const { t } = useI18n();
  if (rows.length === 0) return <p className="muted">{t('common.none')}</p>;
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

export function Loading() {
  const { t } = useI18n();
  return (
    <p className="muted" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" /> {t('common.loading')}
    </p>
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
export function Async<T>({ load, children }: {
  load: { data: T | null; error: unknown; loading: boolean; reload: () => void };
  children: (data: T) => ReactNode;
}) {
  if (load.error) return <ErrorBox error={load.error} retry={load.reload} />;
  if (load.data === null) return <Loading />;
  return <>{children(load.data)}</>;
}

export function Badge({ tone, children }: { tone?: 'ok' | 'bad' | 'warn' | 'info'; children: ReactNode }) {
  return <span className={`badge ${tone ?? ''}`}>{children}</span>;
}

// Code is an identifier: always left-to-right, even inside Persian text.
export function Code({ children }: { children: ReactNode }) {
  return (
    <bdi className="code" dir="ltr">
      {children}
    </bdi>
  );
}

export function Money({ v }: { v: number | null | undefined }) {
  const { n } = useI18n();
  return <span className={`money ${v !== null && v !== undefined && v < 0 ? 'neg' : ''}`}>{n(v)}</span>;
}

export function Link({ to, children }: { to: string; children: ReactNode }) {
  return <a href={`#${to}`}>{children}</a>;
}
