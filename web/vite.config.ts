import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时把 /api 和 /export 代理到本地 `proxystack-web serve --dev`：
// PSW_PANEL_ORIGIN=http://127.0.0.1:9090 npm run dev
const origin = process.env.PSW_PANEL_ORIGIN ?? 'http://127.0.0.1:9090'

// 线上由后端在 index.html 注入 <base href>，前端据此计算面板根路径；开发服务器在这里补上，
// 否则在 /stacks/1 这类二级页面刷新时，请求和静态资源都会按当前目录解析而白屏。
const devBase: Plugin = {
  name: 'psw-dev-base',
  apply: 'serve',
  transformIndexHtml: (html) => html.replace('<!--psw:base-->', '<base href="/">'),
}

export default defineConfig({
  base: './',
  plugins: [vue(), devBase],
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
