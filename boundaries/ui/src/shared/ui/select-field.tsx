import type { SelectProps } from "@headlessui/react";
import { Field, Label, Select } from "@headlessui/react";

export type SelectFieldOption = Readonly<{ value: string; label: string }>;

export type SelectFieldProps = Omit<
  SelectProps,
  "className" | "children" | "as" | "onChange" | "value"
> & {
  label: string;
  options: readonly SelectFieldOption[];
  value: string;
  // Shown as a disabled first option while value matches no option.
  placeholder?: string;
  onChange: (value: string) => void;
  className?: string;
};

export function SelectField({
  label,
  options,
  value,
  placeholder,
  onChange,
  className,
  ...props
}: SelectFieldProps) {
  const classes = ["nomnom-select-field", className].filter(Boolean).join(" ");
  const matches = options.some((option) => option.value === value);

  return (
    <Field className={classes}>
      <Label className="nomnom-label">{label}</Label>
      <Select
        className="nomnom-select"
        onChange={(event) => onChange(event.target.value)}
        value={matches ? value : ""}
        {...props}
      >
        {placeholder && !matches ? (
          <option disabled value="">
            {placeholder}
          </option>
        ) : null}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </Select>
    </Field>
  );
}
