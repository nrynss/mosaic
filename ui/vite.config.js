import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

const proxyTarget = process.env.MOSAIC_API_PROXY_TARGET || 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': {
        target: proxyTarget,
        changeOrigin: true
      }
    }
  }
});
