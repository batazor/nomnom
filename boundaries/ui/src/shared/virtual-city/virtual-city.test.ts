import { describe, expect, it } from "vitest";

import { fromGrid, toGrid } from "./virtual-city";

const city = {
  origin: { latitude: 52.515, longitude: 13.385 },
  blocks: 12,
  blockMeters: 150,
};

describe("virtual city projection", () => {
  it("places intersections as delivery does", () => {
    const cos = Math.cos((52.515 * Math.PI) / 180);
    const point = fromGrid(city, { x: 3, y: 2 });

    expect(point.latitude).toBeCloseTo(52.515 + 300 / 111_320, 12);
    expect(point.longitude).toBeCloseTo(13.385 + 450 / (111_320 * cos), 12);
  });

  it("projects back onto the grid", () => {
    const grid = toGrid(city, fromGrid(city, { x: 7.25, y: 4.5 }));

    expect(grid.x).toBeCloseTo(7.25, 9);
    expect(grid.y).toBeCloseTo(4.5, 9);
  });
});
