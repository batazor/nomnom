import { Button as AstraButton } from "@astra-rn/react/button";
import type { ButtonProps as AstraButtonProps } from "@astra-rn/react/button";

export type ButtonProps = Omit<AstraButtonProps, "className"> & {
  className?: string;
};

export function Button({
  className,
  variant = "primary",
  ...props
}: ButtonProps) {
  const classes = ["nomnom-button", className].filter(Boolean).join(" ");

  return (
    <AstraButton
      className={classes}
      data-ui-primary={variant === "primary" ? "true" : undefined}
      data-variant={variant}
      variant={variant}
      {...props}
    />
  );
}
