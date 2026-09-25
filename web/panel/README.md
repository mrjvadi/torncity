# Torn City — operations console (web client)

The operators' console of the Torn City Telegram game: a single-page React +
TypeScript client for the panel API. Bilingual (Persian, right-to-left, and
English), light and dark, mobile-first.

## Build

Requirements: Node.js 22.18 or later.

```sh
npm ci
npm run build        # type-checks, then writes the client to dist/
npm run lint
npm test             # node --test over src/**/*.test.ts
```

The build output is `dist/` (an `index.html` at its root and hashed assets
under `dist/assets/`). That directory is all a server needs: the panel
backend embeds or serves it as static files. Nothing is inlined, so it runs
under a strict Content-Security-Policy (`script-src 'self'; style-src 'self'`).

## Configuration

| Variable (build time) | Default | Meaning |
|---|---|---|
| `VITE_API_BASE` | *(empty)* | Prefix of the API. Empty: the API is on the page's own origin under `/api/…`. |

The live feed's WebSocket address comes from the API
(`GET /api/realtime/token`); a path is taken on the page's own origin.

For development, `npm run dev` serves the client on port 5173 and proxies
`/api` to a panel on `http://127.0.0.1:8090` and `/connection` to a
real-time server on `http://127.0.0.1:8000` (override with `PANEL_DEV_API`
and `PANEL_DEV_LIVE`).

## Structure

- `src/lib` — API client, formatting (Persian digits, Solar Hijri dates),
  chart arithmetic, live-feed protocol handling. Pure modules have tests.
- `src/components` — the design system: data table (`DataView`), charts,
  stat tiles, dialogs with reason and typed confirmation, search palette.
- `src/pages` — one module per console section, loaded on demand.
- `src/i18n` — `en.json` / `fa.json` (same keys, checked by a test) and the
  labels of the server's list columns.

---

# کنسول عملیات تورن‌سیتی (برنامهٔ وب)

کنسول اپراتورهای بازی تلگرامی تورن‌سیتی: برنامهٔ تک‌صفحه‌ای React و
TypeScript برای API پنل. دوزبانه (فارسی راست‌به‌چپ و انگلیسی)، روشن و تیره،
با اولویت موبایل.

## ساخت

نیازمندی: Node.js نسخهٔ ۲۲٫۱۸ یا بالاتر.

```sh
npm ci
npm run build        # بررسی نوع‌ها و سپس ساخت در dist/
npm run lint
npm test
```

خروجی ساخت پوشهٔ `dist/` است (فایل `index.html` در ریشه و دارایی‌ها در
`dist/assets/`). سرور تنها به همین پوشه نیاز دارد: بک‌اند پنل آن را جاسازی
یا به‌صورت فایل ایستا سرو می‌کند. هیچ چیز درون‌خطی نیست و برنامه زیر
سیاست امنیت محتوای سخت‌گیرانه اجرا می‌شود.

## پیکربندی

- `VITE_API_BASE` (هنگام ساخت): پیشوند API؛ خالی یعنی `/api/…` روی همان مبدأ صفحه.
- نشانی WebSocket جریان زنده از خود API (`GET /api/realtime/token`) می‌آید.
- در حالت توسعه (`npm run dev`) درخواست‌های `/api` به پنل روی
  `http://127.0.0.1:8090` و `/connection` به سرور بی‌درنگ روی
  `http://127.0.0.1:8000` فرستاده می‌شوند (با `PANEL_DEV_API` و `PANEL_DEV_LIVE` قابل تغییر).
