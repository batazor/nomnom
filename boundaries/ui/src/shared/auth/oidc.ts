// Browser login with OIDC authorization code + PKCE (ADR 35): a public client,
// no client secret. The access token lives only in page memory and is renewed
// with the refresh token (offline_access, rotated by Dex on every use); the
// PKCE verifier and state survive the redirect in sessionStorage.

export type OidcConfig = Readonly<{
  issuer: string;
  clientId: string;
  // Client id of BFF: Dex puts it into aud through a cross-client scope.
  audience: string;
  redirectUri: string;
  // Demo user of kind (demo@domain and its password) shown next to sign-in.
  demoEmailDomain?: string;
  demoPassword?: string;
}>;

export type OidcLogin = Readonly<{
  accessToken: string;
  // Absent when the provider issues none; the login then ends with the token.
  refreshToken?: string;
  // Epoch milliseconds.
  expiresAt: number;
  // Display name of the user: email, falling back to sub.
  user: string;
  // Opaque value given to beginLogin, for example the role being signed in.
  purpose: string;
  returnTo: string;
}>;

type PendingLogin = Readonly<{
  state: string;
  verifier: string;
  purpose: string;
  returnTo: string;
}>;

type Discovery = Readonly<{
  authorization_endpoint: string;
  token_endpoint: string;
}>;

const pendingKey = "nomnom.oidc.pending";

function base64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/, "");
}

function randomString(): string {
  return base64Url(crypto.getRandomValues(new Uint8Array(32)));
}

export async function codeChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier),
  );
  return base64Url(new Uint8Array(digest));
}

async function discover(
  issuer: string,
  fetchFn: typeof fetch,
): Promise<Discovery> {
  const response = await fetchFn(
    `${issuer.replace(/\/$/, "")}/.well-known/openid-configuration`,
  );
  if (!response.ok) throw new Error(`OIDC discovery: HTTP ${response.status}`);
  return (await response.json()) as Discovery;
}

// Claims of a JWT payload, without verification: BFF verifies the token, the
// page only reads its expiry and a display name.
export function tokenClaims(token: string): Record<string, unknown> {
  const payload = token.split(".")[1] ?? "";
  const json = atob(payload.replaceAll("-", "+").replaceAll("_", "/"));
  return JSON.parse(
    new TextDecoder().decode(Uint8Array.from(json, (c) => c.charCodeAt(0))),
  ) as Record<string, unknown>;
}

// The authorization URL to send the browser to. prompt=login asks for a
// password every time, so one browser can sign in several users.
export async function beginLogin(
  config: OidcConfig,
  purpose: string,
  returnTo: string,
  fetchFn: typeof fetch = fetch,
): Promise<string> {
  const { authorization_endpoint } = await discover(config.issuer, fetchFn);
  const pending: PendingLogin = {
    state: randomString(),
    verifier: randomString(),
    purpose,
    returnTo,
  };
  sessionStorage.setItem(pendingKey, JSON.stringify(pending));
  const url = new URL(authorization_endpoint);
  url.search = new URLSearchParams({
    client_id: config.clientId,
    response_type: "code",
    redirect_uri: config.redirectUri,
    // No nonce: Dex would copy it into the access token, and BFF rejects
    // tokens with nonce as ID tokens (ADR 35).
    scope: `openid email offline_access audience:server:client_id:${config.audience}`,
    state: pending.state,
    code_challenge: await codeChallenge(pending.verifier),
    code_challenge_method: "S256",
    prompt: "login",
  }).toString();
  return url.toString();
}

// Exchanges the code of the callback URL for tokens. The pending login is
// consumed first, so a reloaded callback cannot replay it.
export async function completeLogin(
  config: OidcConfig,
  callbackUrl: string,
  fetchFn: typeof fetch = fetch,
): Promise<OidcLogin> {
  const raw = sessionStorage.getItem(pendingKey);
  sessionStorage.removeItem(pendingKey);
  if (!raw) throw new Error("No login in progress");
  const pending = JSON.parse(raw) as PendingLogin;
  const params = new URL(callbackUrl).searchParams;
  const error = params.get("error");
  if (error) throw new Error(`Login failed: ${error}`);
  const code = params.get("code");
  if (!code || params.get("state") !== pending.state) {
    throw new Error("Login callback does not match the login in progress");
  }

  const { token_endpoint } = await discover(config.issuer, fetchFn);
  const response = await fetchFn(token_endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      client_id: config.clientId,
      code,
      redirect_uri: config.redirectUri,
      code_verifier: pending.verifier,
    }),
  });
  return tokenLogin(response, pending.purpose, pending.returnTo);
}

// Renews the access token of purpose. The provider rotates the refresh token:
// the returned login carries the new one, the old one is spent.
export async function refreshLogin(
  config: OidcConfig,
  refreshToken: string,
  purpose: string,
  fetchFn: typeof fetch = fetch,
): Promise<OidcLogin> {
  const { token_endpoint } = await discover(config.issuer, fetchFn);
  const response = await fetchFn(token_endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "refresh_token",
      client_id: config.clientId,
      refresh_token: refreshToken,
    }),
  });
  return tokenLogin(response, purpose, "/");
}

// Signs in the demo user of kind in one step, without the provider's form:
// the password grant, which Dex allows there for the board's demo button.
export async function demoLogin(
  config: OidcConfig,
  purpose: string,
  fetchFn: typeof fetch = fetch,
): Promise<OidcLogin> {
  if (!config.demoEmailDomain || !config.demoPassword) {
    throw new Error("No demo user configured");
  }
  const { token_endpoint } = await discover(config.issuer, fetchFn);
  const response = await fetchFn(token_endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "password",
      client_id: config.clientId,
      username: `${purpose}@${config.demoEmailDomain}`,
      password: config.demoPassword,
      scope: `openid email offline_access audience:server:client_id:${config.audience}`,
    }),
  });
  return tokenLogin(response, purpose, "/");
}

async function tokenLogin(
  response: Response,
  purpose: string,
  returnTo: string,
): Promise<OidcLogin> {
  if (!response.ok) throw new Error(`Token request: HTTP ${response.status}`);
  const body = (await response.json()) as {
    access_token?: unknown;
    refresh_token?: unknown;
  };
  if (typeof body.access_token !== "string") {
    throw new Error("Token response has no access_token");
  }
  const claims = tokenClaims(body.access_token);
  return {
    accessToken: body.access_token,
    refreshToken:
      typeof body.refresh_token === "string" ? body.refresh_token : undefined,
    expiresAt: Number(claims.exp) * 1000,
    user: String(claims.email ?? claims.sub ?? ""),
    purpose,
    returnTo,
  };
}
