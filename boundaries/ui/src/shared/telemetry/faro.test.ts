import type { TransportItem } from "@grafana/faro-react";
import { describe, expect, it } from "vitest";

import { initTelemetry, redactItem, redactUrl } from "./faro";

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
        telemetryUrl: "https://telemetry.test/collect",
      }),
    ).toBeUndefined();
  });
});
