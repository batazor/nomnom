export const supportedLocales = ["ru", "en"] as const;

export type SupportedLocale = (typeof supportedLocales)[number];

export type RuntimeConfig = Readonly<{
  publicGraphqlUrl: string;
  locale: SupportedLocale;
  enableMocks: boolean;
}>;

type RuntimeEnv = Readonly<{
  VITE_PUBLIC_GRAPHQL_URL?: string;
  VITE_LOCALE?: string;
  VITE_ENABLE_MOCKS?: string;
}>;

function parsePublicUrl(value: string | undefined): string {
  if (!value) {
    throw new Error("VITE_PUBLIC_GRAPHQL_URL is required");
  }

  const url = new URL(value);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("VITE_PUBLIC_GRAPHQL_URL must use http or https");
  }

  return url.toString();
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
  });
}

export function getRuntimeConfig(): RuntimeConfig {
  return parseRuntimeConfig(import.meta.env);
}
