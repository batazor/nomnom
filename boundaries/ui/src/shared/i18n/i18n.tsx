import { createContext, type ReactNode, useContext, useMemo } from "react";

import type { SupportedLocale } from "../config/runtime-config";
import { messages, type MessageKey } from "./messages";

type Variables = Readonly<Record<string, string | number>>;

function interpolate(message: string, variables: Variables = {}): string {
  return message.replace(/\{(\w+)\}/g, (_, key: string) =>
    String(variables[key] ?? `{${key}}`),
  );
}

export function createTranslator(locale: string) {
  const localizedMessages =
    locale in messages ? messages[locale as SupportedLocale] : messages.ru;

  return (key: MessageKey, variables?: Variables): string => {
    const localized = localizedMessages[key] ?? messages.ru[key];
    return interpolate(localized, variables);
  };
}

export function createFormatters(locale: SupportedLocale) {
  return {
    currency: (currency: string) =>
      new Intl.NumberFormat(locale, {
        style: "currency",
        currency,
        maximumFractionDigits: 2,
      }),
    date: new Intl.DateTimeFormat(locale, {
      day: "numeric",
      month: "long",
      year: "numeric",
    }),
    number: new Intl.NumberFormat(locale),
    plural: new Intl.PluralRules(locale),
  };
}

type I18nValue = Readonly<{
  locale: SupportedLocale;
  t: ReturnType<typeof createTranslator>;
  formatters: ReturnType<typeof createFormatters>;
}>;

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({
  children,
  locale,
}: {
  children: ReactNode;
  locale: SupportedLocale;
}) {
  const value = useMemo(
    () => ({
      locale,
      t: createTranslator(locale),
      formatters: createFormatters(locale),
    }),
    [locale],
  );
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const value = useContext(I18nContext);
  if (!value) {
    throw new Error("useI18n must be used within I18nProvider");
  }
  return value;
}
