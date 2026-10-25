import type { Clock } from "../../application/clock.ts";

/** Deterministic clock controlled by tests. */
export class FixedClock implements Clock {
  #now: number;

  constructor(now: Date) {
    this.#now = now.getTime();
  }

  now(): Date {
    return new Date(this.#now);
  }

  set(now: Date): void {
    this.#now = now.getTime();
  }

  advance(milliseconds: number): void {
    this.#now += milliseconds;
  }
}
