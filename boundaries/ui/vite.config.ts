import { readFileSync } from "node:fs";
import { Agent } from "node:https";
import { dirname, resolve } from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin, ProxyOptions } from "vite";
import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";

// Public BFF schema, the same file codegen reads; MSW mocks execute against it.
const publicSchemaPath = resolve(
  import.meta.dirname,
  "../bff/client-schema.graphql",
);

// Origins the built UI talks to: the public BFF, the optional Faro collector
// and the OIDC provider (absent only on mocks).
function connectOrigins(env: Record<string, string>): string[] {
  return [
    env.VITE_PUBLIC_API_URL,
    env.VITE_FARO_URL,
    env.VITE_OIDC_ISSUER,
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

// Dev server only: /api forwards to the public gateway of kind, so the browser
// stays on its own http origin and never sees the local CA. The proxy itself
// verifies the gateway against that CA (make kind-local-ca). Docker publishes
// the gateway on 127.0.0.1 only, while Node resolves *.localhost to ::1: the
// proxy dials the address and sends the gateway hostname as SNI and Host.
function kindApiProxy(): Record<string, ProxyOptions> {
  const target = process.env.NOMNOM_API_PROXY_TARGET;
  if (!target) return {};

  const gateway = new URL(target);
  const caPath = process.env.NOMNOM_API_PROXY_CA;
  if (!caPath) {
    throw new Error("NOMNOM_API_PROXY_CA is required with NOMNOM_API_PROXY_TARGET");
  }

  return {
    "/api": {
      target: `${gateway.protocol}//127.0.0.1:${gateway.port}`,
      rewrite: (path) => path.replace(/^\/api/, ""),
      headers: { host: gateway.host },
      agent: new Agent({
        // Relative to the repository root, where make kind-local-ca writes it.
        ca: readFileSync(resolve(import.meta.dirname, "../..", caPath)),
        servername: gateway.hostname,
      }),
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
    proxy: kindApiProxy(),
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
}));
