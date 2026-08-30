import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Das Build-Ergebnis wandert nach internal/webui/dist und wird von dort ins
// Go-Binary eingebettet — deshalb gibt es am Zielserver kein Node.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 700,
  },
  server: {
    port: 5173,
    proxy: {
      // Im Entwicklungsbetrieb zeigt Vite auf das laufende `volt serve`.
      '/api': { target: 'http://127.0.0.1:8443', changeOrigin: true },
      '/healthz': { target: 'http://127.0.0.1:8443', changeOrigin: true },
    },
  },
})
