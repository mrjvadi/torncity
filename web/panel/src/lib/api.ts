// The panel's API client. The session is an HttpOnly cookie the page never
// sees; every change carries the session's CSRF token and a fresh
// idempotency key, so a retried click is answered, not repeated.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail: string;
  constructor(status: number, code: string, detail: string) {
    super(code);
    this.status = status;
    this.code = code;
    this.detail = detail;
  }
}

let csrf = '';
let onUnauthorized: () => void = () => {};

export function setCsrf(token: string): void {
  csrf = token;
}

export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn;
}

export function newKey(): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

async function request<T>(method: string, path: string, body?: unknown, key?: string): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET') {
    if (csrf) headers['X-CSRF-Token'] = csrf;
    if (key) headers['Idempotency-Key'] = key;
  }
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
      cache: 'no-store',
    });
  } catch {
    throw new ApiError(0, 'network', '');
  }
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) {
    const e = (data ?? {}) as { error?: string; message?: string };
    if (res.status === 401 && path !== '/api/auth/login') onUnauthorized();
    throw new ApiError(res.status, e.error ?? 'internal', e.message ?? '');
  }
  return data as T;
}

export function get<T>(path: string): Promise<T> {
  return request<T>('GET', path);
}

// post sends a change. The same key must be reused when the same change is
// retried; a fresh one is made per confirmed action.
export function post<T>(path: string, body: unknown, key: string = newKey()): Promise<T> {
  return request<T>('POST', path, body, key);
}

export interface Session {
  username: string;
  csrf: string;
  expires_at: string;
  idle_timeout: string;
}

export async function login(username: string, password: string, code: string): Promise<Session> {
  const s = await request<Session>('POST', '/api/auth/login', { username, password, code });
  setCsrf(s.csrf);
  return s;
}

export async function currentSession(): Promise<Session | null> {
  try {
    const s = await request<Session>('GET', '/api/auth/session');
    setCsrf(s.csrf);
    return s;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return null;
    throw e;
  }
}

export async function logout(): Promise<void> {
  await request('POST', '/api/auth/logout', {});
  setCsrf('');
}

export function q(params: Record<string, string | number | undefined>): string {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') u.set(k, String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
}
