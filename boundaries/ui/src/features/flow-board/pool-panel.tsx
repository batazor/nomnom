import type { ReactNode } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button } from "../../shared/ui";

// Shared parts of the Cook Pool and Courier Pool in the column headers
// (FR-624): the load of the line with − and + for the pool size, and the
// workers.

// «Линия 3 из 5 · занято 2/3» with a load bar and the backlog line.
export function PoolLoad({
  label,
  size,
  onLine,
  busy,
  backlog,
  warning,
  actions,
}: {
  label: string;
  size: number;
  onLine: number;
  busy: number;
  backlog: string;
  warning: string | null;
  // − and + of the pool size, at the end of the line.
  actions: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="board-pool-load">
      <div className="board-pool-head">
        <p className="board-pool-line">
          {t("board.pool.line", { onLine, size })}
          <span aria-hidden="true"> · </span>
          {t("board.pool.busy", { busy, onLine })}
        </p>
        {actions}
      </div>
      {/* An empty line has nothing to load: the meter stays at zero. */}
      <meter
        aria-label={t("board.pool.load", { label })}
        className="board-pool-bar"
        max={Math.max(onLine, 1)}
        min={0}
        value={busy}
      />
      <p className="board-pool-backlog">{backlog}</p>
      {warning ? (
        <p className="board-pool-warning" role="alert">
          {warning}
        </p>
      ) : null}
    </div>
  );
}

export type PoolMember = Readonly<{
  id: string;
  name: string;
  // Dot of the row: free on line, working, off line.
  state: "free" | "working" | "offline";
  // What the worker does now: «готовит заказ от 12:30», «свободен».
  doing: string;
  // How far the work has got, under «doing»; absent when nothing is known.
  progress?: ReactNode;
  onLine: boolean;
}>;

// Workers of the pool with the line action; busy ones have none.
export function PoolMembers({
  label,
  members,
  pending,
  onLine,
}: {
  label: string;
  members: readonly PoolMember[];
  pending: boolean;
  onLine: (member: PoolMember, onLine: boolean) => void;
}) {
  const { t } = useI18n();
  return (
    <ul aria-label={label} className="board-pool-members">
      {members.map((member) => (
        <li data-state={member.state} key={member.id}>
          <span aria-hidden="true" className="board-state-dot" />
          <span className="board-pool-name">{member.name}</span>
          <span className="board-pool-doing">{member.doing}</span>
          {member.progress}
          <span className="board-pool-actions">
            {member.state === "working" ? null : member.onLine ? (
              <Button
                aria-label={t("board.pool.takeOffNamed", { name: member.name })}
                disabled={pending}
                onClick={() => onLine(member, false)}
                size="sm"
                variant="ghost"
              >
                {t("board.pool.takeOff")}
              </Button>
            ) : (
              <Button
                aria-label={t("board.pool.putOnNamed", { name: member.name })}
                disabled={pending}
                onClick={() => onLine(member, true)}
                size="sm"
                variant="outline"
              >
                {t("board.pool.putOn")}
              </Button>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
}

// The worker − removes: the last one who is not working, off line first.
export function removableMember(
  members: readonly PoolMember[],
): PoolMember | undefined {
  const idle = members.filter((member) => member.state !== "working").reverse();
  return idle.find((member) => !member.onLine) ?? idle[0];
}

// − and + of the pool size: + adds a worker under a made-up name, − removes
// the removable one; − is off while every worker is busy.
export function PoolSize({
  addLabel,
  removeLabel,
  pending,
  canRemove,
  onAdd,
  onRemove,
}: {
  addLabel: string;
  removeLabel: string;
  pending: boolean;
  canRemove: boolean;
  onAdd: () => void;
  onRemove: () => void;
}) {
  return (
    <span className="board-pool-size">
      <Button
        aria-label={removeLabel}
        disabled={pending || !canRemove}
        isIconOnly
        onClick={onRemove}
        size="sm"
        variant="outline"
      >
        <span aria-hidden="true">−</span>
      </Button>
      <Button
        aria-label={addLabel}
        disabled={pending}
        isIconOnly
        onClick={onAdd}
        size="sm"
        variant="outline"
      >
        <span aria-hidden="true">+</span>
      </Button>
    </span>
  );
}
