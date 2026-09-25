import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { useI18n, type Key } from '../i18n/index.tsx';
import { get, q } from '../lib/api.ts';
import type { SearchHit } from '../lib/types.ts';
import { go, href } from '../router.ts';
import { Icon } from './Icon.tsx';

// SearchPalette finds a player, company, city, country or faction by code
// or name from anywhere (Ctrl+K or /), and goes to it with Enter.

const ROUTE: Record<string, string> = { player: 'players', company: 'companies', city: 'cities', country: 'countries', faction: 'factions' };
const ICON: Record<string, string> = { player: 'user', company: 'briefcase', city: 'city', country: 'globe', faction: 'users' };

export function SearchPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  const ref = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [text, setText] = useState('');
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [active, setActive] = useState(0);
  const [busy, setBusy] = useState(false);
  const listId = useId();

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      d.showModal();
      setText('');
      setHits([]);
      window.setTimeout(() => input.current?.focus(), 0);
    }
    if (!open && d.open) d.close();
  }, [open]);

  useEffect(() => {
    const s = text.trim();
    if (!s) {
      setHits([]);
      return;
    }
    let live = true;
    const id = window.setTimeout(() => {
      setBusy(true);
      get<SearchHit[]>(`/api/search${q({ q: s })}`)
        .then((h) => {
          if (live) {
            setHits(h);
            setActive(0);
          }
        })
        .catch(() => live && setHits([]))
        .finally(() => live && setBusy(false));
    }, 200);
    return () => {
      live = false;
      window.clearTimeout(id);
    };
  }, [text]);

  const open_ = (h: SearchHit) => {
    const route = ROUTE[h.kind];
    if (route) go(href([route, h.code]).slice(1));
    onClose();
  };
  const keys = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') setActive((a) => Math.min(a + 1, hits.length - 1));
    else if (e.key === 'ArrowUp') setActive((a) => Math.max(a - 1, 0));
    else if (e.key === 'Enter' && hits[active]) open_(hits[active]);
    else return;
    e.preventDefault();
  };

  return (
    <dialog ref={ref} className="dialog palette" onClose={onClose} aria-label={t('search.title')}>
      <div className="palette-input">
        <Icon name="search" />
        <input
          ref={input}
          type="search"
          role="combobox"
          aria-expanded={hits.length > 0}
          aria-controls={listId}
          aria-activedescendant={hits[active] ? `${listId}-${active}` : undefined}
          value={text}
          dir="auto"
          placeholder={t('search.placeholder')}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={keys}
        />
        {busy && <span className="spinner" aria-hidden="true" />}
      </div>
      <ul id={listId} role="listbox" className="palette-list">
        {hits.map((h, i) => (
          <li
            key={`${h.kind}:${h.code}`}
            id={`${listId}-${i}`}
            role="option"
            aria-selected={i === active}
            onMouseEnter={() => setActive(i)}
            onClick={() => open_(h)}
          >
            <Icon name={ICON[h.kind] ?? 'info'} />
            <span className="palette-name">
              <bdi>{h.name}</bdi>
              {h.extra && <span className="muted small"> <bdi dir="ltr">{h.kind === 'player' ? `@${h.extra}` : h.extra}</bdi></span>}
            </span>
            <span className="muted small">{t(`search.kind_${h.kind}` as Key)}</span>
            <bdi className="code" dir="ltr">
              {h.code}
            </bdi>
          </li>
        ))}
        {text.trim() && !busy && hits.length === 0 && <li className="muted palette-empty">{t('search.nothing')}</li>}
      </ul>
      <p className="palette-hint muted small">{t('search.hint')}</p>
    </dialog>
  );
}
