import {
  Menu,
  MenuButton,
  MenuItem,
  MenuItems,
  MenuSeparator,
} from "@headlessui/react";
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
  demoLogin,
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

// The board signs in one demo user, who acts as every role on it.
export const boardUser = "demo";

// Access token of the user signed in as role, while it is valid.
export function useSignedInToken(role: string): string | undefined {
  const { signIns } = useContext(SignInContext);
  const login = signIns[role];
  return login && login.expiresAt > Date.now() ? login.accessToken : undefined;
}

// Initials of a user for the avatar: the first letters of the dotted parts
// of the email name ("anna.ivanova@…" → "AI"), else its first letter.
function initials(user: string): string {
  const name = user.split("@")[0];
  const parts = name.split(/[._-]+/).filter(Boolean);
  const letters =
    parts.length > 1 ? `${parts[0][0]}${parts[1][0]}` : name.slice(0, 1);
  return letters.toUpperCase();
}

// The account in the top bar: a sign-in button, or the avatar of the signed-in
// user with a menu to sign out; nothing when login is not configured.
export function AccountMenu({
  role,
  roleLabel,
}: {
  role: string;
  roleLabel: string;
}) {
  const { t } = useI18n();
  const { config, signIns, restoring, add, remove } = useContext(SignInContext);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  if (!config) return null;
  const login = signIns[role];
  if (login) {
    const letters = initials(login.user);
    return (
      <Menu as="div" className="account-menu">
        <MenuButton
          aria-label={t("auth.account", { user: login.user })}
          className="account-avatar"
        >
          {letters}
        </MenuButton>
        <MenuItems anchor="bottom end" className="account-menu-items">
          <div className="account-menu-header">
            <span aria-hidden="true" className="account-avatar" data-static>
              {letters}
            </span>
            <span className="account-menu-who">
              <span className="account-menu-user">{login.user}</span>
              <span className="account-menu-role">{roleLabel}</span>
            </span>
          </div>
          <MenuSeparator className="account-menu-separator" />
          <MenuItem>
            <button
              className="account-menu-item"
              onClick={() => remove(role)}
              type="button"
            >
              <svg
                aria-hidden="true"
                fill="none"
                focusable="false"
                viewBox="0 0 16 16"
              >
                <path
                  d="M6.5 2.75H4.25a1.5 1.5 0 0 0-1.5 1.5v7.5a1.5 1.5 0 0 0 1.5 1.5H6.5M10.5 5.25 13.25 8l-2.75 2.75M13 8H6.25"
                  stroke="currentColor"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth="1.25"
                />
              </svg>
              {t("auth.signOut")}
            </button>
          </MenuItem>
        </MenuItems>
      </Menu>
    );
  }

  // The demo user of kind signs in with one press: no provider form to fill.
  const demo = config.demoEmailDomain && config.demoPassword;
  async function signIn() {
    if (!config) return;
    setPending(true);
    try {
      if (demo) {
        add(await demoLogin(config, role));
        setPending(false);
      } else {
        window.location.assign(
          await beginLogin(config, role, window.location.pathname),
        );
      }
    } catch {
      setPending(false);
      setFailed(true);
    }
  }

  return (
    <span className="sign-in">
      <Button isPending={pending || restoring.has(role)} onClick={signIn}>
        {demo ? t("auth.signInDemo") : t("auth.signIn", { role: roleLabel })}
      </Button>
      {failed ? (
        <span className="sign-in-error" role="alert">
          {t("auth.failedNow")}
        </span>
      ) : null}
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
