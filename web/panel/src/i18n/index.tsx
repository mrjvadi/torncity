import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import en from './en.json';
import fa from './fa.json';
import { colLabel, valueLabel, viewTitle } from './labels.ts';
import { applyLanguage, initialLang, rememberLang, type Lang } from '../lib/lang.ts';
import { bps, compact, day, duration, num, relative, when } from '../lib/format.ts';

export type Key = keyof typeof en;

const DICTS: Record<Lang, Record<string, string>> = { en, fa };

export function translate(lang: Lang, key: Key, vars?: Record<string, string | number>): string {
  let s = DICTS[lang][key] ?? DICTS.en[key] ?? key;
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      s = s.replace(`{${k}}`, typeof v === 'number' ? num(lang, v) : v);
    }
  }
  return s;
}

export interface I18n {
  lang: Lang;
  dir: 'rtl' | 'ltr';
  setLang: (l: Lang) => void;
  t: (key: Key, vars?: Record<string, string | number>) => string;
  n: (v: number | null | undefined) => string;
  nc: (v: number | null | undefined) => string;
  pct: (v: number | null | undefined) => string;
  at: (iso: string | null | undefined) => string;
  ago: (iso: string | null | undefined) => string;
  dayLabel: (ymd: string, style?: 'short' | 'long') => string;
  dur: (secs: number | null | undefined) => string;
  col: (key: string) => string;
  val: (v: string) => string | undefined;
  view: (name: string) => string;
}

const Ctx = createContext<I18n | null>(null);

function storage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(() => initialLang(storage(), navigator.language));
  useEffect(() => {
    applyLanguage(lang, document.documentElement);
  }, [lang]);
  const setLang = useCallback((l: Lang) => {
    rememberLang(storage(), l);
    setLangState(l);
  }, []);
  const value = useMemo<I18n>(
    () => ({
      lang,
      dir: lang === 'fa' ? 'rtl' : 'ltr',
      setLang,
      t: (key, vars) => translate(lang, key, vars),
      n: (v) => num(lang, v),
      nc: (v) => compact(lang, v),
      pct: (v) => bps(lang, v),
      at: (iso) => when(lang, iso),
      ago: (iso) => relative(lang, iso),
      dayLabel: (ymd, style) => day(lang, ymd, style),
      dur: (secs) => duration(lang, secs),
      col: (key) => colLabel(lang, key),
      val: (v) => valueLabel(lang, v),
      view: (name) => viewTitle(lang, name),
    }),
    [lang, setLang],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useI18n(): I18n {
  const v = useContext(Ctx);
  if (!v) throw new Error('useI18n outside I18nProvider');
  return v;
}
