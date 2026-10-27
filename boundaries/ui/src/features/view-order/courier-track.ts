import { useEffect, useState } from "react";
import { useSubscription } from "urql";

import {
  CourierMovedDocument,
  type TrackedPositionFragment,
} from "../../shared/graphql/generated/graphql";
import type { ViewedOrder } from "./use-order-updates";

export type OrderTracking = NonNullable<ViewedOrder["tracking"]>;
export type TrackedPosition = TrackedPositionFragment;
type City = OrderTracking["city"];
type GeoPoint = City["origin"];

// A point of the Virtual City grid: x blocks east, y blocks north of the
// south-west corner (FR-658).
export type GridPoint = { x: number; y: number };

// Meters of one degree of latitude; one degree of longitude is this times
// cos(latitude), as delivery computes it.
const metersPerDegree = 111_320;

export function toGrid(city: City, point: GeoPoint): GridPoint {
  const cos = Math.cos((city.origin.latitude * Math.PI) / 180);
  return {
    x:
      ((point.longitude - city.origin.longitude) * metersPerDegree * cos) /
      city.blockMeters,
    y:
      ((point.latitude - city.origin.latitude) * metersPerDegree) /
      city.blockMeters,
  };
}

// Kinds of the blocks between the streets: a decoration of the map, seeded
// by the tracking channel so one order keeps its city between renders.
export type BlockKind = "building" | "park" | "plaza";

export function cityBlocks(
  blocks: number,
  seed: string,
): ReadonlyArray<{ x: number; y: number; kind: BlockKind }> {
  const random = seededRandom(seed);
  const result: { x: number; y: number; kind: BlockKind }[] = [];
  for (let y = 0; y < blocks; y += 1) {
    for (let x = 0; x < blocks; x += 1) {
      const roll = random();
      result.push({
        x,
        y,
        kind: roll < 0.14 ? "park" : roll < 0.24 ? "plaza" : "building",
      });
    }
  }
  return result;
}

// mulberry32 over a 32-bit FNV-1a hash of the seed.
function seededRandom(seed: string): () => number {
  let state = 0x811c9dc5;
  for (let index = 0; index < seed.length; index += 1) {
    state = Math.imul(state ^ seed.charCodeAt(index), 0x01000193);
  }
  return () => {
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4_294_967_296;
  };
}

// Positions of the courier on the map: the stored one of the query, then
// every courierMoved point (FR-669). A query answer with another stored
// position — after each (re)connect of the stream — starts the trail again
// from it; an orderChanged push with the same one keeps the trail.
// The map mounts only with tracking, so the subscription closes with it.
export function useCourierTrack(
  tracking: OrderTracking,
): readonly TrackedPosition[] {
  const start = tracking.courierPosition ?? null;
  const startKey = [
    tracking.channel,
    start?.recordedAt,
    start?.latitude,
    start?.longitude,
  ].join("|");
  const [trail, setTrail] = useState<readonly TrackedPosition[]>(() =>
    start ? [start] : [],
  );

  // biome-ignore lint/correctness/useExhaustiveDependencies: startKey stands for start by value
  useEffect(() => {
    setTrail(start ? [start] : []);
  }, [startKey]);

  const [moved] = useSubscription({
    query: CourierMovedDocument,
    variables: { channel: tracking.channel },
  });

  useEffect(() => {
    const position = moved.data?.courierMoved;
    if (position) setTrail((current) => [...current, position]);
  }, [moved.data]);

  return trail;
}
