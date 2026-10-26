import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from "react";
import { useMutation, useQuery, useSubscription } from "urql";

import {
  AddCourierDocument,
  BoardCourierPoolChangedDocument,
  BoardCourierPoolDocument,
  type CourierPoolProblemCode,
  type CourierPoolViewFragment,
  RetireCourierDocument,
  SetCourierOnLineDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import {
  PoolAddForm,
  PoolLoad,
  type PoolMember,
  PoolMembers,
} from "./pool-panel";
import { useLatest } from "./use-latest";

// Courier Pool of the platform on the flow board (FR-623, FR-627): one source
// reads it — courierPool, then courierPoolChanged (ADR 36, no polling) — and
// shares it with the courier column header and the kitchen cards that name
// the courier of an order. null: not loaded, or the user is no dispatcher
// (PermissionDenied), so no pool is shown.

const CourierPoolContext = createContext<CourierPoolViewFragment | null>(null);

export function useCourierPool(): CourierPoolViewFragment | null {
  return useContext(CourierPoolContext);
}

export function CourierPoolProvider({
  pool,
  children,
}: {
  pool: CourierPoolViewFragment | null;
  children: ReactNode;
}) {
  return (
    <CourierPoolContext.Provider value={pool}>
      {children}
    </CourierPoolContext.Provider>
  );
}

// Renders nothing: it runs under the courier role and hands the pool up.
export function CourierPoolSource({
  onPool,
}: {
  onPool: (pool: CourierPoolViewFragment | null) => void;
}) {
  const [{ data, error }] = useQuery({ query: BoardCourierPoolDocument });
  const permitted = Boolean(data?.courierPool) && !error;
  const [{ data: pushed }] = useSubscription({
    query: BoardCourierPoolChangedDocument,
    pause: !permitted,
  });
  const pool = useLatest(
    permitted ? data?.courierPool : undefined,
    pushed?.courierPoolChanged,
  );
  const current = permitted ? (pool ?? null) : null;
  useEffect(() => onPool(current), [current, onPool]);
  return null;
}

// Name of the courier delivering or offered an order; null when the pool is
// hidden or nobody has the order.
export function useCourierOf(
  orderId: string,
): Readonly<{ name: string; delivering: boolean }> | null {
  const pool = useCourierPool();
  if (!pool) return null;
  const delivering = pool.couriers.find(
    (courier) => courier.currentOrderId === orderId,
  );
  if (delivering) return { name: delivering.name, delivering: true };
  const offered = pool.couriers.find(
    (courier) => courier.offeredOrderId === orderId,
  );
  return offered ? { name: offered.name, delivering: false } : null;
}

const problemMessage: Record<CourierPoolProblemCode, MessageKey> = {
  COURIER_NAME_INVALID: "board.couriers.problem.COURIER_NAME_INVALID",
  COURIER_STATUS_CONFLICT: "board.couriers.problem.COURIER_STATUS_CONFLICT",
  COURIER_ON_LINE: "board.couriers.problem.COURIER_ON_LINE",
};

type Outcome = Readonly<{
  error?: unknown;
  problem?: CourierPoolProblemCode | null;
}>;

// Courier Pool in the courier column header: the line, the load and the
// deliveries waiting for a courier, the couriers with their line actions and
// adding one by name. Hidden for users who are no dispatcher (AC-798).
export function CourierPoolPanel() {
  const { t } = useI18n();
  const pool = useCourierPool();
  const [adding, add] = useMutation(AddCourierDocument);
  const [lining, setOnLine] = useMutation(SetCourierOnLineDocument);
  const [retiring, retire] = useMutation(RetireCourierDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);
  if (!pool) return null;

  function settle(outcome: Outcome): boolean {
    if (outcome.error) setProblem("board.actionFailed");
    else if (outcome.problem) setProblem(problemMessage[outcome.problem]);
    else setProblem(null);
    return !outcome.error && !outcome.problem;
  }

  const members: PoolMember[] = pool.couriers.map((courier) => ({
    id: courier.id,
    name: courier.name,
    onLine: courier.availability !== "UNAVAILABLE",
    state:
      courier.availability === "BUSY"
        ? "working"
        : courier.availability === "AVAILABLE"
          ? "free"
          : "offline",
    doing:
      courier.availability === "BUSY"
        ? t("board.couriers.delivering")
        : courier.availability === "AVAILABLE"
          ? courier.offeredOrderId
            ? t("board.couriers.offered")
            : t("board.couriers.free")
          : t("board.pool.offLine"),
  }));

  return (
    <section aria-label={t("board.couriers.title")} className="board-pool">
      <PoolLoad
        backlog={t("board.couriers.waiting", { count: pool.waiting })}
        busy={pool.busy}
        label={t("board.couriers.title")}
        onLine={pool.onLine}
        size={pool.size}
        warning={
          pool.onLine === 0 && pool.waiting > 0
            ? t("board.couriers.noLine")
            : null
        }
      />
      <PoolMembers
        label={t("board.couriers.members")}
        members={members}
        onLine={async (member, onLine) => {
          const result = await setOnLine({ courierId: member.id, onLine });
          settle({
            error: result.error,
            problem: result.data?.setCourierOnLine.problem,
          });
        }}
        onRetire={async (member) => {
          const result = await retire({ courierId: member.id });
          settle({
            error: result.error,
            problem: result.data?.retireCourier.problem,
          });
        }}
        pending={lining.fetching || retiring.fetching}
      />
      <PoolAddForm
        label={t("board.couriers.name")}
        onAdd={async (name) => {
          const result = await add({ name });
          return settle({
            error: result.error,
            problem: result.data?.addCourier.problem,
          });
        }}
        pending={adding.fetching}
        submit={t("board.couriers.add")}
      />
      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </section>
  );
}
