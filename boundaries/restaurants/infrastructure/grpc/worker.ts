import type { Http2Server, ServerHttp2Session } from "node:http2";

import type { Worker } from "../../pkg/lifecycle/lifecycle.ts";

/** HTTP/2 gRPC server integrated with the shared process lifecycle. */
export class GrpcWorker implements Worker {
  readonly #server: Http2Server;
  readonly #port: number;
  readonly #sessions = new Set<ServerHttp2Session>();

  constructor(server: Http2Server, port: number) {
    this.#server = server;
    this.#port = port;
    server.on("session", (session) => {
      this.#sessions.add(session);
      session.once("close", () => this.#sessions.delete(session));
    });
  }

  get port(): number {
    const address = this.#server.address();
    return typeof address === "object" && address !== null
      ? address.port
      : this.#port;
  }

  start(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.#server.once("error", reject);
      this.#server.listen(this.#port, () => {
        this.#server.off("error", reject);
        resolve();
      });
    });
  }

  run(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.#server.once("close", resolve);
      this.#server.once("error", reject);
    });
  }

  async shutdown(deadline: AbortSignal): Promise<void> {
    const closed = new Promise<void>((resolve, reject) => {
      this.#server.close((error) => (error ? reject(error) : resolve()));
    });
    for (const session of this.#sessions) session.close();

    const timedOut = await Promise.race([
      closed.then(() => false),
      aborted(deadline).then(() => true),
    ]);
    if (!timedOut) return;

    for (const session of this.#sessions) session.destroy();
    throw new Error("drain timeout exceeded, in-flight gRPC sessions closed");
  }
}

function aborted(signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) =>
    signal.addEventListener("abort", () => resolve(), { once: true }),
  );
}
