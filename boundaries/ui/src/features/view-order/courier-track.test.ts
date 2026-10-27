import { describe, expect, it } from "vitest";

import { cityBlocks } from "./courier-track";

describe("city blocks", () => {
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
