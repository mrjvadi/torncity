import assert from 'node:assert/strict';
import { test } from 'node:test';
import { bps, day, delta, duration, humanize, num, relative, when } from './format.ts';

test('Persian uses Persian digits, English Western ones', () => {
  assert.equal(num('fa', 1234567), '۱٬۲۳۴٬۵۶۷');
  assert.equal(num('en', 1234567), '1,234,567');
  assert.equal(num('en', null), '—');
});

test('basis points are percentages', () => {
  assert.equal(bps('en', 1250), '12.5%');
  assert.ok(bps('fa', 1250).includes('۱۲'));
});

test('dates are Solar Hijri in Persian and Gregorian in English, on Tehran time', () => {
  const iso = '2026-09-25T21:00:00Z'; // 00:30 on the 26th in Tehran
  assert.match(when('en', iso), /26 Sept 2026|26 Sep 2026/);
  assert.match(when('fa', iso), /۱۴۰۵/);
  assert.match(day('fa', '2026-03-21', 'long'), /۱۴۰۵/);
});

test('relative times and durations', () => {
  const now = Date.parse('2026-09-25T12:00:00Z');
  assert.equal(relative('en', '2026-09-25T09:00:00Z', now), '3 hours ago');
  assert.equal(relative('en', '2026-09-25T12:05:00Z', now), 'in 5 minutes');
  assert.equal(duration('en', 7500), '2h 5m');
  assert.equal(duration('en', 90000), '1d 1h');
});

test('changes and words', () => {
  assert.equal(delta('en', 110, 100), '+10%');
  assert.equal(delta('en', 1, 0), null);
  assert.equal(humanize('net_worth'), 'Net worth');
});
