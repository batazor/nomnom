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
} from "./oidc";
import "./sign-in.css";

// Users signed in through the identity provider, by the role they act as on
// the board. Kept in memory only: a reload signs everyone out (ADR 35).
type SignIns = Readonly<Record<string, OidcLogin>>;

type SignInState = Readonly<{
  config?: OidcConfig;
  signIns: SignIns;
  add: (login: OidcLogin) => void;
  remove: (role: string) => void;
}>;

const SignInContext = createContext<SignInState>({
  signIns: {},
  add() {},
  remove() {},
});

export function SignInProvider({
  config,
  children,
}: {
  config?: OidcConfig;
  children: ReactNode;
}) {
  const [signIns, setSignIns] = useState<SignIns>({});
  const state = useMemo<SignInState>(
    () => ({
      config,
      signIns,
      add: (login) =>
        setSignIns((current) => ({ ...current, [login.purpose]: login })),
      remove: (role) =>
        setSignIns((current) => {
          const { [role]: _, ...rest } = current;
          return rest;
        }),
    }),
    [config, signIns],
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
  const { config, signIns, remove } = useContext(SignInContext);
  const [pending, setPending] = useState(false);
  if (!config) return null;
  const login = signIns[role];

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
    <Button
      isPending={pending}
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
