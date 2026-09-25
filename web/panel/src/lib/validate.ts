// What every change asks of its operator before it is sent: a reason, and
// inputs that make sense. The server checks all of it again.

export const MAX_REASON = 500;

export type ReasonProblem = 'reason_required' | 'reason_too_long' | null;

export function checkReason(reason: string): ReasonProblem {
  const r = reason.trim();
  if (r.length === 0) return 'reason_required';
  if ([...r].length > MAX_REASON) return 'reason_too_long';
  return null;
}

// A positive whole amount, typed in any digits, or null.
export function parseAmount(raw: string): number | null {
  const s = raw
    .replace(/[۰-۹]/g, (c) => String(c.charCodeAt(0) - 0x06f0))
    .replace(/[,\s٬]/g, '');
  if (!/^\d{1,15}$/.test(s)) return null;
  const v = Number(s);
  return v > 0 ? v : null;
}

// A Telegram group's chat id: negative.
export function parseChatId(raw: string): number | null {
  const s = raw.trim().replace(/[۰-۹]/g, (c) => String(c.charCodeAt(0) - 0x06f0));
  if (!/^-\d{1,16}$/.test(s)) return null;
  return Number(s);
}
