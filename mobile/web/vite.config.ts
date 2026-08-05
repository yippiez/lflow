import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build lands next to the Go file that embeds it: mobile ships
// dist/ inside the binary, so `lflow serve --http` serves app and API from one
// port. Dev mode proxies /api to a running `lflow serve --http :7420`.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../dist',
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
