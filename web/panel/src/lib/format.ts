import type { Lang } from './lang.ts';

const LOCALE: Record<Lang, string> = { fa: 'fa-IR', en: 'en-US' };

// Whole numbers, grouped, in the language's digits (Persian digits in fa).
export function num(lang: Lang, v: number | bigint | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return new Intl.NumberFormat(LOCALE[lang], { maximumFractionDigits: 0 }).format(v);
}

// Basis points as a percentage: 12345 -> 123.45%.
export function bps(lang: Lang, v: number | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return new Intl.NumberFormat(LOCALE[lang], { style: 'percent', minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(
    v / 10000,
  );
}

// An instant, in Tehran time, the game's clock.
export function when(lang: Lang, iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '—';
  return new Intl.DateTimeFormat(LOCALE[lang], {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'Asia/Tehran',
  }).format(d);
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
