import assert from 'node:assert/strict';
import { test } from 'node:test';
import { foldSeries, niceScale, stackSeries } from './chart.ts';

test('a scale covers the data with round steps and zero', () => {
  const s = niceScale(0, 93, 4);
  assert.equal(s.min, 0);
  assert.equal(s.max, 100);
  assert.deepEqual(s.ticks, [0, 25, 50, 75, 100]);
  const neg = niceScale(-30, 70, 4);
  assert.ok(neg.min <= -30 && neg.max >= 70 && neg.ticks.includes(0));
  assert.deepEqual(niceScale(0, 0, 4).ticks.slice(0, 1), [0]);
});

test('stacks put each layer on the one before, negatives apart', () => {
  assert.deepEqual(stackSeries([[1, 2], [3, -4]]), [
    [[0, 1], [0, 2]],
    [[1, 4], [0, -4]],
  ]);
});

test('lines beyond the colours fold into one', () => {
  const lines = Array.from({ length: 10 }, (_, i) => ({ key: `k${i}`, values: [i, i] }));
  const out = foldSeries(lines, 7, 'other');
  assert.equal(out.length, 8);
  assert.equal(out[0]?.key, 'k9');
  assert.equal(out[7]?.key, 'other');
  assert.deepEqual(out[7]?.values, [0 + 1 + 2, 0 + 1 + 2]);
  assert.equal(foldSeries(lines.slice(0, 8), 7, 'other').length, 8);
});
