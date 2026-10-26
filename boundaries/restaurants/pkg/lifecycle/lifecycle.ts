// Runtime contract процесса restaurants (docs/cloud-native-runtime.md): health
// state, технический порт, обработка SIGTERM, readiness-propagation delay и
// drain. Бизнес-код от него не зависит: он получает только AbortSignal.
import {
  createServer,
  type IncomingMessage,
  type Server,
  type ServerResponse,
} from "node:http";
import { setTimeout as delay } from "node:timers/promises";

export interface LifecycleConfig {
  /** Технический порт с /health/*. */
  readonly managementPort: number;
  /** Ожидание распространения not ready до остановки приёма работы. */
  readonly readinessDelayMs: number;
  /** Deadline на завершение начатой работы. */
  readonly drainTimeoutMs: number;
}

/** Единица работы процесса, которой владеет composition root. */
export interface Worker {
  /** Захватывает ресурсы, например listener. Ошибка прерывает startup. */
  start(): Promise<void>;
  /** Обслуживает работу до shutdown и завершается после штатной остановки. */
  run(): Promise<void>;
  /** Прекращает приём новой работы и дожидается начатой до отмены deadline. */
  shutdown(deadline: AbortSignal): Promise<void>;
}

export interface RunOptions {
  /** Отмена извне, эквивалентная SIGTERM; используется в тестах. */
  readonly signal?: AbortSignal;
  /** Технический порт уже открыт: сообщает фактический адрес. */
  readonly onManagementListening?: (port: number) => void;
  readonly log?: (message: string) => void;
}

/** Уже вычисленное состояние экземпляра; handlers не обращаются к зависимостям. */
export class Health {
  #started = false;
  #shuttingDown = false;

  markStarted(): void {
    this.#started = true;
  }

  markShuttingDown(): void {
    this.#shuttingDown = true;
  }

  get ready(): boolean {
    return this.#started && !this.#shuttingDown;
  }

  readonly handle = (request: IncomingMessage, response: ServerResponse) => {
    if (request.method !== "GET") {
      return send(response, 405, { status: "method_not_allowed" });
    }
    switch (request.url) {
      case "/health/startup":
        return reply(response, this.#started, "initializing");
      // Liveness не зависит от внешних систем и остаётся успешной во время shutdown.
      case "/health/live":
        return reply(response, true, "");
      case "/health/ready":
        return this.#shuttingDown
          ? reply(response, false, "shutting_down")
          : reply(response, this.#started, "initializing");
      default:
        return send(response, 404, { status: "not_found" });
    }
  };
}

function reply(response: ServerResponse, ok: boolean, reason: string): void {
  if (ok) {
    send(response, 200, { status: "ok" });
  } else {
    send(response, 503, { status: "unavailable", reason });
  }
}

function send(response: ServerResponse, status: number, body: object): void {
  response.writeHead(status, {
    "content-type": "application/json",
    "cache-control": "no-store",
  });
  response.end(JSON.stringify(body));
}

const SHUTDOWN_SIGNALS = ["SIGTERM", "SIGINT"] as const;

export class Lifecycle {
  readonly health = new Health();
  readonly #config: LifecycleConfig;

  constructor(config: LifecycleConfig) {
    this.#config = config;
  }

  /**
   * Выполняет lifecycle: технический порт → start → ready → SIGTERM/SIGINT →
   * not ready → readiness delay → shutdown → exit. Resolve означает штатный
   * shutdown, reject — non-zero exit.
   */
  async run(worker: Worker, options: RunOptions = {}): Promise<void> {
    const log = options.log ?? (() => {});
    const stopRequested = new AbortController();
    const onSignal = () => {
      // Повторный сигнал больше не перехватывается и завершает процесс сразу.
      removeSignalHandlers();
      stopRequested.abort();
    };
    const removeSignalHandlers = () => {
      for (const name of SHUTDOWN_SIGNALS) process.off(name, onSignal);
    };
    for (const name of SHUTDOWN_SIGNALS) process.once(name, onSignal);
    options.signal?.addEventListener("abort", onSignal, { once: true });

    const management = createServer(this.health.handle);
    try {
      const port = await listen(management, this.#config.managementPort);
      log(`management listening on :${port}`);
      options.onManagementListening?.(port);

      await worker.start();
      const running = settle(worker.run());
      this.health.markStarted();

      if (await firstOf(running, aborted(stopRequested.signal))) {
        this.health.markShuttingDown();
        throw stoppedUnexpectedly(await running);
      }
      this.health.markShuttingDown();
      log(
        `shutting down: readiness delay ${this.#config.readinessDelayMs}ms, drain timeout ${this.#config.drainTimeoutMs}ms`,
      );

      if (await firstOf(running, delay(this.#config.readinessDelayMs))) {
        throw stoppedUnexpectedly(await running);
      }
      await worker.shutdown(AbortSignal.timeout(this.#config.drainTimeoutMs));
      const outcome = await running;
      if (outcome.error !== undefined) throw outcome.error;
    } finally {
      removeSignalHandlers();
      options.signal?.removeEventListener("abort", onSignal);
      // Технический порт закрывается последним: до этого probes видят live=200.
      management.closeAllConnections();
      management.close();
    }
  }
}

/** HTTP-сервер бизнес-порта как Worker. */
export class HttpWorker implements Worker {
  readonly #server: Server;
  readonly #port: number;
  #draining = false;

  constructor(server: Server, port: number) {
    this.#server = server;
    this.#port = port;
    // Во время drain keep-alive соединения закрываются после текущего ответа.
    server.prependListener("request", (_request, response: ServerResponse) => {
      if (this.#draining) response.setHeader("connection", "close");
    });
  }

  get port(): number {
    const address = this.#server.address();
    return typeof address === "object" && address !== null
      ? address.port
      : this.#port;
  }

  async start(): Promise<void> {
    await listen(this.#server, this.#port);
  }

  run(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.#server.once("close", resolve);
      this.#server.once("error", reject);
    });
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    this.#draining = true;
    const closed = new Promise<void>((resolve, reject) => {
      this.#server.close((error) => (error ? reject(error) : resolve()));
    });
    this.#server.closeIdleConnections();
    try {
      await firstOf(closed, aborted(deadline));
    } finally {
      if (deadline.aborted) this.#server.closeAllConnections();
    }
    if (deadline.aborted) {
      throw new Error("drain timeout exceeded, in-flight connections closed");
    }
  }
}

function listen(server: Server, port: number): Promise<number> {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, () => {
      server.off("error", reject);
      const address = server.address();
      resolve(
        typeof address === "object" && address !== null ? address.port : port,
      );
    });
  });
}

interface Outcome {
  readonly error?: unknown;
}

function settle(promise: Promise<void>): Promise<Outcome> {
  return promise.then(
    () => ({}),
    (error: unknown) => ({ error: error ?? new Error("unknown error") }),
  );
}

/** true, если первым завершился first. */
async function firstOf(first: Promise<unknown>, second: Promise<unknown>) {
  return Promise.race([first.then(() => true), second.then(() => false)]);
}

function aborted(signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) =>
    signal.addEventListener("abort", () => resolve(), { once: true }),
  );
}

function stoppedUnexpectedly(outcome: Outcome): Error {
  return new Error("worker stopped before shutdown", {
    cause: outcome.error ?? new Error("no error reported"),
  });
}

/**
 * Workers of one process that serve side by side, e.g. two consumer groups:
 * all start before the process is ready, a failure of one stops the process,
 * and shutdown drains all of them within the same deadline.
 */
export class ParallelWorkers implements Worker {
  readonly #workers: readonly Worker[];

  constructor(workers: readonly Worker[]) {
    this.#workers = workers;
  }

  async start(): Promise<void> {
    for (const worker of this.#workers) await worker.start();
  }

  async run(): Promise<void> {
    await Promise.all(this.#workers.map((worker) => worker.run()));
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    await Promise.all(this.#workers.map((worker) => worker.shutdown(deadline)));
  }
}
