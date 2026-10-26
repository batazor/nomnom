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
  PoolAddForm,
  PoolLoad,
  type PoolMember,
  PoolMembers,
} from "./pool-panel";

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
// and the queue, the cooks with their line actions and hiring by name. The
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
  const { t } = useI18n();
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
      />
      <PoolMembers
        label={t("board.cooks.members")}
        members={members}
        onLine={async (member, onLine) => {
          const result = await setOnLine({
            restaurantId,
            cookId: member.id,
            onLine,
          });
          settle({
            error: result.error,
            problem: result.data?.setCookOnLine.problem,
          });
        }}
        onRetire={async (member) => {
          const result = await retire({ restaurantId, cookId: member.id });
          settle({
            error: result.error,
            problem: result.data?.retireCook.problem,
          });
        }}
        pending={lining.fetching || retiring.fetching}
      />
      <PoolAddForm
        label={t("board.cooks.name")}
        onAdd={async (name) => {
          const result = await hire({ restaurantId, name });
          return settle({
            error: result.error,
            problem: result.data?.hireCook.problem,
          });
        }}
        pending={hiring.fetching}
        submit={t("board.cooks.hire")}
      />
      {problem ? (
        <p className="board-error" role="alert">
          {t(problem)}
        </p>
      ) : null}
    </section>
  );
}
