import assert from 'node:assert/strict';
import { test } from 'node:test';
import { applyLanguage, directionOf, initialLang, rememberLang, type LangTarget } from './lang.ts';
import { asciiDigits, digits, num } from './format.ts';

test('switching the language switches the page direction', () => {
  const root: LangTarget = { lang: '', dir: '' };
  applyLanguage('fa', root);
  assert.deepEqual(root, { lang: 'fa', dir: 'rtl' });
  applyLanguage('en', root);
  assert.deepEqual(root, { lang: 'en', dir: 'ltr' });
  assert.equal(directionOf('fa'), 'rtl');
});

test('the chosen language is remembered, and a broken store is survived', () => {
  const mem = new Map<string, string>();
  const store = { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => void mem.set(k, v) };
  assert.equal(initialLang(store, 'en-US'), 'en');
  assert.equal(initialLang(store, 'de-DE'), 'fa');
  rememberLang(store, 'en');
  assert.equal(initialLang(store, 'fa-IR'), 'en');
  const broken = {
    getItem: (): string | null => {
      throw new Error('blocked');
    },
    setItem: (): void => {
      throw new Error('blocked');
    },
  };
  assert.equal(initialLang(broken, undefined), 'fa');
  assert.doesNotThrow(() => rememberLang(broken, 'fa'));
});

test('Persian shows Persian digits and inputs accept them', () => {
  assert.equal(num('fa', 1234), '۱٬۲۳۴');
  assert.equal(num('en', 1234), '1,234');
  assert.equal(digits('fa', 'AB12'), 'AB۱۲');
  assert.equal(asciiDigits('۱۲۳٤٥'), '12345');
});
