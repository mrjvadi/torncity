// Light, dark, or whatever the device prefers; remembered on this device.

export type Theme = 'light' | 'dark' | 'system';

const KEY = 'panel.theme';

export function initialTheme(): Theme {
  try {
    const v = window.localStorage.getItem(KEY);
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    // storage may be blocked
  }
  return 'system';
}

export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  if (theme === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', theme);
  try {
    window.localStorage.setItem(KEY, theme);
  } catch {
    // storage may be blocked
  }
}

export function nextTheme(t: Theme): Theme {
  return t === 'system' ? 'light' : t === 'light' ? 'dark' : 'system';
}
