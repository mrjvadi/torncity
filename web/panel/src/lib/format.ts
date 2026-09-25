import type { Lang } from './lang.ts';

// Numbers, money, percentages, instants and durations, in the panel's two
// languages: Persian digits and the Solar Hijri calendar in fa (fa-IR with
// the Persian calendar), Western digits and the Gregorian calendar in en.
// Instants are shown on the game's clock, Tehran time.

const LOCALE: Record<Lang, string> = { fa: 'fa-IR-u-ca-persian-nu-arabext', en: 'en-GB-u-ca-gregory' };
const ZONE = 'Asia/Tehran';

const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat>();
function fmt<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat>(key: string, make: () => T): T {
  let f = cache.get(key) as T | undefined;
  if (!f) {
    f = make();
    cache.set(key, f);
  }
  return f;
}

// Whole numbers, grouped, in the language's digits.
export function num(lang: Lang, v: number | bigint | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return fmt(`n:${lang}`, () => new Intl.NumberFormat(LOCALE[lang], { maximumFractionDigits: 0 })).format(v);
}

// A short form of a large number: 12.3K, 4.5M (and the Persian words in fa).
export function compact(lang: Lang, v: number | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return fmt(`c:${lang}`, () => new Intl.NumberFormat(LOCALE[lang], { notation: 'compact', maximumFractionDigits: 1 })).format(v);
}

// Basis points as a percentage: 12345 -> 123.45%.
export function bps(lang: Lang, v: number | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return fmt(
    `p:${lang}`,
    () => new Intl.NumberFormat(LOCALE[lang], { style: 'percent', minimumFractionDigits: 0, maximumFractionDigits: 2 }),
  ).format(v / 10000);
}

// A signed change as a percentage of its base: +3.2%, −1.0%.
export function delta(lang: Lang, now: number, before: number): string | null {
  if (!Number.isFinite(now) || !Number.isFinite(before) || before === 0) return null;
  const r = (now - before) / Math.abs(before);
  return fmt(
    `d:${lang}`,
    () => new Intl.NumberFormat(LOCALE[lang], { style: 'percent', maximumFractionDigits: 1, signDisplay: 'exceptZero' }),
  ).format(r);
}

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

// An instant, on the game's clock.
export function when(lang: Lang, iso: string | null | undefined): string {
  const d = parse(iso);
  if (!d) return '—';
  return fmt(
    `w:${lang}`,
    () => new Intl.DateTimeFormat(LOCALE[lang], { dateStyle: 'medium', timeStyle: 'short', timeZone: ZONE }),
  ).format(d);
}

// A day (YYYY-MM-DD, already on the game's clock) in the language's calendar.
export function day(lang: Lang, ymd: string, style: 'short' | 'long' = 'short'): string {
  const d = new Date(`${ymd}T12:00:00Z`);
  if (Number.isNaN(d.getTime())) return ymd;
  const opts: Intl.DateTimeFormatOptions =
    style === 'short' ? { month: 'short', day: 'numeric', timeZone: 'UTC' } : { dateStyle: 'medium', timeZone: 'UTC' };
  return fmt(`day:${lang}:${style}`, () => new Intl.DateTimeFormat(LOCALE[lang], opts)).format(d);
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 86400],
  ['month', 30 * 86400],
  ['week', 7 * 86400],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
];

// How long ago (or how long until) an instant: "3 hours ago", "in 5 minutes".
export function relative(lang: Lang, iso: string | null | undefined, now: number = Date.now()): string {
  const d = parse(iso);
  if (!d) return '—';
  const secs = Math.round((d.getTime() - now) / 1000);
  const rtf = fmt(`r:${lang}`, () => new Intl.RelativeTimeFormat(LOCALE[lang], { numeric: 'auto' }));
  for (const [unit, size] of UNITS) {
    if (Math.abs(secs) >= size || unit === 'second') return rtf.format(Math.trunc(secs / size), unit);
  }
  return '—';
}

// A length of time in seconds: "2h 5m" in the language's digits.
export function duration(lang: Lang, secs: number | null | undefined): string {
  if (secs === null || secs === undefined) return '—';
  const s = Math.abs(Math.round(secs));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const parts: string[] = [];
  const unit = lang === 'fa' ? { d: 'روز', h: 'ساعت', m: 'دقیقه', s: 'ثانیه' } : { d: 'd', h: 'h', m: 'm', s: 's' };
  const sep = lang === 'fa' ? ' ' : '';
  if (d) parts.push(`${num(lang, d)}${sep}${unit.d}`);
  if (h) parts.push(`${num(lang, h)}${sep}${unit.h}`);
  if (m && !d) parts.push(`${num(lang, m)}${sep}${unit.m}`);
  if (parts.length === 0) parts.push(`${num(lang, s % 60)}${sep}${unit.s}`);
  return parts.join(' ');
}

// Any digits typed (Persian or Arabic-Indic) as ASCII, for inputs.
export function asciiDigits(s: string): string {
  return s
    .replace(/[۰-۹]/g, (c) => String(c.charCodeAt(0) - 0x06f0))
    .replace(/[٠-٩]/g, (c) => String(c.charCodeAt(0) - 0x0660));
}

// Text with its digits in the language's script (for codes and ids shown
// inside a sentence).
export function digits(lang: Lang, s: string): string {
  if (lang !== 'fa') return s;
  return s.replace(/[0-9]/g, (c) => String.fromCharCode(0x06f0 + Number(c)));
}

// A key such as "net_worth" as words: "Net worth".
export function humanize(key: string): string {
  const s = key.replace(/[._]+/g, ' ').trim();
  return s.charAt(0).toUpperCase() + s.slice(1);
}
