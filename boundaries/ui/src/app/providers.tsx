import type { Faro } from "@grafana/faro-react";
import { type ReactNode, useMemo } from "react";
import { Provider as UrqlProvider } from "urql";

import type { RuntimeConfig } from "../shared/config/runtime-config";
import { createGraphqlClient } from "../shared/graphql/client";
import { I18nProvider } from "../shared/i18n/i18n";
import { reportGraphqlError } from "../shared/telemetry/faro";

export function AppProviders({
  children,
  config,
  faro,
}: {
  children: ReactNode;
  config: RuntimeConfig;
  faro?: Faro;
}) {
  const client = useMemo(
    () =>
      createGraphqlClient({
        url: config.publicGraphqlUrl,
        reportError(report) {
          if (import.meta.env.DEV) {
            console.error("GraphQL request failed", report);
          }
          reportGraphqlError(faro, report);
        },
      }),
    [config.publicGraphqlUrl, faro],
  );

  return (
    <I18nProvider locale={config.locale}>
      <UrqlProvider value={client}>{children}</UrqlProvider>
    </I18nProvider>
  );
}
