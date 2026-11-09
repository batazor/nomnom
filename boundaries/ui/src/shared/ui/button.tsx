import type { ButtonProps as HeadlessButtonProps } from "@headlessui/react";
import { Button as HeadlessButton } from "@headlessui/react";
import type { MouseEvent } from "react";

export type ButtonProps = Omit<HeadlessButtonProps, "className" | "as"> & {
  variant?: "primary" | "secondary" | "outline" | "ghost" | "danger";
  size?: "sm" | "md";
  fullWidth?: boolean;
  isIconOnly?: boolean;
  // Busy with the action: the button stays put but does not take presses.
  isPending?: boolean;
  className?: string;
};

// `disabled` is for a button that is off before anyone reaches it. A button
// that turns itself off by being pressed (isPending, or aria-disabled from the
// caller) keeps focus instead: a natively disabled button drops it to the body
// (FR-780). Its presses are ignored, a submit included.
export function Button({
  className,
  variant = "primary",
  size = "md",
  fullWidth = false,
  isIconOnly = false,
  isPending = false,
  type = "button",
  onClick,
  ...props
}: ButtonProps) {
  const classes = ["nomnom-button", className].filter(Boolean).join(" ");
  const ariaDisabled = props["aria-disabled"];
  const inert = isPending || ariaDisabled === true || ariaDisabled === "true";

  return (
    <HeadlessButton
      {...props}
      aria-busy={isPending || undefined}
      aria-disabled={inert || undefined}
      className={classes}
      data-full-width={fullWidth || undefined}
      data-icon-only={isIconOnly || undefined}
      data-inert={inert || undefined}
      data-pending={isPending || undefined}
      data-size={size}
      data-ui-primary={variant === "primary" ? "true" : undefined}
      data-variant={variant}
      type={type}
      onClick={(event: MouseEvent<HTMLButtonElement>) => {
        if (inert) {
          event.preventDefault();
          return;
        }
        onClick?.(event);
      }}
    />
  );
}
