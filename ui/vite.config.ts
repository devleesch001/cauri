import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

// The build goes where the Go package embedding it lives. Its .gitkeep is
// committed so that the embed compiles before any UI build; emptying the
// directory deletes it, so it is written again afterwards.
const outDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))
const keepDist: Plugin = {
  name: 'cauri-keep-dist',
  apply: 'build',
  closeBundle() {
    writeFileSync(outDir + '/.gitkeep', '')
  },
}

// In development, /api goes to a local "cauri api" (CAURI_API_URL to change it).
export default defineConfig({
  plugins: [vue(), keepDist],
  // vue-i18n build flags: Composition API only, no devtools in production.
  define: {
    __VUE_I18N_FULL_INSTALL__: true,
    __VUE_I18N_LEGACY_API__: false,
    __INTLIFY_PROD_DEVTOOLS__: false,
  },
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: { outDir, emptyOutDir: true },
  server: {
    // Fixed port: the VS Code launch configuration opens it; if it is
    // taken, Vite stops instead of moving to another one.
    port: 5173,
    strictPort: true,
    proxy: { '/api': process.env.CAURI_API_URL ?? 'http://localhost:8080' },
  },
})
