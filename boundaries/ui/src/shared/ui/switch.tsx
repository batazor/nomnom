import type { SwitchProps as HeadlessSwitchProps } from "@headlessui/react";
import { Field, Switch as HeadlessSwitch, Label } from "@headlessui/react";

export type SwitchProps = Omit<
  HeadlessSwitchProps,
  "className" | "children" | "as"
> & {
  label: string;
  className?: string;
};

export function Switch({ label, className, ...props }: SwitchProps) {
  const classes = ["nomnom-switch", className].filter(Boolean).join(" ");

  return (
    <Field className={classes}>
      <HeadlessSwitch className="nomnom-switch-control" {...props}>
        <span aria-hidden="true" className="nomnom-switch-thumb" />
      </HeadlessSwitch>
      <Label className="nomnom-label">{label}</Label>
    </Field>
  );
}
