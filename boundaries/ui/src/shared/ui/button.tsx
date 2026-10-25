import type { ButtonProps as HeadlessButtonProps } from "@headlessui/react";
import { Button as HeadlessButton } from "@headlessui/react";

export type ButtonProps = Omit<HeadlessButtonProps, "className" | "as"> & {
  variant?: "primary" | "secondary" | "outline" | "ghost" | "danger";
  size?: "sm" | "md";
  fullWidth?: boolean;
  isIconOnly?: boolean;
  // Busy with the action: the button stays put but does not take presses.
  isPending?: boolean;
  className?: string;
};

export function Button({
  className,
  variant = "primary",
  size = "md",
  fullWidth = false,
  isIconOnly = false,
  isPending = false,
  disabled,
  type = "button",
  ...props
}: ButtonProps) {
  const classes = ["nomnom-button", className].filter(Boolean).join(" ");

  return (
    <HeadlessButton
      aria-busy={isPending || undefined}
      className={classes}
      data-full-width={fullWidth || undefined}
      data-icon-only={isIconOnly || undefined}
      data-pending={isPending || undefined}
      data-size={size}
      data-ui-primary={variant === "primary" ? "true" : undefined}
      data-variant={variant}
      disabled={disabled || isPending}
      type={type}
      {...props}
    />
  );
}
