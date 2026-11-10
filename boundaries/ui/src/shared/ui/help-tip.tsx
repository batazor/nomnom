import { useEffect, useId, useRef, useState } from "react";

const hoverDelay = 500;
const nearbyTipWindow = 300;
let lastHoverEnd = -Infinity;

export type HelpTipProps = Readonly<{
  // Names the button: "what is this column".
  label: string;
  children: string;
}>;

// A quiet info button at the end of a heading; hovering or focusing it shows
// a short explanation under it, a tap does the same on a touch screen, and
// Escape hides it. Headless UI has no tooltip (its Popover opens on click), so
// this one is a plain button described by the tooltip.
export function HelpTip({ label, children }: HelpTipProps) {
  const [hoverOpen, setHoverOpen] = useState(false);
  const [focusOpen, setFocusOpen] = useState(false);
  const hoverTimer = useRef<number>(undefined);
  const tipId = useId();
  const open = hoverOpen || focusOpen;

  function cancelHover() {
    window.clearTimeout(hoverTimer.current);
  }

  useEffect(() => {
    return () => window.clearTimeout(hoverTimer.current);
  }, []);

  useEffect(() => {
    if (!open) return;
    function dismiss(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      window.clearTimeout(hoverTimer.current);
      setHoverOpen(false);
      setFocusOpen(false);
    }
    document.addEventListener("keydown", dismiss);
    return () => document.removeEventListener("keydown", dismiss);
  }, [open]);

  return (
    <span
      className="nomnom-help"
      onPointerEnter={(event) => {
        if (event.pointerType !== "mouse" || open) return;
        if (performance.now() - lastHoverEnd < nearbyTipWindow) {
          setHoverOpen(true);
          return;
        }
        hoverTimer.current = window.setTimeout(
          () => setHoverOpen(true),
          hoverDelay,
        );
      }}
      onPointerLeave={(event) => {
        if (event.pointerType !== "mouse") return;
        cancelHover();
        if (hoverOpen) lastHoverEnd = performance.now();
        setHoverOpen(false);
      }}
    >
      <button
        aria-describedby={tipId}
        aria-label={label}
        className="nomnom-help-button"
        data-open={open || undefined}
        onBlur={() => setFocusOpen(false)}
        onClick={() => {
          cancelHover();
          setFocusOpen(true);
        }}
        onFocus={() => {
          cancelHover();
          setFocusOpen(true);
        }}
        type="button"
      >
        <svg
          aria-hidden="true"
          fill="none"
          focusable="false"
          viewBox="0 0 16 16"
        >
          <circle
            cx="8"
            cy="8"
            r="6.25"
            stroke="currentColor"
            strokeWidth="1.25"
          />
          <path
            d="M8 7.25v3.5"
            stroke="currentColor"
            strokeLinecap="round"
            strokeWidth="1.25"
          />
          <circle cx="8" cy="5.1" fill="currentColor" r="0.8" />
        </svg>
      </button>
      <span
        className="nomnom-help-panel"
        data-closed={open ? undefined : true}
        id={tipId}
        role="tooltip"
      >
        {children}
      </span>
    </span>
  );
}
