import { createContext, type ReactNode, useContext, useMemo } from "react";
import { Provider as UrqlProvider } from "urql";

import { boardUser, useSignedInToken } from "../auth/sign-in";
import {
  createGraphqlClient,
  type GraphqlErrorReport,
  type SubscriptionTransport,
} from "../graphql/client";
import { marketGraphqlUrl, useMarketSelection } from "../market/market";

// Roles of the flow board; each column has its own client and cache.
export const roles = ["customer", "owner", "kitchen", "courier"] as const;

export type Role = (typeof roles)[number];

// With mocks every role gets a fixed token: the mock BFF does not check it.
// Against BFF every role calls as the user signed in on the board.
const MocksContext = createContext(false);

export function RoleSessionProvider({
  mocks,
  children,
}: {
  mocks: boolean;
  children: ReactNode;
}) {
  return (
    <MocksContext.Provider value={mocks}>{children}</MocksContext.Provider>
  );
}

// Whether the board can call BFF: with mocks or once the user signed in.
export function useCanAct(): boolean {
  const mocks = useContext(MocksContext);
  const signedIn = useSignedInToken(boardUser);
  return mocks || signedIn !== undefined;
}

type ClientSettings = Readonly<{
  reportError?: (report: GraphqlErrorReport) => void;
  subscriptions?: SubscriptionTransport;
  persistedOperations: boolean;
}>;

const ClientSettingsContext = createContext<ClientSettings | null>(null);

export function RoleClientSettings({
  settings,
  children,
}: {
  settings: ClientSettings;
  children: ReactNode;
}) {
  return (
    <ClientSettingsContext.Provider value={settings}>
      {children}
    </ClientSettingsContext.Provider>
  );
}

// Everything inside calls BFF as the user of role, in the chosen Market: the
// client talks to /m/{marketId}/graphql, which the public gateway routes to
// the Cell of the Market (ADR 41). Each role has its own client, so cached
// data of one user never shows up in another column. Without a chosen Market
// there is no client: GraphQL screens render only behind the Market choice.
export function AsRole({
  as: role,
  children,
}: {
  as: Role;
  children: ReactNode;
}) {
  const settings = useContext(ClientSettingsContext);
  if (!settings) throw new Error("AsRole needs RoleClientSettings");
  const mocks = useContext(MocksContext);
  const signedIn = useSignedInToken(boardUser);
  const token = mocks ? `mock-${role}` : signedIn;
  const { apiUrl, marketId } = useMarketSelection();

  // A new Market gets a new client: cached data of one Market never shows
  // up in another.
  const client = useMemo(
    () =>
      marketId === undefined
        ? undefined
        : createGraphqlClient({
            url: marketGraphqlUrl(apiUrl, marketId),
            reportError: settings.reportError,
            subscriptions: settings.subscriptions,
            persistedOperations: settings.persistedOperations,
            getAccessToken: () => token,
          }),
    [settings, token, apiUrl, marketId],
  );

  if (!client) return children;
  return <UrqlProvider value={client}>{children}</UrqlProvider>;
}
