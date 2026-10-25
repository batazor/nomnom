import { useEffect, useState } from "react";

// The board has no polling (ADR 36): a query loads a value, its subscription
// pushes it after every change. Whichever arrived last wins: the client
// refetches active queries when a stream (re)connects, so a fresh query
// result can follow an older event.
export function useLatest<T>(
  loaded: T | undefined,
  pushed: T | undefined,
): T | undefined {
  const [value, setValue] = useState(loaded);
  useEffect(() => {
    if (loaded !== undefined) setValue(loaded);
  }, [loaded]);
  useEffect(() => {
    if (pushed !== undefined) setValue(pushed);
  }, [pushed]);
  return value ?? loaded;
}
