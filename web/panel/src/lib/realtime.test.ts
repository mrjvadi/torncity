import assert from 'node:assert/strict';
import { test } from 'node:test';
import { backoff, FEED_SIZE, initialLive, parseEvent, reduceLive, wsURL, type LiveEvent } from './realtime.ts';

const at = '2026-09-25T10:00:00Z';

test('only well-formed events on panel:ops are taken', () => {
  assert.deepEqual(parseEvent({ type: 'audit', at, data: { action: 'x' } }), { type: 'audit', at, data: { action: 'x' } });
  for (const bad of [null, 'x', 1, {}, { type: 'nope', at, data: {} }, { type: 'audit', data: {} }, { type: 'audit', at, data: [] },
    { type: 'audit', at, data: null }]) {
    assert.equal(parseEvent(bad), null, JSON.stringify(bad));
  }
});

test('figures replace the dashboard, other events join the feed', () => {
  const kpis: LiveEvent = { type: 'kpis', at, data: { players: 5, health: 'lagging' } };
  let s = reduceLive(initialLive, kpis);
  assert.deepEqual(s.kpis, kpis.data);
  assert.equal(s.health, 'lagging');
  assert.equal(s.feed.length, 0);
  s = reduceLive(s, { type: 'flag', at, data: { no: 1 } });
  s = reduceLive(s, { type: 'health', at, data: { state: 'ok', was: 'lagging' } });
  assert.equal(s.health, 'ok');
  assert.equal(s.feed.length, 2);
  assert.equal(s.feed[0]?.type, 'health');
  assert.equal(s.unseen, 2);
  assert.deepEqual(s.ticks, { kpis: 1, flag: 1, health: 1 });
});

test('the feed keeps only the latest events', () => {
  let s = initialLive;
  for (let i = 0; i < FEED_SIZE + 10; i++) s = reduceLive(s, { type: 'audit', at, data: { id: i } });
  assert.equal(s.feed.length, FEED_SIZE);
  assert.equal(s.feed[0]?.data.id, FEED_SIZE + 9);
});

test('the WebSocket follows the page, or an absolute address', () => {
  assert.equal(wsURL('/connection/websocket', { protocol: 'https:', host: 'panel.example.test' }), 'wss://panel.example.test/connection/websocket');
  assert.equal(wsURL('connection/websocket', { protocol: 'http:', host: 'localhost:5173' }), 'ws://localhost:5173/connection/websocket');
  assert.equal(wsURL('wss://live.example.test/ws', { protocol: 'https:', host: 'x' }), 'wss://live.example.test/ws');
});

test('reconnects back off, capped, with jitter', () => {
  assert.equal(backoff(0, () => 0), 500);
  assert.equal(backoff(0, () => 0.999999), 1000);
  assert.equal(backoff(3, () => 0), 4000);
  assert.equal(backoff(20, () => 0.999999), 30000);
});
