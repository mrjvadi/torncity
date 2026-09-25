import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const load = (name: string): Record<string, string> =>
  JSON.parse(readFileSync(new URL(`./${name}.json`, import.meta.url), 'utf8')) as Record<string, string>;

test('Persian and English carry the same keys, none empty', () => {
  const fa = load('fa');
  const en = load('en');
  assert.deepEqual(Object.keys(fa).sort(), Object.keys(en).sort());
  for (const [k, v] of [...Object.entries(fa), ...Object.entries(en)]) assert.ok(v.trim() !== '', `${k} is empty`);
});
