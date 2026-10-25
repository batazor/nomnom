import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Provider as UrqlProvider } from "urql";

import { useSignedInToken } from "../auth/sign-in";
import {
  createGraphqlClient,
  type GraphqlErrorReport,
} from "../graphql/client";

// Users of the flow board: each column calls BFF as its own user.
export const roles = ["customer", "owner", "kitchen", "courier"] as const;

export type Role = (typeof roles)[number];

export type DemoRestaurant = Readonly<{ id: string; name: string }>;

export type RoleSession = Readonly<{
  tokens: Readonly<Record<Role, string>>;
  // Moment the tokens stop being valid, epoch milliseconds.
  expiresAt: number;
  restaurants: readonly DemoRestaurant[];
}>;

// Tokens are fetched again this long before they expire.
const refreshMarginMs = 60_000;
const retryDelayMs = 5_000;

type SessionState =
  | { kind: "loading" }
  | { kind: "ready"; session: RoleSession }
  | { kind: "unavailable" };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

// Response of the kind dev token issuer (ops/kind): tokens of the demo users,
// their expiry and the demo restaurants.
export function parseRoleSession(value: unknown): RoleSession {
  if (!isRecord(value) || !isRecord(value.tokens)) {
    throw new Error("Unexpected dev token response");
  }
  const raw = value.tokens;
  const tokens = Object.fromEntries(
    roles.map((role) => {
      const token = raw[role];
      if (typeof token !== "string" || token === "") {
        throw new Error(`Dev token response has no ${role} token`);
      }
      return [role, token];
    }),
  ) as Record<Role, string>;
  // The issuer answers epoch seconds; an RFC 3339 string is accepted too.
  const expiresAt =
    typeof value.expiresAt === "number"
      ? value.expiresAt * 1000
      : Date.parse(String(value.expiresAt));
  if (Number.isNaN(expiresAt)) {
    throw new Error("Dev token response has no expiresAt");
  }
  const restaurants = Array.isArray(value.restaurants)
    ? value.restaurants.flatMap((restaurant) =>
        isRecord(restaurant) &&
        typeof restaurant.id === "string" &&
        typeof restaurant.name === "string"
          ? [{ id: restaurant.id, name: restaurant.name }]
          : [],
      )
    : [];
  return { tokens, expiresAt, restaurants };
}

// With mocks every role gets a fixed token: the mock BFF does not check it.
export const mockRoleSession: RoleSession = {
  tokens: {
    customer: "mock-customer",
    owner: "mock-owner",
    kitchen: "mock-kitchen",
    courier: "mock-courier",
  },
  expiresAt: Number.POSITIVE_INFINITY,
  restaurants: [],
};

const SessionContext = createContext<SessionState>({ kind: "unavailable" });

export function RoleSessionProvider({
  tokensUrl,
  mocks,
  children,
}: {
  tokensUrl?: string;
  mocks: boolean;
  children: ReactNode;
}) {
  const [state, setState] = useState<SessionState>(() =>
    mocks
      ? { kind: "ready", session: mockRoleSession }
      : tokensUrl
        ? { kind: "loading" }
        : { kind: "unavailable" },
  );

  useEffect(() => {
    if (mocks || !tokensUrl) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;

    async function load() {
      try {
        const response = await fetch(tokensUrl as string);
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const session = parseRoleSession(await response.json());
        if (stopped) return;
        setState({ kind: "ready", session });
        timer = setTimeout(
          load,
          Math.max(
            session.expiresAt - Date.now() - refreshMarginMs,
            retryDelayMs,
          ),
        );
      } catch {
        if (stopped) return;
        setState((current) =>
          current.kind === "ready" ? current : { kind: "unavailable" },
        );
        timer = setTimeout(load, retryDelayMs);
      }
    }

    void load();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [mocks, tokensUrl]);

  return (
    <SessionContext.Provider value={state}>{children}</SessionContext.Provider>
  );
}

export function useRoleSession(): SessionState {
  return useContext(SessionContext);
}

// Whether the board can call BFF as role: a demo token or a signed-in user.
export function useCanActAs(role: Role): boolean {
  const session = useRoleSession();
  const signedIn = useSignedInToken(role);
  return session.kind === "ready" || signedIn !== undefined;
}

type ClientSettings = Readonly<{
  url: string;
  reportError?: (report: GraphqlErrorReport) => void;
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

// Everything inside calls BFF as the user of role. Each role has its own
// client, so cached data of one user never shows up in another column.
export function AsRole({
  as: role,
  children,
}: {
  as: Role;
  children: ReactNode;
}) {
  const settings = useContext(ClientSettingsContext);
  if (!settings) throw new Error("AsRole needs RoleClientSettings");
  const session = useRoleSession();
  // A user signed in through the identity provider replaces the demo user.
  const signedIn = useSignedInToken(role);
  const token =
    signedIn ??
    (session.kind === "ready" ? session.session.tokens[role] : undefined);

  const client = useMemo(
    () =>
      createGraphqlClient({
        url: settings.url,
        reportError: settings.reportError,
        getAccessToken: () => token,
      }),
    [settings, token],
  );

  return <UrqlProvider value={client}>{children}</UrqlProvider>;
}
