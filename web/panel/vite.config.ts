import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The built client (dist/) is served by the panel backend as static files.
// Nothing is inlined: the panel's Content-Security-Policy allows only
// same-origin scripts, styles and fonts. In development, /api is proxied to
// a panel running locally and /connection to the real-time server.
const api = process.env.PANEL_DEV_API ?? 'http://127.0.0.1:8090';
const live = process.env.PANEL_DEV_LIVE ?? 'http://127.0.0.1:8000';

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
    target: 'es2022',
  },
  server: {
    proxy: {
      '/api': api,
      '/connection': { target: live, ws: true },
    },
  },
});
