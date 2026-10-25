// Background work inside the process lifecycle: a periodic task runs next to
// the business Worker and is stopped before the Worker drains.
import type { Worker } from "./lifecycle.ts";

/** One run of a periodic task; it should stop early when `signal` aborts. */
export type PeriodicTask = (signal: AbortSignal) => Promise<void>;

export interface PeriodicLoopOptions {
  /** Delay of the first run after start. */
  readonly initialDelayMs: number;
  /** Delay between the end of one run and the start of the next. */
  readonly intervalMs: number;
  readonly log?: (message: string) => void;
}

/**
 * Runs `task` after `initialDelayMs` and then `intervalMs` after each run
 * ends, so runs never overlap. A failed run is logged and the loop continues.
 */
export class PeriodicLoop {
  readonly #task: PeriodicTask;
  readonly #options: PeriodicLoopOptions;
  readonly #stop = new AbortController();
  #timer: NodeJS.Timeout | undefined;
  #running: Promise<void> | undefined;

  constructor(task: PeriodicTask, options: PeriodicLoopOptions) {
    this.#task = task;
    this.#options = options;
  }

  start(): void {
    this.#schedule(this.#options.initialDelayMs);
  }

  /** Cancels the next run, aborts the current one and waits for it to end. */
  async stop(): Promise<void> {
    this.#stop.abort();
    clearTimeout(this.#timer);
    this.#timer = undefined;
    await this.#running;
  }

  #schedule(delayMs: number): void {
    if (this.#stop.signal.aborted) return;
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      this.#running = this.#run();
    }, delayMs);
  }

  async #run(): Promise<void> {
    try {
      await this.#task(this.#stop.signal);
    } catch (error) {
      if (!this.#stop.signal.aborted) {
        this.#options.log?.(`periodic task failed: ${describe(error)}`);
      }
    } finally {
      this.#running = undefined;
      this.#schedule(this.#options.intervalMs);
    }
  }
}

const describe = (error: unknown): string => {
  if (!(error instanceof Error)) return String(error);
  return error.cause === undefined
    ? error.message
    : `${error.message}: ${describe(error.cause)}`;
};

/**
 * Worker that runs `loops` next to `worker`: they start after the worker
 * accepted work and stop before its drain (and when it stops on its own).
 */
export class WorkerWithLoops implements Worker {
  readonly #worker: Worker;
  readonly #loops: readonly PeriodicLoop[];

  constructor(worker: Worker, loops: readonly PeriodicLoop[]) {
    this.#worker = worker;
    this.#loops = loops;
  }

  async start(): Promise<void> {
    await this.#worker.start();
    for (const loop of this.#loops) loop.start();
  }

  async run(): Promise<void> {
    try {
      await this.#worker.run();
    } finally {
      await this.#stopLoops();
    }
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    await this.#stopLoops();
    await this.#worker.shutdown(deadline);
  }

  async #stopLoops(): Promise<void> {
    await Promise.all(this.#loops.map((loop) => loop.stop()));
  }
}
