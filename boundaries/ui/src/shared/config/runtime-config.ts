export const supportedLocales = ["ru", "en"] as const;

export type SupportedLocale = (typeof supportedLocales)[number];

import type { OidcConfig } from "../auth/oidc";

export type RuntimeConfig = Readonly<{
  publicGraphqlUrl: string;
  locale: SupportedLocale;
  enableMocks: boolean;
  // Faro collector (Alloy faro.receiver); without it browser telemetry is off.
  telemetryUrl?: string;
  environment: string;
  // Dev token issuer of kind: access tokens of the demo users of the flow board.
  devTokensUrl?: string;
  // Browser login (ADR 35); without an issuer the sign-in buttons are hidden.
  oidc?: OidcConfig;
}>;

type RuntimeEnv = Readonly<{
  VITE_PUBLIC_GRAPHQL_URL?: string;
  VITE_LOCALE?: string;
  VITE_ENABLE_MOCKS?: string;
  VITE_FARO_URL?: string;
  VITE_DEPLOYMENT_ENVIRONMENT?: string;
  VITE_DEV_TOKENS_URL?: string;
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
    throw new Error("VITE_PUBLIC_GRAPHQL_URL is required");
  }

  return parseHttpUrl("VITE_PUBLIC_GRAPHQL_URL", value);
}

function parseLocale(value: string | undefined): SupportedLocale {
  const locale = value ?? "ru";
  if (!supportedLocales.includes(locale as SupportedLocale)) {
    throw new Error(`Unsupported VITE_LOCALE: ${locale}`);
  }
  return locale as SupportedLocale;
}

export function parseRuntimeConfig(env: RuntimeEnv): RuntimeConfig {
  return Object.freeze({
    publicGraphqlUrl: parsePublicUrl(env.VITE_PUBLIC_GRAPHQL_URL),
    locale: parseLocale(env.VITE_LOCALE),
    enableMocks: env.VITE_ENABLE_MOCKS === "true",
    telemetryUrl: env.VITE_FARO_URL
      ? parseHttpUrl("VITE_FARO_URL", env.VITE_FARO_URL)
      : undefined,
    environment: env.VITE_DEPLOYMENT_ENVIRONMENT ?? "development",
    devTokensUrl: env.VITE_DEV_TOKENS_URL
      ? parseHttpUrl("VITE_DEV_TOKENS_URL", env.VITE_DEV_TOKENS_URL)
      : undefined,
    oidc: parseOidc(env),
  });
}

function parseOidc(env: RuntimeEnv): OidcConfig | undefined {
  if (!env.VITE_OIDC_ISSUER) return undefined;
  return Object.freeze({
    issuer: parseHttpUrl("VITE_OIDC_ISSUER", env.VITE_OIDC_ISSUER).replace(
      /\/$/,
      "",
    ),
    clientId: env.VITE_OIDC_CLIENT_ID || "nomnom-ui",
    audience: env.VITE_OIDC_AUDIENCE || "nomnom-bff",
    demoEmailDomain: env.VITE_OIDC_DEMO_EMAIL_DOMAIN || undefined,
    demoPassword: env.VITE_OIDC_DEMO_PASSWORD || undefined,
    redirectUri: new URL(
      "/auth/callback",
      globalThis.location?.origin,
    ).toString(),
  });
}

export function getRuntimeConfig(): RuntimeConfig {
  return parseRuntimeConfig(import.meta.env);
}
