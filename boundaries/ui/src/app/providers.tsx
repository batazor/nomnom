import { type ReactNode, useMemo } from "react";
import { Provider as UrqlProvider } from "urql";

import type { RuntimeConfig } from "../shared/config/runtime-config";
import { createGraphqlClient } from "../shared/graphql/client";
import { I18nProvider } from "../shared/i18n/i18n";

export function AppProviders({
  children,
  config,
}: {
  children: ReactNode;
  config: RuntimeConfig;
}) {
  const client = useMemo(
    () =>
      createGraphqlClient({
        url: config.publicGraphqlUrl,
        reportError(report) {
          if (import.meta.env.DEV) {
            console.error("GraphQL request failed", report);
          }
        },
      }),
    [config.publicGraphqlUrl],
  );

  return (
    <I18nProvider locale={config.locale}>
      <UrqlProvider value={client}>{children}</UrqlProvider>
    </I18nProvider>
  );
}
