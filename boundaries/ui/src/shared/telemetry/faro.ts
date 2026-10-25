import {
  createReactRouterV7DataOptions,
  type Faro,
  getWebInstrumentations,
  initializeFaro,
  ReactIntegration,
  type TransportItem,
} from "@grafana/faro-react";
import { TracingInstrumentation } from "@grafana/faro-web-tracing";
import { matchRoutes } from "react-router-dom";

import type { RuntimeConfig } from "../config/runtime-config";
import type { GraphqlErrorReport } from "../graphql/client";

// Resource ids in paths are high-cardinality and link a session to an order
// (docs/ui.md#observability): telemetry carries the route shape only.
const idSegments =
  /\/(restaurants|orders)\/[^/?#]+|\/manage\/restaurants\/[^/?#]+/g;

export function redactUrl(value: string): string {
  const url = new URL(value);
  const path = url.pathname.replace(idSegments, (segment) =>
    segment.startsWith("/manage/")
      ? "/manage/restaurants/:restaurantId"
      : segment.startsWith("/orders/")
        ? "/orders/:orderId"
        : "/restaurants/:restaurantId",
  );
  return `${url.origin}${path}`;
}

export function redactItem(item: TransportItem): TransportItem {
  const page = item.meta.page;
  if (!page?.url) return item;
  return {
    ...item,
    meta: { ...item.meta, page: { ...page, url: redactUrl(page.url) } },
  };
}

export function initTelemetry(config: RuntimeConfig): Faro | undefined {
  if (!config.telemetryUrl || config.enableMocks) return undefined;

  return initializeFaro({
    url: config.telemetryUrl,
    app: {
      name: "customer-ui",
      namespace: "nomnom",
      version: import.meta.env.VITE_APP_VERSION ?? "dev",
      environment: config.environment,
    },
    instrumentations: [
      // Console capture would ship GraphQL variables logged in development.
      ...getWebInstrumentations({ captureConsole: false }),
      // traceparent only to the BFF: the trace continues in Cosmo Router.
      new TracingInstrumentation({
        instrumentationOptions: {
          propagateTraceHeaderCorsUrls: [
            new URL(config.publicGraphqlUrl).origin,
          ],
        },
      }),
      // Views are named by route pattern (withFaroRouterInstrumentation in app/router).
      new ReactIntegration({
        router: createReactRouterV7DataOptions({ matchRoutes }),
      }),
    ],
    beforeSend: redactItem,
  });
}

export function reportGraphqlError(
  faro: Faro | undefined,
  report: GraphqlErrorReport,
): void {
  faro?.api.pushEvent("graphql_error", {
    operation_kind: report.operationName,
    error_kind: report.kind,
    ...(report.requestId ? { request_id: report.requestId } : {}),
  });
}
