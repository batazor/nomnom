import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useNavigate } from "react-router-dom";

import { useI18n } from "../i18n/i18n";
import { Button } from "../ui";
import {
  beginLogin,
  completeLogin,
  type OidcConfig,
  type OidcLogin,
  refreshLogin,
} from "./oidc";
import "./sign-in.css";

// Users signed in through the identity provider, by the role they act as on
// the board. Access tokens stay in memory (ADR 35); the refresh token of each
// role is kept in sessionStorage, so a reload restores the sign-in and closing
// the tab ends it.
type SignIns = Readonly<Record<string, OidcLogin>>;

type StoredSignIn = Readonly<{ refreshToken: string; user: string }>;

type SignInState = Readonly<{
  config?: OidcConfig;
  signIns: SignIns;
  // Roles whose stored sign-in is being restored after a reload.
  restoring: ReadonlySet<string>;
  add: (login: OidcLogin) => void;
  remove: (role: string) => void;
}>;

const storageKey = "nomnom.oidc.sessions";
// Access tokens are renewed this long before they expire.
const refreshMarginMs = 60_000;

const SignInContext = createContext<SignInState>({
  signIns: {},
  restoring: new Set(),
  add() {},
  remove() {},
});

// Storage may be unavailable (private mode, blocked site data): the sign-in
// then lasts as long as the page.
function readStored(): Record<string, StoredSignIn> {
  try {
    return JSON.parse(sessionStorage.getItem(storageKey) ?? "{}") as Record<
      string,
      StoredSignIn
    >;
  } catch {
    return {};
  }
}

function writeStored(role: string, value: StoredSignIn | undefined) {
  try {
    const { [role]: _, ...rest } = readStored();
    sessionStorage.setItem(
      storageKey,
      JSON.stringify(value ? { ...rest, [role]: value } : rest),
    );
  } catch {
    // Not persisted; the in-memory sign-in still works.
  }
}

export function SignInProvider({
  config,
  children,
}: {
  config?: OidcConfig;
  children: ReactNode;
}) {
  const [signIns, setSignIns] = useState<SignIns>({});
  const [restoring, setRestoring] = useState<ReadonlySet<string>>(
    () => new Set(config ? Object.keys(readStored()) : []),
  );

  const actions = useMemo(() => {
    function remove(role: string) {
      writeStored(role, undefined);
      setSignIns((current) => {
        const { [role]: _, ...rest } = current;
        return rest;
      });
    }
    function add(login: OidcLogin) {
      writeStored(
        login.purpose,
        login.refreshToken
          ? { refreshToken: login.refreshToken, user: login.user }
          : undefined,
      );
      setSignIns((current) => ({ ...current, [login.purpose]: login }));
    }
    return { add, remove };
  }, []);

  // After a reload: exchange each stored refresh token for a fresh login.
  useEffect(() => {
    if (!config) return;
    let stopped = false;
    for (const [role, stored] of Object.entries(readStored())) {
      refreshLogin(config, stored.refreshToken, role)
        .then((login) => {
          if (!stopped) actions.add(login);
        })
        .catch(() => {
          if (!stopped) actions.remove(role);
        })
        .finally(() => {
          if (stopped) return;
          setRestoring((current) => {
            const next = new Set(current);
            next.delete(role);
            return next;
          });
        });
    }
    return () => {
      stopped = true;
    };
  }, [config, actions]);

  // Renew every access token before it expires; a failed renewal signs the
  // role out, so the board never calls BFF with an expired token.
  useEffect(() => {
    if (!config) return;
    const timers = Object.values(signIns).map((login) =>
      setTimeout(
        () => {
          if (!login.refreshToken) {
            actions.remove(login.purpose);
            return;
          }
          refreshLogin(config, login.refreshToken, login.purpose)
            .then(actions.add)
            .catch(() => actions.remove(login.purpose));
        },
        Math.max(login.expiresAt - Date.now() - refreshMarginMs, 0),
      ),
    );
    return () => {
      for (const timer of timers) clearTimeout(timer);
    };
  }, [config, signIns, actions]);

  const state = useMemo<SignInState>(
    () => ({ config, signIns, restoring, ...actions }),
    [config, signIns, restoring, actions],
  );
  return (
    <SignInContext.Provider value={state}>{children}</SignInContext.Provider>
  );
}

// Whether users can sign in through the identity provider.
export function useSignInAvailable(): boolean {
  return useContext(SignInContext).config !== undefined;
}

// Access token of the user signed in as role, while it is valid.
export function useSignedInToken(role: string): string | undefined {
  const { signIns } = useContext(SignInContext);
  const login = signIns[role];
  return login && login.expiresAt > Date.now() ? login.accessToken : undefined;
}

// Sign in or out as role; nothing when login is not configured.
export function SignInButton({
  role,
  roleLabel,
}: {
  role: string;
  roleLabel: string;
}) {
  const { t } = useI18n();
  const { config, signIns, restoring, remove } = useContext(SignInContext);
  const [pending, setPending] = useState(false);
  if (!config) return null;
  const login = signIns[role];
  const demo = config.demoEmailDomain ? (
    <span className="sign-in-demo">
      {t("auth.demo", {
        email: `${role}@${config.demoEmailDomain}`,
        password: config.demoPassword ?? "",
      })}
    </span>
  ) : null;

  if (login) {
    return (
      <span className="sign-in">
        <span className="sign-in-user">
          {t("auth.signedInAs", { role: roleLabel, user: login.user })}
        </span>
        <Button onPress={() => remove(role)} variant="secondary">
          {t("auth.signOut")}
        </Button>
      </span>
    );
  }

  return (
    <span className="sign-in">
      <Button
        isPending={pending || restoring.has(role)}
        onPress={async () => {
          setPending(true);
          try {
            window.location.assign(
              await beginLogin(config, role, window.location.pathname),
            );
          } catch {
            setPending(false);
          }
        }}
        variant="secondary"
      >
        {t("auth.signIn", { role: roleLabel })}
      </Button>
      {demo}
    </span>
  );
}

// Route of the redirect URI: finishes the login and returns to the page that
// started it.
export function SignInCallback() {
  const { t } = useI18n();
  const { config, add } = useContext(SignInContext);
  const navigate = useNavigate();
  const [failed, setFailed] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    // StrictMode runs effects twice; the code can be exchanged only once.
    if (started.current || !config) return;
    started.current = true;
    completeLogin(config, window.location.href)
      .then((login) => {
        add(login);
        navigate(login.returnTo, { replace: true });
      })
      .catch(() => setFailed(true));
  }, [config, add, navigate]);

  return (
    <p role="status">
      {failed || !config ? t("auth.failed") : t("auth.signingIn")}
    </p>
  );
}
