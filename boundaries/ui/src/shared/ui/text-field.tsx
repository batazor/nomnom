import type { InputProps } from "@headlessui/react";
import { Description, Field, Input, Label } from "@headlessui/react";
import type { Ref } from "react";

export type TextFieldProps = Omit<
  InputProps,
  "className" | "children" | "as" | "invalid" | "onChange"
> & {
  label: string;
  description?: string;
  // Shown under the field and marks it invalid while set.
  errorMessage?: string | null;
  onChange?: (value: string) => void;
  className?: string;
  inputRef?: Ref<HTMLInputElement>;
};

export function TextField({
  label,
  description,
  errorMessage,
  onChange,
  className,
  inputRef,
  ...props
}: TextFieldProps) {
  const classes = ["nomnom-text-field", className].filter(Boolean).join(" ");

  return (
    <Field className={classes}>
      <Label className="nomnom-label">{label}</Label>
      <Input
        className="nomnom-input"
        invalid={Boolean(errorMessage)}
        onChange={(event) => onChange?.(event.target.value)}
        ref={inputRef}
        {...props}
      />
      {description ? (
        <Description className="nomnom-description">{description}</Description>
      ) : null}
      {errorMessage ? (
        <Description className="nomnom-field-error">{errorMessage}</Description>
      ) : null}
    </Field>
  );
}
