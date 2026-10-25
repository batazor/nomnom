import { beforeEach, describe, expect, it } from "vitest";

import {
  beginLogin,
  codeChallenge,
  completeLogin,
  type OidcConfig,
} from "./oidc";

const config: OidcConfig = {
  issuer: "http://localhost:5556/dex",
  clientId: "nomnom-ui",
  audience: "nomnom-bff",
  redirectUri: "http://localhost:4173/auth/callback",
};

function jwt(claims: Record<string, unknown>): string {
  const part = (value: unknown) =>
    btoa(JSON.stringify(value)).replaceAll("=", "");
  return `${part({ alg: "RS256" })}.${part(claims)}.sig`;
}

type Call = { url: string; body?: URLSearchParams };

function fakeIdP(calls: Call[], accessToken: string): typeof fetch {
  return async (input, init) => {
    const url = String(input);
    calls.push({ url, body: init?.body as URLSearchParams | undefined });
    if (url.endsWith("/.well-known/openid-configuration")) {
      return Response.json({
        authorization_endpoint: `${config.issuer}/auth`,
        token_endpoint: `${config.issuer}/token`,
      });
    }
    return Response.json({ access_token: accessToken, id_token: "id" });
  };
}

describe("OIDC authorization code + PKCE", () => {
  beforeEach(() => sessionStorage.clear());

  it("follows RFC 7636 for the S256 challenge", async () => {
    expect(
      await codeChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"),
    ).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  });

  it("asks for the BFF audience without a nonce and exchanges the code", async () => {
    const calls: Call[] = [];
    const token = jwt({ exp: 2_000_000_000, email: "owner@nomnom.test" });
    const idp = fakeIdP(calls, token);

    const authorize = new URL(await beginLogin(config, "owner", "/board", idp));
    expect(authorize.origin + authorize.pathname).toBe(`${config.issuer}/auth`);
    const params = authorize.searchParams;
    expect(params.get("scope")).toBe(
      "openid email audience:server:client_id:nomnom-bff",
    );
    expect(params.get("code_challenge_method")).toBe("S256");
    expect(params.has("nonce")).toBe(false);

    const login = await completeLogin(
      config,
      `${config.redirectUri}?code=abc&state=${params.get("state")}`,
      idp,
    );
    expect(login).toEqual({
      accessToken: token,
      expiresAt: 2_000_000_000_000,
      user: "owner@nomnom.test",
      purpose: "owner",
      returnTo: "/board",
    });
    const exchange = calls.at(-1)?.body;
    expect(exchange?.get("code")).toBe("abc");
    expect(await codeChallenge(exchange?.get("code_verifier") ?? "")).toBe(
      params.get("code_challenge"),
    );
  });

  it("rejects a callback with another state and cannot be replayed", async () => {
    const idp = fakeIdP([], jwt({ exp: 1 }));
    await beginLogin(config, "owner", "/", idp);
    await expect(
      completeLogin(config, `${config.redirectUri}?code=abc&state=forged`, idp),
    ).rejects.toThrow("does not match");
    await expect(
      completeLogin(config, `${config.redirectUri}?code=abc&state=forged`, idp),
    ).rejects.toThrow("No login in progress");
  });
});
