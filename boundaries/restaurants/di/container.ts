// Awilix helpers of the composition roots (ADR 5). Only `cmd/` and `di/`
// modules import them; domains and application handlers never see Awilix.
import {
  type AwilixContainer,
  createContainer,
  InjectionMode,
  type Resolver,
} from "awilix";

/**
 * Resolvers for every key of the cradle `T`: a registration module that
 * annotates its registrations with it provides exactly its cradle.
 */
export type Registrations<T extends object> = {
  readonly [K in keyof T]-?: Resolver<T[K]>;
};

/**
 * Container of one process. PROXY injection hands each factory the cradle,
 * which only registration factories destructure; strict mode rejects a
 * longer-lived registration that depends on a shorter-lived one, so a
 * request or message scope resource cannot leak into the application scope.
 */
export const createProcessContainer = <
  T extends object,
>(): AwilixContainer<T> =>
  createContainer<T>({ injectionMode: InjectionMode.PROXY, strict: true });
