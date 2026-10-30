// Use case telemetry of the composition roots (docs/observability.md, "Use
// cases"): after the slices registered their handlers, the process replaces
// each registration its slices list with the same handler decorated by
// UseCaseTelemetry (pkg/telemetry/usecase.ts). Slices own the lists in their
// di; use cases stay unaware of OpenTelemetry. Names are the stable snake_case
// names of the application folders.
import { type AwilixContainer, asFunction } from "awilix";

import type { UseCase, UseCaseTelemetry } from "../pkg/telemetry/usecase.ts";

type Executable = { execute(input: never): Promise<unknown> };

/**
 * Decorates the registered handlers `useCases` of `container` with the
 * `useCaseTelemetry` registration; the lifetime stays singleton.
 */
export function instrumentUseCases<
  C extends { readonly useCaseTelemetry: UseCaseTelemetry },
>(
  container: AwilixContainer<C>,
  useCases: Readonly<Partial<Record<keyof C, UseCase>>>,
): void {
  for (const [key, useCase] of Object.entries(useCases) as [
    keyof C & string,
    UseCase,
  ][]) {
    const original = container.registrations[key];
    if (original === undefined) {
      throw new Error(`use case ${key} is not registered`);
    }
    container.register(
      key,
      asFunction(({ useCaseTelemetry }: C) =>
        useCaseTelemetry.instrument(
          useCase,
          original.resolve(container) as Executable,
        ),
      ).singleton(),
    );
  }
}
