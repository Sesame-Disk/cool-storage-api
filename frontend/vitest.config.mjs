import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    css: true,
    setupFiles: ['./src/setupTests.vitest.js'],
    // Only pick up *.vitest.{js,jsx,ts,tsx} so existing Jest *.test.js
    // files keep running under Jest without interference.
    include: ['src/**/*.vitest.{js,jsx,ts,tsx}'],
    exclude: ['node_modules', 'build', 'dist', 'dist-vite', 'e2e'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      include: ['src/**/*.{js,jsx}'],
      exclude: ['src/**/*.test.{js,jsx}', 'src/**/*.vitest.{js,jsx}', 'src/**/__tests__/**'],
    },
  },
});
