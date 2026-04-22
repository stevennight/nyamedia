import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: '/admin/static/',
  build: {
    outDir: '../internal/web/static',
    emptyOutDir: true,
  },
})
