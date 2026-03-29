import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    hmr: {
      path: '/__vite_hmr',
    },
    proxy: {
      '/api/v1/ws': {
        target: 'ws://api:8080',
        ws: true,
      },
      '/api': {
        target: 'http://api:8080',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://api:8080',
        changeOrigin: true,
      },
      '/simulator': {
        target: 'http://simulator:8085',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/simulator/, ''),
      },
    },
  },
})
