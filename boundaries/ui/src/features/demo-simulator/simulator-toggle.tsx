import { useState } from "react";
import { useMutation, useQuery } from "urql";

import {
  DemoSimulatorDocument,
  SetSimulatorEnabledDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import { Switch } from "../../shared/ui";
import "./demo-simulator.css";

type ToggleState = { kind: "idle" } | { kind: "sending" } | { kind: "error" };

// Header switch of the demo Simulator (FR-607). The query runs once on load
// (no polling, ADR 36); only the operator gets an answer, everyone else gets
// PermissionDenied or Unauthenticated and sees no switch. The switch follows
// the mutation answer only, never an optimistic guess.
export function SimulatorToggle() {
  const { t } = useI18n();
  const [query] = useQuery({ query: DemoSimulatorDocument });
  const [, setSimulatorEnabled] = useMutation(SetSimulatorEnabledDocument);
  const [state, setState] = useState<ToggleState>({ kind: "idle" });
  // The state confirmed by the last mutation answer.
  const [confirmed, setConfirmed] = useState<boolean | null>(null);

  const simulator = query.data?.simulator;
  // No answer (loading, a refusal or a failure): no switch.
  if (query.error || !simulator) return null;

  const enabled = confirmed ?? simulator.enabled;
  const sending = state.kind === "sending";

  async function toggle() {
    if (sending) return;
    setState({ kind: "sending" });
    const result = await setSimulatorEnabled({ enabled: !enabled });
    const payload = result.data?.setSimulatorEnabled;
    if (result.error || !payload) {
      setState({ kind: "error" });
      return;
    }
    setConfirmed(payload.enabled);
    setState({ kind: "idle" });
  }

  return (
    <div aria-busy={sending || undefined} className="demo-simulator">
      <Switch
        checked={enabled}
        disabled={sending}
        label={t("simulator.label")}
        onChange={() => void toggle()}
      />
      {state.kind === "error" ? (
        <p className="demo-simulator-error" role="alert">
          {t("simulator.error.failed")}
        </p>
      ) : null}
    </div>
  );
}
