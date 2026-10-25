import { dirname, resolve } from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Public BFF schema, the same file codegen reads; MSW mocks execute against it.
const publicSchemaPath = resolve(
  import.meta.dirname,
  process.env.GRAPHQL_SCHEMA ?? "../bff/plugins/bff/src/schema.graphql",
);

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [{ find: /^@public-schema(?=\?|$)/, replacement: publicSchemaPath }],
  },
  server: {
    port: 4173,
    fs: { allow: [import.meta.dirname, dirname(publicSchemaPath)] },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
