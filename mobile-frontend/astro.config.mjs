import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import tailwindcss from '@tailwindcss/vite';

// Dev-server API proxy target. Defaults to the local backend dev server; set
// MOBILE_DEV_API to point the mobile dev server at a running stack (e.g. the
// web nginx at http://localhost:18000, which routes to sesamefs + sesameauth).
const API_PROXY = process.env.MOBILE_DEV_API || 'http://localhost:3000';

export default defineConfig({
  integrations: [react()],
  output: 'static',
  site: 'http://localhost:4321',
  // The dev toolbar is a bottom-anchored dev-only overlay that intercepts clicks
  // on bottom sheet buttons — absent in the production build, so disable it to
  // keep the dev test loop faithful to the container.
  devToolbar: { enabled: false },
  vite: {
    plugins: [tailwindcss()],
    server: {
      proxy: {
        '/api2': API_PROXY,
        '/api/v2.1': API_PROXY,
        '/api/v2/': API_PROXY, // block-upload endpoints live under /api/v2/
        '/api/v2/blocks': API_PROXY,
        '/seafhttp': API_PROXY,
        '/media': API_PROXY,
        '/accounts': API_PROXY,
      },
    },
  },
});
