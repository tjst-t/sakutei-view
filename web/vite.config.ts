import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
//
// In dev, the browser talks to Vite's own server, which proxies /api/*
// through to the real sakutei-view backend (cmd/sakutei-view) so there is
// no CORS configuration to maintain and the frontend code never needs to
// know the backend's address. Override the target with VITE_API_PROXY_TARGET
// if the backend is listening somewhere other than the default
// 127.0.0.1:8080 (see README.md's LISTEN_ADDR).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
