import type { Faro } from "@grafana/faro-react";
import { LazyMotion, MotionConfig } from "motion/react";
import { type ReactNode, useMemo } from "react";
import { SignInProvider } from "../shared/auth/sign-in";
import type { RuntimeConfig } from "../shared/config/runtime-config";
import type { SubscriptionTransport } from "../shared/graphql/client";
import { I18nProvider } from "../shared/i18n/i18n";
import {
  AsRole,
  RoleClientSettings,
  RoleSessionProvider,
} from "../shared/session/roles";
import { reportGraphqlError } from "../shared/telemetry/faro";

// Mocks answer subscriptions in process; the module loads only with mocks.
const mockSubscriptions: SubscriptionTransport = (request, sink, connected) => {
  let unsubscribe: (() => void) | undefined;
  let stopped = false;
  import("../shared/graphql/mocks/subscriptions")
    .then((mocks) => {
      if (!stopped) {
        unsubscribe = mocks.mockSubscriptions(request, sink, connected);
      }
    })
    .catch(sink.error);
  return () => {
    stopped = true;
    unsubscribe?.();
  };
};

const loadMotionFeatures = () =>
  import("./motion-features").then((module) => module.default);

export function AppProviders({
  children,
  config,
  faro,
}: {
  children: ReactNode;
  config: RuntimeConfig;
  faro?: Faro;
}) {
  const settings = useMemo(
    () => ({
      url: config.publicGraphqlUrl,
      subscriptions: config.enableMocks ? mockSubscriptions : undefined,
      reportError(report: Parameters<typeof reportGraphqlError>[1]) {
        if (import.meta.env.DEV) {
          console.error("GraphQL request failed", report);
        }
        reportGraphqlError(faro, report);
      },
    }),
    [config.publicGraphqlUrl, config.enableMocks, faro],
  );

  return (
    // strict keeps components on the lightweight `m`; "user" turns transforms
    // off under prefers-reduced-motion, like the CSS motion tokens.
    <LazyMotion features={loadMotionFeatures} strict>
      <MotionConfig reducedMotion="user">
        <I18nProvider locale={config.locale}>
          <SignInProvider config={config.oidc}>
            <RoleSessionProvider mocks={config.enableMocks}>
              <RoleClientSettings settings={settings}>
                {/* Outside the board columns the app acts as the customer. */}
                <AsRole as="customer">{children}</AsRole>
              </RoleClientSettings>
            </RoleSessionProvider>
          </SignInProvider>
        </I18nProvider>
      </MotionConfig>
    </LazyMotion>
  );
}
