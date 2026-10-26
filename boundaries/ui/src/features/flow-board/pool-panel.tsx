import { type FormEvent, useState } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { Button, TextField } from "../../shared/ui";

// Shared parts of the Cook Pool and Courier Pool in the column headers
// (FR-624): the load of the line, the workers and the form that adds one.

// «Линия 3 из 5 · занято 2/3» with a load bar and the backlog line.
export function PoolLoad({
  label,
  size,
  onLine,
  busy,
  backlog,
  warning,
}: {
  label: string;
  size: number;
  onLine: number;
  busy: number;
  backlog: string;
  warning: string | null;
}) {
  const { t } = useI18n();
  return (
    <div className="board-pool-load">
      <p className="board-pool-line">
        {t("board.pool.line", { onLine, size })}
        <span aria-hidden="true"> · </span>
        {t("board.pool.busy", { busy, onLine })}
      </p>
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
  onLine: boolean;
}>;

// Workers of the pool with the line actions; busy ones have none.
export function PoolMembers({
  label,
  members,
  pending,
  onLine,
  onRetire,
}: {
  label: string;
  members: readonly PoolMember[];
  pending: boolean;
  onLine: (member: PoolMember, onLine: boolean) => void;
  onRetire: (member: PoolMember) => void;
}) {
  const { t } = useI18n();
  return (
    <ul aria-label={label} className="board-pool-members">
      {members.map((member) => (
        <li data-state={member.state} key={member.id}>
          <span aria-hidden="true" className="board-state-dot" />
          <span className="board-pool-name">{member.name}</span>
          <span className="board-pool-doing">{member.doing}</span>
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
              <>
                <Button
                  aria-label={t("board.pool.putOnNamed", { name: member.name })}
                  disabled={pending}
                  onClick={() => onLine(member, true)}
                  size="sm"
                  variant="outline"
                >
                  {t("board.pool.putOn")}
                </Button>
                <Button
                  aria-label={t("board.pool.retireNamed", {
                    name: member.name,
                  })}
                  disabled={pending}
                  onClick={() => onRetire(member)}
                  size="sm"
                  variant="ghost"
                >
                  {t("board.pool.retire")}
                </Button>
              </>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
}

// One name field and a button; the field clears after a successful add.
export function PoolAddForm({
  label,
  submit,
  pending,
  onAdd,
}: {
  label: string;
  submit: string;
  pending: boolean;
  onAdd: (name: string) => Promise<boolean>;
}) {
  const [name, setName] = useState("");
  async function add(event: FormEvent) {
    event.preventDefault();
    if (name.trim() === "") return;
    if (await onAdd(name)) setName("");
  }
  return (
    <form className="board-pool-add" onSubmit={(event) => void add(event)}>
      <TextField label={label} maxLength={64} onChange={setName} value={name} />
      <Button isPending={pending} size="sm" type="submit" variant="outline">
        {submit}
      </Button>
    </form>
  );
}
