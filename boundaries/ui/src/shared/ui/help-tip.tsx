import { useId, useState } from "react";

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
  const [open, setOpen] = useState(false);
  const tipId = useId();

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: hovering the button or its tooltip keeps it open for the mouse; keyboard users get it from the button's focus.
    <span
      className="nomnom-help"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <button
        aria-describedby={tipId}
        aria-label={label}
        className="nomnom-help-button"
        data-open={open || undefined}
        onBlur={() => setOpen(false)}
        onClick={() => setOpen(true)}
        onFocus={() => setOpen(true)}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
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
