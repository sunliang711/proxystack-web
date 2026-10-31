import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时把 /api 和 /export 代理到本地 `proxystack-web serve --dev`：
// PSW_PANEL_ORIGIN=http://127.0.0.1:9090 npm run dev
const origin = process.env.PSW_PANEL_ORIGIN ?? 'http://127.0.0.1:9090'

export default defineConfig({
  base: './',
  plugins: [vue()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    proxy: {
      '/api': origin,
      '/export': origin,
    },
  },
})
