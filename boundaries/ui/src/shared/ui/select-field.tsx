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
      {/* The native arrow differs per browser: the control draws its own. */}
      <span className="nomnom-select-control">
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
        <svg
          aria-hidden="true"
          className="nomnom-select-arrow"
          fill="none"
          focusable="false"
          viewBox="0 0 16 16"
        >
          <path
            d="m4.5 6.5 3.5 3.5 3.5-3.5"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="1.5"
          />
        </svg>
      </span>
    </Field>
  );
}
