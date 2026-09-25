// Per-device conveniences (a list's hidden columns, its page size),
// remembered in localStorage when it is available and forgotten otherwise.

const PREFIX = 'panel.pref.';

export function loadPref(key: string): string | null {
  try {
    return window.localStorage.getItem(PREFIX + key);
  } catch {
    return null;
  }
}

export function savePref(key: string, value: string): void {
  try {
    window.localStorage.setItem(PREFIX + key, value);
  } catch {
    // storage may be blocked; the choice lasts for this page only
  }
}
