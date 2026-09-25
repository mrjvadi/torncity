// The two languages the panel speaks, and what switching between them
// changes on the page: the document's language and its direction.

export type Lang = 'fa' | 'en';

export const LANGS: readonly Lang[] = ['fa', 'en'];

export function isLang(v: unknown): v is Lang {
  return v === 'fa' || v === 'en';
}

export function directionOf(lang: Lang): 'rtl' | 'ltr' {
  return lang === 'fa' ? 'rtl' : 'ltr';
}

// The part of an element applyLanguage touches, so it can be tested
// without a browser.
export interface LangTarget {
  lang: string;
  dir: string;
}

export function applyLanguage(lang: Lang, root: LangTarget): void {
  root.lang = lang;
  root.dir = directionOf(lang);
}

const LANG_KEY = 'panel.lang';

// The language remembered on this device, or the browser's when it prefers
// English, else Persian.
export function initialLang(store: Pick<Storage, 'getItem'> | undefined, browser: string | undefined): Lang {
  try {
    const saved = store?.getItem(LANG_KEY);
    if (isLang(saved)) return saved;
  } catch {
    // storage may be blocked
  }
  return browser?.toLowerCase().startsWith('en') ? 'en' : 'fa';
}

export function rememberLang(store: Pick<Storage, 'setItem'> | undefined, lang: Lang): void {
  try {
    store?.setItem(LANG_KEY, lang);
  } catch {
    // storage may be blocked; the choice then lasts for this page only
  }
}
