import { Popover, PopoverButton, PopoverPanel } from "@headlessui/react";

export type HelpTipProps = Readonly<{
  // Names the button: "what is this column".
  label: string;
  children: string;
}>;

// A small "?" button next to a heading; pressing it shows a short
// explanation beside the button. The panel floats outside any clipping
// column and closes on Escape or a click elsewhere.
export function HelpTip({ label, children }: HelpTipProps) {
  return (
    <Popover className="nomnom-help">
      <PopoverButton aria-label={label} className="nomnom-help-button">
        <span aria-hidden="true">?</span>
      </PopoverButton>
      <PopoverPanel
        anchor={{ to: "bottom start", gap: 6 }}
        className="nomnom-help-panel"
        transition
      >
        {children}
      </PopoverPanel>
    </Popover>
  );
}
