import type { TransportItem } from "@grafana/faro-react";
import { describe, expect, it } from "vitest";

import { bffUrlPattern, initTelemetry, redactItem, redactUrl } from "./faro";

describe("redactUrl", () => {
  it("keeps the route shape without resource ids and query", () => {
    expect(redactUrl("https://app.test/orders/order-42?x=1#top")).toBe(
      "https://app.test/orders/:orderId",
    );
    expect(redactUrl("https://app.test/restaurants/r-1")).toBe(
      "https://app.test/restaurants/:restaurantId",
    );
    expect(redactUrl("https://app.test/manage/restaurants/r-1")).toBe(
      "https://app.test/manage/restaurants/:restaurantId",
    );
    expect(redactUrl("https://app.test/")).toBe("https://app.test/");
  });

  it("redacts the page URL of every transport item", () => {
    const item = {
      type: "event",
      payload: {},
      meta: { page: { url: "https://app.test/orders/order-42" } },
    } as unknown as TransportItem;
    expect(redactItem(item).meta.page?.url).toBe(
      "https://app.test/orders/:orderId",
    );
  });
});

describe("initTelemetry", () => {
  const base = {
    publicGraphqlUrl: "https://api.test/graphql",
    locale: "ru",
    environment: "test",
  } as const;

  it("stays off without a collector URL or with mocks", () => {
    expect(initTelemetry({ ...base, enableMocks: false })).toBeUndefined();
    expect(
      initTelemetry({
        ...base,
        enableMocks: true,
        telemetry: {
          url: "https://telemetry.test/collect",
          appVersion: "1.0.0",
        },
      }),
    ).toBeUndefined();
  });
});

describe("bffUrlPattern", () => {
  it("matches every request to the BFF origin and nothing else", () => {
    const pattern = bffUrlPattern("http://localhost:3010/graphql");
    expect(pattern.test("http://localhost:3010/graphql?query=x")).toBe(true);
    expect(pattern.test("http://localhost:3010/graphql")).toBe(true);
    expect(pattern.test("http://localhost:30100/graphql")).toBe(false);
    expect(pattern.test("http://localhostX3010/graphql")).toBe(false);
    expect(pattern.test("http://localhost:12347/collect")).toBe(false);
  });
});
