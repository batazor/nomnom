import { describe, expect, it } from "vitest";

import { cityBlocks, toGrid } from "./courier-track";

const city = {
  origin: { latitude: 52.515, longitude: 13.385 },
  blocks: 12,
  blockMeters: 150,
};

describe("virtual city", () => {
  it("projects coordinates onto the grid as delivery places them", () => {
    const cos = Math.cos((52.515 * Math.PI) / 180);
    const point = toGrid(city, {
      latitude: 52.515 + 300 / 111_320,
      longitude: 13.385 + 450 / (111_320 * cos),
    });

    expect(point.x).toBeCloseTo(3, 9);
    expect(point.y).toBeCloseTo(2, 9);
  });

  it("keeps the blocks of one channel and varies them between channels", () => {
    const first = cityBlocks(12, "channel-a");

    expect(first).toHaveLength(144);
    expect(cityBlocks(12, "channel-a")).toEqual(first);
    expect(cityBlocks(12, "channel-b")).not.toEqual(first);
    expect(new Set(first.map((block) => block.kind))).toEqual(
      new Set(["building", "park", "plaza"]),
    );
  });
});
