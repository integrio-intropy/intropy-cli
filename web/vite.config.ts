import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import svgr from 'vite-plugin-svgr'

// The production build lands in dist/, which the Go `web` package embeds via
// go:embed. During development, `npm run dev` proxies /api to a running
// `intropy int run` (default port 8730) so the SPA talks to the real CLI.
// svgr turns `.svg?react` imports (Material Symbols icons) into components,
// so only the icons actually imported end up in the bundle — no icon font.
export default defineConfig({
  plugins: [react(), svgr()],
  base: '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8730',
    },
  },
})
