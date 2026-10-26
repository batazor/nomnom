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
  PoolLoad,
  type PoolMember,
  PoolMembers,
  PoolSize,
  removableMember,
} from "./pool-panel";
import { useLatest } from "./use-latest";
import { newWorkerName } from "./worker-names";

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
  // The pool id is its Market (ADR 41): the subscription follows that pool.
  const marketId = data?.courierPool.id ?? "";
  const [{ data: pushed }] = useSubscription({
    query: BoardCourierPoolChangedDocument,
    variables: { marketId },
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
// − and + for the pool. Hidden for users who are no dispatcher (AC-798).
export function CourierPoolPanel() {
  const { t, locale } = useI18n();
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

  async function setLine(courierId: string, onLine: boolean): Promise<boolean> {
    const result = await setOnLine({ courierId, onLine });
    return settle({
      error: result.error,
      problem: result.data?.setCourierOnLine.problem,
    });
  }

  // − takes the courier off the line first: only an off-line courier leaves.
  async function remove(member: PoolMember) {
    if (member.onLine && !(await setLine(member.id, false))) return;
    const result = await retire({ courierId: member.id });
    settle({
      error: result.error,
      problem: result.data?.retireCourier.problem,
    });
  }

  const couriers = pool.couriers;
  const removable = removableMember(members);
  const pending = adding.fetching || lining.fetching || retiring.fetching;

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
        actions={
          <PoolSize
            addLabel={t("board.couriers.add")}
            canRemove={removable !== undefined}
            onAdd={async () => {
              const name = newWorkerName(
                locale,
                couriers.map((courier) => courier.name),
              );
              const result = await add({ name });
              settle({
                error: result.error,
                problem: result.data?.addCourier.problem,
              });
            }}
            onRemove={() => {
              if (removable) void remove(removable);
            }}
            pending={pending}
            removeLabel={t("board.couriers.retireOne")}
          />
        }
      />
      <PoolMembers
        label={t("board.couriers.members")}
        members={members}
        onLine={(member, onLine) => void setLine(member.id, onLine)}
        pending={pending}
      />
      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </section>
  );
}
