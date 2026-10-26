import { useState } from "react";
import { useMutation } from "urql";

import {
  type CookPoolProblemCode,
  type CookPoolViewFragment,
  HireCookDocument,
  RetireCookDocument,
  SetCookOnLineDocument,
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
import { newWorkerName } from "./worker-names";

const problemMessage: Record<CookPoolProblemCode, MessageKey> = {
  COOK_NAME_INVALID: "board.cooks.problem.COOK_NAME_INVALID",
  COOK_BUSY: "board.cooks.problem.COOK_BUSY",
  COOK_ON_LINE: "board.cooks.problem.COOK_ON_LINE",
  COOK_RETIRED: "board.cooks.problem.COOK_RETIRED",
};

type Outcome = Readonly<{
  error?: unknown;
  problem?: CookPoolProblemCode | null;
}>;

// Cook Pool of the kitchen in its column header (FR-624): the line, the load
// and the queue, the cooks with their line action, − and + for the pool. The
// pool comes with kitchenChanged, so the panel only sends commands.
export function CookPoolPanel({
  restaurantId,
  pool,
  orderTimes,
}: {
  restaurantId: string;
  pool: CookPoolViewFragment;
  // Receipt time of each kitchen order, to say which one a cook prepares.
  orderTimes: ReadonlyMap<string, string>;
}) {
  const { t, locale } = useI18n();
  const [hiring, hire] = useMutation(HireCookDocument);
  const [lining, setOnLine] = useMutation(SetCookOnLineDocument);
  const [retiring, retire] = useMutation(RetireCookDocument);
  const [problem, setProblem] = useState<MessageKey | null>(null);

  function settle(outcome: Outcome): boolean {
    if (outcome.error) setProblem("board.actionFailed");
    else if (outcome.problem) setProblem(problemMessage[outcome.problem]);
    else setProblem(null);
    return !outcome.error && !outcome.problem;
  }

  const members: PoolMember[] = pool.cooks.map((cook) => {
    const time = cook.currentKitchenOrderId
      ? orderTimes.get(cook.currentKitchenOrderId)
      : undefined;
    return {
      id: cook.id,
      name: cook.name,
      onLine: cook.onLine,
      state: cook.currentKitchenOrderId
        ? "working"
        : cook.onLine
          ? "free"
          : "offline",
      doing: cook.currentKitchenOrderId
        ? time
          ? t("board.cooks.cooking", { time })
          : t("board.cooks.cookingOrder")
        : cook.onLine
          ? t("board.cooks.free")
          : t("board.pool.offLine"),
    };
  });

  async function takeOff(cookId: string, onLine: boolean): Promise<boolean> {
    const result = await setOnLine({ restaurantId, cookId, onLine });
    return settle({
      error: result.error,
      problem: result.data?.setCookOnLine.problem,
    });
  }

  // − takes the cook off the line first: only an off-line cook leaves.
  async function remove(member: PoolMember) {
    if (member.onLine && !(await takeOff(member.id, false))) return;
    const result = await retire({ restaurantId, cookId: member.id });
    settle({
      error: result.error,
      problem: result.data?.retireCook.problem,
    });
  }

  const removable = removableMember(members);
  const pending = hiring.fetching || lining.fetching || retiring.fetching;

  return (
    <section aria-label={t("board.cooks.title")} className="board-pool">
      <PoolLoad
        backlog={t("board.cooks.queued", { count: pool.queued })}
        busy={pool.busy}
        label={t("board.cooks.title")}
        onLine={pool.onLine}
        size={pool.size}
        // An empty line with a queue: orders wait until the Preparation
        // timer of orders runs out (FR-624, AC-797).
        warning={
          pool.onLine === 0 && pool.queued > 0 ? t("board.cooks.noLine") : null
        }
        actions={
          <PoolSize
            addLabel={t("board.cooks.hire")}
            canRemove={removable !== undefined}
            onAdd={async () => {
              const name = newWorkerName(
                locale,
                pool.cooks.map((cook) => cook.name),
              );
              const result = await hire({ restaurantId, name });
              settle({
                error: result.error,
                problem: result.data?.hireCook.problem,
              });
            }}
            onRemove={() => {
              if (removable) void remove(removable);
            }}
            pending={pending}
            removeLabel={t("board.cooks.retireOne")}
          />
        }
      />
      <PoolMembers
        label={t("board.cooks.members")}
        members={members}
        onLine={(member, onLine) => void takeOff(member.id, onLine)}
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
