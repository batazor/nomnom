import { Description } from "@astra-rn/react/description";
import { FieldError } from "@astra-rn/react/field-error";
import { Input } from "@astra-rn/react/input";
import { Label } from "@astra-rn/react/label";
import type { TextFieldProps as AstraTextFieldProps } from "@astra-rn/react/textfield";
import { TextField as AstraTextField } from "@astra-rn/react/textfield";
import type { Ref } from "react";

export type TextFieldProps = Omit<
  AstraTextFieldProps,
  "className" | "children" | "isInvalid"
> & {
  label: string;
  description?: string;
  // Shown under the field and marks it invalid while set.
  errorMessage?: string | null;
  className?: string;
  inputRef?: Ref<HTMLInputElement>;
};

export function TextField({
  label,
  description,
  errorMessage,
  className,
  inputRef,
  ...props
}: TextFieldProps) {
  const classes = ["nomnom-text-field", className].filter(Boolean).join(" ");

  return (
    <AstraTextField
      className={classes}
      fullWidth
      isInvalid={Boolean(errorMessage)}
      validationBehavior="aria"
      {...props}
    >
      <Label>{label}</Label>
      <Input ref={inputRef} />
      {description ? <Description>{description}</Description> : null}
      <FieldError>{errorMessage}</FieldError>
    </AstraTextField>
  );
}
