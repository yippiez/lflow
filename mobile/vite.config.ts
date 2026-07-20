import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build lands inside the Go tree: pkg/tui/daemon/webui embeds dist/ so
// `lflow serve --http` ships the app. Dev mode proxies /api to a running
// `lflow serve --http :7420`.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../pkg/tui/daemon/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7420',
        changeOrigin: true,
      },
    },
  },
})
