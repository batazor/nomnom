import { dirname, resolve } from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";

// Public BFF schema, the same file codegen reads; MSW mocks execute against it.
const publicSchemaPath = resolve(
  import.meta.dirname,
  process.env.GRAPHQL_SCHEMA ?? "../bff/plugins/bff/src/schema.graphql",
);

// Origins the built UI talks to: the public BFF, the optional Faro collector
// and the optional dev token issuer.
function connectOrigins(env: Record<string, string>): string[] {
  return [
    env.VITE_PUBLIC_GRAPHQL_URL,
    env.VITE_FARO_URL,
    env.VITE_DEV_TOKENS_URL,
  ]
    .filter(Boolean)
    .map((url) => new URL(url).origin);
}

function contentSecurityPolicy(origins: string[]): string {
  return [
    "default-src 'self'",
    "script-src 'self'",
    // react-aria usePress prepends one fixed <style> with touch-action rules.
    "style-src 'self' 'sha256-38RhXrc7EdReTKsOm23ZPOCUgniTUUcjky8QOOrQx6o='",
    "img-src 'self' data:",
    "font-src 'self'",
    `connect-src 'self' ${origins.join(" ")}`.trim(),
    "worker-src 'self'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join("; ");
}

// Connection allowlists (Chrome 152+) gate every connection, not only the
// request types CSP knows about; browsers without support ignore the header.
function connectionAllowlist(origins: string[]): string {
  const patterns = origins.map((origin) => `"${origin}/*"`);
  return `(${[...patterns, "response-origin"].join(" ")})`;
}

// The production build carries the CSP as a meta tag, because the hosting model
// is not decided yet (ops/README.md). The dev server stays without it: React
// Refresh injects an inline script. `vite preview` sends the real headers,
// including those a meta tag cannot carry.
function securityHeaders(mode: string): Plugin {
  const origins = connectOrigins(loadEnv(mode, import.meta.dirname, "VITE_"));
  const csp = contentSecurityPolicy(origins);

  return {
    name: "nomnom-security-headers",
    transformIndexHtml: {
      order: "post",
      handler: (html, ctx) =>
        ctx.server
          ? html
          : html.replace(
              /<meta charset="UTF-8" \/>/,
              `$&\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`,
            ),
    },
    configurePreviewServer(server) {
      server.middlewares.use((_req, res, next) => {
        res.setHeader(
          "Content-Security-Policy",
          `${csp}; frame-ancestors 'none'`,
        );
        res.setHeader("Connection-Allowlist", connectionAllowlist(origins));
        next();
      });
    },
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), securityHeaders(mode)],
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
}));
