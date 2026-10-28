import { useId, useState } from "react";
import { useMutation, useQuery } from "urql";

import {
  ManageOpeningHoursDocument,
  type ManagedOpeningHoursFragment,
  SetOpeningHoursDocument,
} from "../../shared/graphql/generated/graphql";
import { useI18n } from "../../shared/i18n/i18n";
import type { MessageKey } from "../../shared/i18n/messages";
import { Button } from "../../shared/ui";
import {
  type InvalidInterval,
  invalidOpeningHours,
  type OpeningErrorKind,
  openingErrorKind,
} from "./bff-error";

// ISO days of week, Monday first, as restaurants keeps them.
const weekDays = [1, 2, 3, 4, 5, 6, 7] as const;

const dayLabel: Record<(typeof weekDays)[number], MessageKey> = {
  1: "manage.hours.day.1",
  2: "manage.hours.day.2",
  3: "manage.hours.day.3",
  4: "manage.hours.day.4",
  5: "manage.hours.day.5",
  6: "manage.hours.day.6",
  7: "manage.hours.day.7",
};

const invalidMessage: Record<InvalidInterval["reason"], MessageKey> = {
  overlap: "manage.hours.error.overlap",
  invalid_time: "manage.hours.error.invalid_time",
  empty_interval: "manage.hours.error.empty_interval",
  invalid_day: "manage.hours.error.invalid_day",
};

const failureMessage: Record<OpeningErrorKind, MessageKey> = {
  notFound: "manage.error.notFound",
  notPermitted: "manage.error.notPermitted",
  unauthenticated: "manage.error.unauthenticated",
  failed: "manage.hours.error.failed",
};

type Interval = Readonly<{
  // Key of the row while it is edited; not sent.
  key: string;
  dayOfWeek: number;
  opens: string;
  closes: string;
}>;

type SaveState =
  | { kind: "idle" }
  | { kind: "saving" }
  // restaurants named the interval it rejected (FR-700).
  | { kind: "invalid"; interval: InvalidInterval }
  | { kind: "failed"; error: OpeningErrorKind };

type ServerHours = ManagedOpeningHoursFragment["openingHours"];

function draftOf(hours: ServerHours): Interval[] {
  return hours.map((interval, index) => ({
    key: `saved-${index}`,
    dayOfWeek: interval.dayOfWeek,
    opens: interval.opens,
    closes: interval.closes,
  }));
}

// Weekly opening hours of a restaurant for its owner (FR-701): intervals by
// day on the restaurant's wall clock, added and removed here and saved as a
// whole schedule. A rejected interval shows the reason next to it.
export function OpeningHours({ restaurantId }: { restaurantId: string }) {
  const { t } = useI18n();
  const [{ data, error }, reload] = useQuery({
    query: ManageOpeningHoursDocument,
    variables: { restaurantId },
  });
  // Outlives the form, which starts over from the saved schedule.
  const [saved, setSaved] = useState<{ changed: boolean } | null>(null);

  if (error && !data) {
    return (
      <section aria-label={t("manage.hours.title")} className="manage-hours">
        <p className="manage-error" role="alert">
          {t("manage.hours.loadError")}
        </p>
        <Button
          onClick={() => reload({ requestPolicy: "network-only" })}
          size="sm"
          variant="outline"
        >
          {t("manage.retry")}
        </Button>
      </section>
    );
  }
  if (!data?.restaurant) return null;
  const restaurant = data.restaurant;
  // A new schedule from the server starts a new draft.
  return (
    <HoursForm
      key={JSON.stringify(restaurant.openingHours)}
      onEdit={() => setSaved(null)}
      onSaved={(changed) => setSaved({ changed })}
      restaurant={restaurant}
      saved={saved}
    />
  );
}

function HoursForm({
  restaurant,
  saved,
  onSaved,
  onEdit,
}: {
  restaurant: ManagedOpeningHoursFragment;
  saved: { changed: boolean } | null;
  onSaved: (changed: boolean) => void;
  onEdit: () => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const [, setOpeningHours] = useMutation(SetOpeningHoursDocument);
  const [draft, setDraft] = useState<Interval[]>(() =>
    draftOf(restaurant.openingHours),
  );
  const [added, setAdded] = useState(0);
  const [state, setState] = useState<SaveState>({ kind: "idle" });
  const saving = state.kind === "saving";

  function change(key: string, field: "opens" | "closes", value: string) {
    onEdit();
    setDraft((current) =>
      current.map((interval) =>
        interval.key === key ? { ...interval, [field]: value } : interval,
      ),
    );
  }

  function add(dayOfWeek: number) {
    onEdit();
    setAdded((count) => count + 1);
    setDraft((current) => [
      ...current,
      { key: `added-${added}`, dayOfWeek, opens: "", closes: "" },
    ]);
  }

  function remove(key: string) {
    onEdit();
    setDraft((current) => current.filter((interval) => interval.key !== key));
  }

  async function save() {
    if (saving) return;
    setState({ kind: "saving" });
    const result = await setOpeningHours({
      restaurantId: restaurant.id,
      openingHours: draft.map(({ dayOfWeek, opens, closes }) => ({
        dayOfWeek,
        opens,
        closes,
      })),
    });
    const payload = result.data?.setOpeningHours;
    if (result.error) {
      const interval = invalidOpeningHours(result.error);
      setState(
        interval
          ? { kind: "invalid", interval }
          : { kind: "failed", error: openingErrorKind(result.error) },
      );
      return;
    }
    if (!payload) {
      setState({ kind: "failed", error: "failed" });
      return;
    }
    // The query of the restaurant is refetched with the new schedule; the
    // form then starts over from it.
    setState({ kind: "idle" });
    onSaved(payload.changed);
  }

  // The rejected interval as the owner sent it: same day and times.
  function rejection(interval: Interval): MessageKey | null {
    if (state.kind !== "invalid") return null;
    const rejected = state.interval;
    return rejected.day === interval.dayOfWeek &&
      rejected.opens === interval.opens &&
      rejected.closes === interval.closes
      ? invalidMessage[rejected.reason]
      : null;
  }
  const rejectedShown =
    state.kind === "invalid" &&
    draft.some((interval) => rejection(interval) !== null);

  return (
    <section
      aria-busy={saving || undefined}
      aria-labelledby={titleId}
      className="manage-hours"
    >
      <div className="manage-hours-head">
        <h3 id={titleId}>{t("manage.hours.title")}</h3>
        <span className="manage-hours-zone">
          {t("manage.hours.zone", { zone: restaurant.timeZone })}
        </span>
      </div>
      {draft.length === 0 ? (
        <p className="manage-hours-empty">{t("manage.hours.empty")}</p>
      ) : null}
      <ul className="manage-hours-days">
        {weekDays.map((day) => {
          const intervals = draft.filter(
            (interval) => interval.dayOfWeek === day,
          );
          const name = t(dayLabel[day]);
          return (
            <li className="manage-hours-day" key={day}>
              <span className="manage-hours-day-name">{name}</span>
              <div className="manage-hours-intervals">
                {intervals.length === 0 ? (
                  <span className="manage-hours-off">
                    {t("manage.hours.dayOff")}
                  </span>
                ) : null}
                {intervals.map((interval) => {
                  const problem = rejection(interval);
                  const overnight =
                    interval.opens !== "" &&
                    interval.closes !== "" &&
                    interval.closes <= interval.opens;
                  return (
                    <div
                      className="manage-hours-interval"
                      data-invalid={problem ? "true" : undefined}
                      key={interval.key}
                    >
                      <label>
                        <span className="sr-only">
                          {`${name}, ${t("manage.hours.opens")}`}
                        </span>
                        <input
                          aria-invalid={problem ? true : undefined}
                          onChange={(event) =>
                            change(interval.key, "opens", event.target.value)
                          }
                          required
                          step={60}
                          type="time"
                          value={interval.opens}
                        />
                      </label>
                      <span aria-hidden="true">–</span>
                      <label>
                        <span className="sr-only">
                          {`${name}, ${t("manage.hours.closes")}`}
                        </span>
                        <input
                          aria-invalid={problem ? true : undefined}
                          onChange={(event) =>
                            change(interval.key, "closes", event.target.value)
                          }
                          required
                          step={60}
                          type="time"
                          value={interval.closes}
                        />
                      </label>
                      {overnight ? (
                        <span className="manage-hours-overnight">
                          {t("manage.hours.overnight")}
                        </span>
                      ) : null}
                      <Button
                        aria-label={t("manage.hours.removeLabel", {
                          day: name,
                          opens: interval.opens,
                          closes: interval.closes,
                        })}
                        disabled={saving}
                        onClick={() => remove(interval.key)}
                        size="sm"
                        variant="ghost"
                      >
                        {t("manage.hours.remove")}
                      </Button>
                      {problem ? (
                        <p className="manage-error" role="alert">
                          {t(problem)}
                        </p>
                      ) : null}
                    </div>
                  );
                })}
                <Button
                  aria-label={t("manage.hours.addLabel", { day: name })}
                  disabled={saving}
                  onClick={() => add(day)}
                  size="sm"
                  variant="outline"
                >
                  {t("manage.hours.add")}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
      {state.kind === "invalid" && !rejectedShown ? (
        <p className="manage-error" role="alert">
          {t("manage.hours.error.invalid")}
        </p>
      ) : null}
      {state.kind === "failed" ? (
        <p className="manage-error" role="alert">
          {t(failureMessage[state.error])}
        </p>
      ) : null}
      <Button isPending={saving} onClick={save} size="sm">
        {t("manage.hours.save")}
      </Button>
      <p aria-live="polite" className="manage-hours-feedback">
        {saving ? t("manage.hours.saving") : null}
        {!saving && saved
          ? t(saved.changed ? "manage.hours.saved" : "manage.hours.unchanged")
          : null}
      </p>
    </section>
  );
}
