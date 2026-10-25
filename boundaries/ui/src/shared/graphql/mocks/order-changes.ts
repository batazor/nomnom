// Mock of the Cosmo Streams subscription orderChanged (ADR 36): like the
// Router, a subscriber gets the current order after each change and nothing
// on subscribe.

type Iteration<T> = IteratorResult<T, undefined>;

export function createChangeFeed() {
  const listeners = new Set<() => void>();

  return {
    // Wakes every subscriber; each re-reads its order and skips non-changes.
    changed() {
      for (const listener of listeners) listener();
    },

    // An explicit iterator instead of an async generator: return() of a
    // generator suspended on await waits for the next change, so an
    // unsubscribed stream would keep its listener. Unlike the Router, the
    // stream completes after a value for which isLast holds: the client then
    // has no open response to abort, and Node fetch under MSW fails on an
    // abort of a streamed response.
    subscribe<T>(
      read: () => T,
      {
        isLast = () => false,
        onClose = () => {},
      }: { isLast?: (value: T) => boolean; onClose?: () => void } = {},
    ): AsyncIterableIterator<T> {
      let last = JSON.stringify(read());
      let wake: (() => void) | undefined;
      let closed = false;
      const listener = () => wake?.();
      listeners.add(listener);

      function close() {
        if (closed) return;
        closed = true;
        listeners.delete(listener);
        onClose();
        wake?.();
      }

      return {
        async next(): Promise<Iteration<T>> {
          while (!closed) {
            const value = read();
            const current = JSON.stringify(value);
            if (current !== last) {
              last = current;
              if (isLast(value)) close();
              return { value, done: false };
            }
            await new Promise<void>((resolve) => {
              wake = resolve;
            });
          }
          return { value: undefined, done: true };
        },
        async return(): Promise<Iteration<T>> {
          close();
          return { value: undefined, done: true };
        },
        [Symbol.asyncIterator]() {
          return this;
        },
      };
    },
  };
}
