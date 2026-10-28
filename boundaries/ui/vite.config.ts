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
  return [env.VITE_PUBLIC_API_URL, env.VITE_FARO_URL, env.VITE_OIDC_ISSUER]
    .filter(Boolean)
    .map((url) => new URL(url).origin);
}

function contentSecurityPolicy(origins: string[]): string {
  return [
    "default-src 'self'",
    "script-src 'self'",
    // react-aria usePress prepends one fixed <style> with touch-action rules;
    // ops/helm (ui.contentSecurityPolicy) allows the same hash.
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

// The deployed origin sends these headers (nginx, ops/helm builds the same
// policy; local ADR 2) and serves /config.js from a ConfigMap. `vite preview`
// mirrors that origin from the Vite env, so a local build runs like the
// deployed one. The dev server stays without them: React Refresh injects an
// inline script.
function previewOrigin(mode: string): Plugin {
  const env = loadEnv(mode, import.meta.dirname, "VITE_");
  const origins = connectOrigins(env);
  const csp = contentSecurityPolicy(origins);

  return {
    name: "nomnom-preview-origin",
    configurePreviewServer(server) {
      server.middlewares.use((req, res, next) => {
        res.setHeader(
          "Content-Security-Policy",
          `${csp}; frame-ancestors 'none'`,
        );
        res.setHeader("Connection-Allowlist", connectionAllowlist(origins));
        if (req.url === "/config.js") {
          res.setHeader("Content-Type", "text/javascript");
          res.end(
            `window.__NOMNOM_CONFIG__ = Object.freeze(${JSON.stringify(env)});\n`,
          );
          return;
        }
        next();
      });
    },
  };
}

// Dev server only: /api forwards to the public gateway of kind and /dex to
// Dex behind it, so the browser stays on its own http origin and never sees
// the local CA. The proxy itself verifies the gateway against that CA (make
// kind-local-ca). Docker publishes the gateway on 127.0.0.1 only, while Node
// resolves *.localhost to ::1: the proxy dials the address and sends the
// gateway hostname as SNI and Host.
function kindProxy(): Record<string, ProxyOptions> {
  const apiTarget = process.env.NOMNOM_API_PROXY_TARGET;
  const authTarget = process.env.NOMNOM_AUTH_PROXY_TARGET;
  if (!apiTarget && !authTarget) return {};

  const caPath = process.env.NOMNOM_API_PROXY_CA;
  if (!caPath) {
    throw new Error(
      "NOMNOM_API_PROXY_CA is required with NOMNOM_API_PROXY_TARGET or NOMNOM_AUTH_PROXY_TARGET",
    );
  }
  // Relative to the repository root, where make kind-local-ca writes it.
  const ca = readFileSync(resolve(import.meta.dirname, "../..", caPath));
  function toGateway(target: string): ProxyOptions {
    const gateway = new URL(target);
    return {
      target: `${gateway.protocol}//127.0.0.1:${gateway.port}`,
      headers: { host: gateway.host },
      agent: new Agent({ ca, servername: gateway.hostname }),
    };
  }

  const proxy: Record<string, ProxyOptions> = {};
  if (apiTarget) {
    proxy["/api"] = {
      ...toGateway(apiTarget),
      rewrite: (path) => path.replace(/^\/api/, ""),
    };
  }
  if (authTarget) {
    // The discovery names the endpoints on the public issuer; the page gets
    // them on its own origin, so the login goes through this proxy too.
    // Tokens keep the public issuer, which BFF expects.
    const issuerOrigin = new URL(authTarget).origin;
    proxy["/dex/.well-known/openid-configuration"] = {
      ...toGateway(authTarget),
      selfHandleResponse: true,
      configure(server) {
        server.on("proxyRes", (proxyRes, req, res) => {
          const chunks: Buffer[] = [];
          proxyRes.on("data", (chunk: Buffer) => chunks.push(chunk));
          proxyRes.on("end", () => {
            const body = Buffer.concat(chunks)
              .toString("utf8")
              .replaceAll(issuerOrigin, `http://${req.headers.host}`);
            res.writeHead(proxyRes.statusCode ?? 502, {
              "content-type": "application/json",
            });
            res.end(body);
          });
        });
      },
    };
    proxy["/dex"] = toGateway(authTarget);
  }
  return proxy;
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), previewOrigin(mode)],
  resolve: {
    alias: [{ find: /^@public-schema(?=\?|$)/, replacement: publicSchemaPath }],
  },
  server: {
    port: 4173,
    fs: { allow: [import.meta.dirname, dirname(publicSchemaPath)] },
    proxy: kindProxy(),
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
}));
