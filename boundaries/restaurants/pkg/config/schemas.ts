import { z } from "zod";

export const portSchema = z.coerce.number().int().min(1).max(65535);

const DURATION_UNITS_MS = {
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
  d: 86_400_000,
} as const;

/** Длительность вида `500ms`, `5s`, `1m`, `2h`, `7d` в миллисекундах. */
export const durationSchema = z
  .string()
  .regex(
    /^\d+(ms|s|m|h|d)$/,
    "expected a duration like 500ms, 5s, 1m, 2h or 7d",
  )
  .transform((value) => {
    const [, amount, unit] = /^(\d+)(ms|s|m|h|d)$/.exec(value) ?? [];
    return (
      Number(amount) * DURATION_UNITS_MS[unit as keyof typeof DURATION_UNITS_MS]
    );
  });

export const booleanSchema = z
  .enum(["true", "false"], { error: "expected true or false" })
  .transform((value) => value === "true");
