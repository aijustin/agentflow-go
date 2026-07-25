import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// base './' keeps asset URLs relative so the SPA works under any mount prefix
// (the dashboard is served with http.StripPrefix, e.g. /observability/).
export default defineConfig({
  base: './',
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // pnpm dev: proxy API calls to a locally running agentflow example.
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7060/observability',
        changeOrigin: true,
      },
    },
  },
});
