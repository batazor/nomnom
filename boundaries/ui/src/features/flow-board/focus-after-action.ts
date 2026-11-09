import { type RefObject, useCallback, useEffect, useRef } from "react";

type Acted = Readonly<{
  key: string;
  index: number;
  // Found when marking: the list itself is gone once its last card leaves.
  heading: HTMLElement | null;
}>;

const cardSelector = "[data-focus-key]";

function cardsIn(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(cardSelector)];
}

// Keeps keyboard focus on a board column when the card a user acted on
// changes or leaves (FR-780). Cards carry data-focus-key (the order id); mark
// the card before its action. Once the answer or kitchenChanged/courierChanged
// has removed the pressed button, focus goes to the head of the same card, else
// of the card now at its place (or the last one), else to the column heading.
// Only lost focus is restored: answers come late, and a user who has moved
// on keeps their focus. Touching anything outside the card forgets the mark.
export function useFocusAfterAction(root: RefObject<HTMLElement | null>) {
  const acted = useRef<Acted | null>(null);

  useEffect(() => {
    function forget(event: Event) {
      const mark = acted.current;
      const container = root.current;
      if (!mark) return;
      const card = (container ? cardsIn(container) : []).find(
        (element) => element.dataset.focusKey === mark.key,
      );
      if (event.target instanceof Node && card?.contains(event.target)) return;
      acted.current = null;
    }
    document.addEventListener("focusin", forget);
    document.addEventListener("pointerdown", forget);
    return () => {
      document.removeEventListener("focusin", forget);
      document.removeEventListener("pointerdown", forget);
    };
  }, [root]);

  const restore = useCallback(() => {
    const mark = acted.current;
    if (!mark) return;
    const active = document.activeElement;
    if (active && active !== document.body) return;
    const container = root.current;
    const cards = container ? cardsIn(container) : [];
    const card =
      cards.find((element) => element.dataset.focusKey === mark.key) ??
      cards[Math.min(mark.index, cards.length - 1)];
    const target =
      card?.querySelector<HTMLElement>("button.board-card-head") ??
      card?.querySelector<HTMLElement>("button") ??
      mark.heading;
    acted.current = null;
    target?.focus();
  }, [root]);

  // After every render: a stage change replaces the pressed button at once.
  useEffect(restore);

  const mark = useCallback(
    (key: string) => {
      const container = root.current;
      if (!container) return;
      const index = cardsIn(container).findIndex(
        (element) => element.dataset.focusKey === key,
      );
      acted.current = {
        key,
        index: Math.max(0, index),
        heading:
          container
            .closest(".board-column")
            ?.querySelector<HTMLElement>(".board-column-header h2") ?? null,
      };
    },
    [root],
  );

  // For AnimatePresence onExitComplete: a leaving card keeps its node, and
  // focus, until its exit ends, and the node goes only after this callback.
  const afterExit = useCallback(() => {
    window.setTimeout(restore);
  }, [restore]);

  return { mark, afterExit };
}
