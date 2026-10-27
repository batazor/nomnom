// Mock of the live courier tracking (FR-663, FR-668): orders with an
// assigned courier carry a fixed Virtual City, pickup and dropoff, and
// courierMoved walks the courier along the streets like the delivery
// Simulator. Development-only: no business rules live here.

import { fromGrid, type GridPoint } from "../../virtual-city";

export type MockGeoPoint = { latitude: number; longitude: number };
export type MockCourierPosition = MockGeoPoint & {
  channel: string;
  recordedAt: string;
};
export type MockTracking = {
  channel: string;
  city: { origin: MockGeoPoint; blocks: number; blockMeters: number };
  pickup: MockGeoPoint;
  dropoff: MockGeoPoint;
  courierPosition: MockCourierPosition | null;
};

type Cell = GridPoint;

const city = {
  origin: { latitude: 52.515, longitude: 13.385 },
  blocks: 12,
  blockMeters: 150,
};
const courierStart: Cell = { x: 0, y: 1 };
const pickupCell: Cell = { x: 3, y: 4 };
const dropoffCell: Cell = { x: 9, y: 8 };
// Points per block and between points, like SIMULATOR__TRACK_INTERVAL.
const pointsPerBlock = 4;
export const courierStepMs = 1_000;

function point(cell: Cell): MockGeoPoint {
  return { ...fromGrid(city, cell) };
}

export function trackingChannel(orderId: string): string {
  return `mock-track-${orderId}`;
}

// Tracking of an order whose courier heads to the restaurant or, picked up,
// to the customer.
export function mockTracking(
  orderId: string,
  pickedUp: boolean,
  now: Date,
): MockTracking {
  return {
    channel: trackingChannel(orderId),
    city,
    pickup: point(pickupCell),
    dropoff: point(dropoffCell),
    courierPosition: {
      ...point(pickedUp ? pickupCell : courierStart),
      channel: trackingChannel(orderId),
      recordedAt: now.toISOString(),
    },
  };
}

// Cells of the walk: horizontally first, then vertically.
function walk(from: Cell, to: Cell): Cell[] {
  const cells: Cell[] = [];
  const steps = Math.abs(to.x - from.x) * pointsPerBlock;
  for (let index = 1; index <= steps; index += 1) {
    cells.push({
      x: from.x + (Math.sign(to.x - from.x) * index) / pointsPerBlock,
      y: from.y,
    });
  }
  const rise = Math.abs(to.y - from.y) * pointsPerBlock;
  for (let index = 1; index <= rise; index += 1) {
    cells.push({
      x: to.x,
      y: from.y + (Math.sign(to.y - from.y) * index) / pointsPerBlock,
    });
  }
  return cells;
}

type Iteration<T> = IteratorResult<T, undefined>;

// courierMoved of one channel: a point every courierStepMs until the
// courier arrives. An unknown channel never yields, like the Router.
export function courierMoves(
  channel: string,
  pickedUp: boolean | null,
  now: () => Date,
): AsyncIterableIterator<{ courierMoved: MockCourierPosition }> {
  const route =
    pickedUp === null
      ? []
      : pickedUp
        ? walk(pickupCell, dropoffCell)
        : walk(courierStart, pickupCell);
  let next = 0;
  let closed = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let wake: (() => void) | undefined;

  return {
    async next(): Promise<Iteration<{ courierMoved: MockCourierPosition }>> {
      while (!closed && next >= route.length) {
        await new Promise<void>((resolve) => {
          wake = resolve;
        });
      }
      if (closed) return { value: undefined, done: true };
      await new Promise<void>((resolve) => {
        wake = resolve;
        timer = setTimeout(resolve, courierStepMs);
      });
      if (closed) return { value: undefined, done: true };
      const cell = route[next];
      next += 1;
      return {
        value: {
          courierMoved: {
            ...point(cell),
            channel,
            recordedAt: now().toISOString(),
          },
        },
        done: false,
      };
    },
    async return(): Promise<Iteration<{ courierMoved: MockCourierPosition }>> {
      closed = true;
      clearTimeout(timer);
      wake?.();
      return { value: undefined, done: true };
    },
    [Symbol.asyncIterator]() {
      return this;
    },
  };
}
