import { Label } from "@astra-rn/react/label";
import type { SwitchProps as AstraSwitchProps } from "@astra-rn/react/switch";
import { Switch as AstraSwitch } from "@astra-rn/react/switch";

export type SwitchProps = Omit<AstraSwitchProps, "className" | "children"> & {
  label: string;
  className?: string;
};

export function Switch({ label, className, ...props }: SwitchProps) {
  const classes = ["nomnom-switch", className].filter(Boolean).join(" ");

  return (
    <AstraSwitch className={classes} {...props}>
      <AstraSwitch.Content>
        <AstraSwitch.Control>
          <AstraSwitch.Thumb />
        </AstraSwitch.Control>
        <Label>{label}</Label>
      </AstraSwitch.Content>
    </AstraSwitch>
  );
}
