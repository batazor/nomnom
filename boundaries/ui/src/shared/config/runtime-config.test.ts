import { describe, expect, it } from "vitest";

import { parseRuntimeConfig } from "./runtime-config";

const required = {
  VITE_PUBLIC_API_URL: "https://api.example.test/",
  VITE_LOCALE: "en",
  VITE_DEPLOYMENT_ENVIRONMENT: "development",
} as const;

const oidc = {
  VITE_OIDC_ISSUER: "https://login.example.test/dex/",
  VITE_OIDC_CLIENT_ID: "nomnom-ui",
  VITE_OIDC_AUDIENCE: "nomnom-bff",
} as const;

describe("parseRuntimeConfig", () => {
  it("accepts a public HTTP endpoint and explicit locale", () => {
    expect(
      parseRuntimeConfig({ ...required, VITE_ENABLE_MOCKS: "true" }),
    ).toEqual({
      publicApiUrl: "https://api.example.test",
      locale: "en",
      enableMocks: true,
      telemetry: undefined,
      environment: "development",
      oidc: undefined,
    });
  });

  it("fails fast without locale or deployment environment", () => {
    const { VITE_LOCALE: _locale, ...withoutLocale } = required;
    expect(() =>
      parseRuntimeConfig({ ...withoutLocale, VITE_ENABLE_MOCKS: "true" }),
    ).toThrow("VITE_LOCALE is required");
    const { VITE_DEPLOYMENT_ENVIRONMENT: _environment, ...withoutEnvironment } =
      required;
    expect(() =>
      parseRuntimeConfig({ ...withoutEnvironment, VITE_ENABLE_MOCKS: "true" }),
    ).toThrow("VITE_DEPLOYMENT_ENVIRONMENT is required");
  });

  it("enables browser telemetry only with a collector URL and app version", () => {
    const config = parseRuntimeConfig({
      ...required,
      ...oidc,
      VITE_FARO_URL: "https://telemetry.example.test/collect",
      VITE_APP_VERSION: "1.2.3",
    });
    expect(config.telemetry).toEqual({
      url: "https://telemetry.example.test/collect",
      appVersion: "1.2.3",
    });
    expect(() =>
      parseRuntimeConfig({
        ...required,
        ...oidc,
        VITE_FARO_URL: "https://telemetry.example.test/collect",
      }),
    ).toThrow("VITE_APP_VERSION is required");
    expect(() =>
      parseRuntimeConfig({
        ...required,
        ...oidc,
        VITE_FARO_URL: "ftp://telemetry.example.test",
        VITE_APP_VERSION: "1.2.3",
      }),
    ).toThrow("VITE_FARO_URL must use http or https");
  });

  it("fails fast when the public endpoint is missing", () => {
    expect(() => parseRuntimeConfig({})).toThrow(
      "VITE_PUBLIC_API_URL is required",
    );
  });

  it("fails fast without a login issuer unless mocks are on", () => {
    expect(() => parseRuntimeConfig(required)).toThrow(
      "VITE_OIDC_ISSUER is required unless mocks are enabled",
    );
    expect(parseRuntimeConfig({ ...required, ...oidc }).oidc?.issuer).toBe(
      "https://login.example.test/dex",
    );
  });

  it("requires the OIDC client and audience with an issuer", () => {
    const { VITE_OIDC_CLIENT_ID: _client, ...withoutClient } = oidc;
    expect(() => parseRuntimeConfig({ ...required, ...withoutClient })).toThrow(
      "VITE_OIDC_CLIENT_ID is required",
    );
    const { VITE_OIDC_AUDIENCE: _audience, ...withoutAudience } = oidc;
    expect(() =>
      parseRuntimeConfig({ ...required, ...withoutAudience }),
    ).toThrow("VITE_OIDC_AUDIENCE is required");
  });

  it("rejects non-HTTP protocols", () => {
    expect(() =>
      parseRuntimeConfig({
        VITE_PUBLIC_API_URL: "grpc://orders.internal:9000",
      }),
    ).toThrow("must use http or https");
  });
});
