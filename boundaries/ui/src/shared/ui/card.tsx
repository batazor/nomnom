import { Card as AstraCard } from "@astra-rn/react/card";
import type { CardProps as AstraCardProps } from "@astra-rn/react/card";

export type CardProps = Omit<AstraCardProps, "className"> & {
  className?: string;
};

export function Card({ className, ...props }: CardProps) {
  const classes = ["nomnom-card", className].filter(Boolean).join(" ");
  return <AstraCard className={classes} {...props} />;
}
