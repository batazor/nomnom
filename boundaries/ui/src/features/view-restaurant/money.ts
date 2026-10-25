import { useI18n } from "../../shared/i18n/i18n";

export type Money = Readonly<{ amount: string; currency: string }>;

export type PricedLine = Readonly<{ price: Money; quantity: number }>;

const decimalPattern = /^(-?)(\d+)(?:\.(\d+))?$/;

function fractionDigits(amount: string): number {
  return decimalPattern.exec(amount)?.[3]?.length ?? 0;
}

// Decimal string as an integer number of 10^-scale units.
function toScaled(amount: string, scale: number): bigint {
  const match = decimalPattern.exec(amount);
  if (!match) throw new Error(`Not a decimal amount: ${amount}`);
  const [, sign, units, fraction = ""] = match;
  const value = BigInt(units + fraction.padEnd(scale, "0"));
  return sign ? -value : value;
}

function fromScaled(value: bigint, scale: number): string {
  const sign = value < 0n ? "-" : "";
  const digits = (value < 0n ? -value : value)
    .toString()
    .padStart(scale + 1, "0");
  const units = digits.slice(0, digits.length - scale);
  const fraction = digits.slice(digits.length - scale);
  return scale > 0 ? `${sign}${units}.${fraction}` : `${sign}${units}`;
}

// Exact sum of price × quantity over decimal strings, without floats. It is
// only an estimate for the cart: the order total comes from placeOrder.
// Returns null for no lines or for lines in different currencies.
export function estimateSubtotal(lines: readonly PricedLine[]): Money | null {
  const [first] = lines;
  if (!first) return null;
  const { currency } = first.price;
  if (lines.some((line) => line.price.currency !== currency)) return null;

  const scale = Math.max(
    ...lines.map((line) => fractionDigits(line.price.amount)),
  );
  const total = lines.reduce(
    (sum, line) =>
      sum + toScaled(line.price.amount, scale) * BigInt(line.quantity),
    0n,
  );
  return { amount: fromScaled(total, scale), currency };
}

export function useMoney() {
  const { formatters } = useI18n();
  // Intl.NumberFormat formats the decimal string exactly, without floats.
  return (money: Money) =>
    formatters
      .currency(money.currency)
      .format(money.amount as Intl.StringNumericLiteral);
}
