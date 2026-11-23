import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: { outDir: '../infrastructure/controller/http/web/static', emptyOutDir: true },
  server: {
    proxy: Object.fromEntries(
      ['/api', '/health', '/ready'].map((path) => [
        path,
        { target: process.env.FASTRAG_API_URL || 'http://127.0.0.1:8080', changeOrigin: true },
      ]),
    ),
  },
})
