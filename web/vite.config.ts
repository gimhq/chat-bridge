/// <reference types="vitest/config" />
import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The Go binary embeds the build (internal/webui); the placeholder keeps the directory present
// in clean clones so `go build` works before the UI is built.
const outDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))

export default defineConfig({
  base: '/ui/',
  plugins: [
    // tanstackRouter must come before react().
    tanstackRouter({
      target: 'react',
      routesDirectory: 'src/app/routes',
      generatedRouteTree: 'src/app/routeTree.gen.ts',
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
    {
      name: 'keep-dist-placeholder',
      apply: 'build',
      closeBundle() {
        writeFileSync(`${outDir}/.gitkeep`, '')
      },
    },
  ],
  resolve: { tsconfigPaths: true },
  server: { host: '0.0.0.0' },
  build: { outDir, emptyOutDir: true },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['src/test/setup.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/app/routeTree.gen.ts', 'src/shared/components/ui/**', 'src/main.tsx', 'src/test/**', '**/*.test.{ts,tsx}'],
    },
  },
})
