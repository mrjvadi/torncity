import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The built client is embedded into cmd/panel (web/panel/embed.go). Nothing
// is inlined: the panel's Content-Security-Policy allows only same-origin
// scripts, styles and fonts. In development, /api is proxied to a panel
// running locally (TORN_PANEL_PUBLIC_URL=http://localhost:5173).
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
    proxy: { '/api': 'http://127.0.0.1:8090' },
  },
});
