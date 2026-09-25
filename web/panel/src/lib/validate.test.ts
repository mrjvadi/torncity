import assert from 'node:assert/strict';
import { test } from 'node:test';
import { checkReason, MAX_REASON, parseAmount, parseChatId } from './validate.ts';

test('a change without a reason is refused before it is sent', () => {
  assert.equal(checkReason(''), 'reason_required');
  assert.equal(checkReason('   \n '), 'reason_required');
  assert.equal(checkReason('compensation for the outage'), null);
  assert.equal(checkReason('ج'.repeat(MAX_REASON)), null);
  assert.equal(checkReason('x'.repeat(MAX_REASON + 1)), 'reason_too_long');
});

test('amounts are positive whole numbers in any digits', () => {
  assert.equal(parseAmount('1,500'), 1500);
  assert.equal(parseAmount('۲۰۰۰'), 2000);
  assert.equal(parseAmount('0'), null);
  assert.equal(parseAmount('-5'), null);
  assert.equal(parseAmount('1.5'), null);
  assert.equal(parseAmount('abc'), null);
});

test('a group chat id is negative', () => {
  assert.equal(parseChatId('-1001234567890'), -1001234567890);
  assert.equal(parseChatId('12345'), null);
});
