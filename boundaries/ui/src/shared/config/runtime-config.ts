export const supportedLocales = ["ru", "en"] as const;

export type SupportedLocale = (typeof supportedLocales)[number];

import type { OidcConfig } from "../auth/oidc";

export type RuntimeConfig = Readonly<{
  // Base URL of the public API (ADR 41): GET /markets lists the Markets, and
  // /m/{marketId}/graphql is the BFF of the Cell of a Market. No trailing slash.
  publicApiUrl: string;
  locale: SupportedLocale;
  enableMocks: boolean;
  // Faro collector (Alloy faro.receiver); without it browser telemetry is off.
  telemetry?: Readonly<{ url: string; appVersion: string }>;
  environment: string;
  // Browser login (ADR 35); required unless the app runs on mocks.
  oidc?: OidcConfig;
}>;

type RuntimeEnv = Readonly<{
  VITE_PUBLIC_API_URL?: string;
  VITE_LOCALE?: string;
  VITE_ENABLE_MOCKS?: string;
  VITE_FARO_URL?: string;
  VITE_DEPLOYMENT_ENVIRONMENT?: string;
  VITE_APP_VERSION?: string;
  VITE_OIDC_ISSUER?: string;
  VITE_OIDC_CLIENT_ID?: string;
  VITE_OIDC_AUDIENCE?: string;
  VITE_OIDC_DEMO_EMAIL_DOMAIN?: string;
  VITE_OIDC_DEMO_PASSWORD?: string;
}>;

function parseHttpUrl(name: string, value: string): string {
  const url = new URL(value);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error(`${name} must use http or https`);
  }

  return url.toString();
}

function parsePublicUrl(value: string | undefined): string {
  if (!value) {
    throw new Error("VITE_PUBLIC_API_URL is required");
  }

  return parseHttpUrl("VITE_PUBLIC_API_URL", value).replace(/\/$/, "");
}

function required(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function parseLocale(value: string | undefined): SupportedLocale {
  const locale = required("VITE_LOCALE", value);
  if (!supportedLocales.includes(locale as SupportedLocale)) {
    throw new Error(`Unsupported VITE_LOCALE: ${locale}`);
  }
  return locale as SupportedLocale;
}

function parseTelemetry(env: RuntimeEnv): RuntimeConfig["telemetry"] {
  if (!env.VITE_FARO_URL) return undefined;
  return Object.freeze({
    url: parseHttpUrl("VITE_FARO_URL", env.VITE_FARO_URL),
    appVersion: required("VITE_APP_VERSION", env.VITE_APP_VERSION),
  });
}

export function parseRuntimeConfig(env: RuntimeEnv): RuntimeConfig {
  return Object.freeze({
    publicApiUrl: parsePublicUrl(env.VITE_PUBLIC_API_URL),
    locale: parseLocale(env.VITE_LOCALE),
    enableMocks: env.VITE_ENABLE_MOCKS === "true",
    telemetry: parseTelemetry(env),
    environment: required(
      "VITE_DEPLOYMENT_ENVIRONMENT",
      env.VITE_DEPLOYMENT_ENVIRONMENT,
    ),
    oidc: parseOidc(env),
  });
}

function parseOidc(env: RuntimeEnv): OidcConfig | undefined {
  if (!env.VITE_OIDC_ISSUER) {
    if (env.VITE_ENABLE_MOCKS === "true") return undefined;
    throw new Error("VITE_OIDC_ISSUER is required unless mocks are enabled");
  }
  return Object.freeze({
    issuer: parseHttpUrl("VITE_OIDC_ISSUER", env.VITE_OIDC_ISSUER).replace(
      /\/$/,
      "",
    ),
    clientId: required("VITE_OIDC_CLIENT_ID", env.VITE_OIDC_CLIENT_ID),
    audience: required("VITE_OIDC_AUDIENCE", env.VITE_OIDC_AUDIENCE),
    demoEmailDomain: env.VITE_OIDC_DEMO_EMAIL_DOMAIN || undefined,
    demoPassword: env.VITE_OIDC_DEMO_PASSWORD || undefined,
    redirectUri: new URL(
      "/auth/callback",
      globalThis.location?.origin,
    ).toString(),
  });
}

// A built app reads its environment from /config.js, which the deployment
// provides (ops/helm, ConfigMap): one image serves every environment. The
// dev server and tests read the Vite env.
declare global {
  var __NOMNOM_CONFIG__: RuntimeEnv | undefined;
}

export function getRuntimeConfig(): RuntimeConfig {
  if (!import.meta.env.PROD) return parseRuntimeConfig(import.meta.env);
  const deployed = globalThis.__NOMNOM_CONFIG__;
  if (deployed === undefined) {
    throw new Error("/config.js of the deployment did not set __NOMNOM_CONFIG__");
  }
  return parseRuntimeConfig(deployed);
}
