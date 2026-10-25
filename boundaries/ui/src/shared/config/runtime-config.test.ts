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
    });
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
