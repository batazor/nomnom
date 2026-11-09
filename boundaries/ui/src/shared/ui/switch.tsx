import type { SwitchProps as HeadlessSwitchProps } from "@headlessui/react";
import { Field, Switch as HeadlessSwitch, Label } from "@headlessui/react";

export type SwitchProps = Omit<
  HeadlessSwitchProps,
  "className" | "children" | "as"
> & {
  label: string;
  // Busy with the change: like Button's isPending, the switch keeps focus
  // and ignores presses instead of turning natively disabled (FR-780).
  isPending?: boolean;
  className?: string;
};

export function Switch({
  label,
  isPending = false,
  onChange,
  className,
  ...props
}: SwitchProps) {
  const classes = ["nomnom-switch", className].filter(Boolean).join(" ");

  return (
    <Field className={classes}>
      <HeadlessSwitch
        {...props}
        aria-disabled={isPending || undefined}
        className="nomnom-switch-control"
        data-pending={isPending || undefined}
        onChange={(checked: boolean) => {
          if (!isPending) onChange?.(checked);
        }}
      >
        <span aria-hidden="true" className="nomnom-switch-thumb" />
      </HeadlessSwitch>
      <Label className="nomnom-label">{label}</Label>
    </Field>
  );
}
