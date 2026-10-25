import { describe, expect, it } from "vitest";

import { parseRuntimeConfig } from "./runtime-config";

describe("parseRuntimeConfig", () => {
  it("accepts a public HTTP endpoint and explicit locale", () => {
    expect(
      parseRuntimeConfig({
        VITE_PUBLIC_GRAPHQL_URL: "https://api.example.test/graphql",
        VITE_LOCALE: "en",
        VITE_ENABLE_MOCKS: "true",
      }),
    ).toEqual({
      publicGraphqlUrl: "https://api.example.test/graphql",
      locale: "en",
      enableMocks: true,
      telemetryUrl: undefined,
      environment: "development",
      devTokensUrl: undefined,
      oidc: undefined,
    });
  });

  it("enables browser telemetry only with a collector URL", () => {
    const config = parseRuntimeConfig({
      VITE_PUBLIC_GRAPHQL_URL: "https://api.example.test/graphql",
      VITE_FARO_URL: "https://telemetry.example.test/collect",
      VITE_DEPLOYMENT_ENVIRONMENT: "kind",
    });
    expect(config.telemetryUrl).toBe("https://telemetry.example.test/collect");
    expect(config.environment).toBe("kind");
    expect(() =>
      parseRuntimeConfig({
        VITE_PUBLIC_GRAPHQL_URL: "https://api.example.test/graphql",
        VITE_FARO_URL: "ftp://telemetry.example.test",
      }),
    ).toThrow("VITE_FARO_URL must use http or https");
  });

  it("fails fast when the public endpoint is missing", () => {
    expect(() => parseRuntimeConfig({})).toThrow(
      "VITE_PUBLIC_GRAPHQL_URL is required",
    );
  });

  it("rejects non-HTTP protocols", () => {
    expect(() =>
      parseRuntimeConfig({
        VITE_PUBLIC_GRAPHQL_URL: "grpc://orders.internal:9000",
      }),
    ).toThrow("must use http or https");
  });
});
