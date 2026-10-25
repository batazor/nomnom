import { beforeEach, describe, expect, it } from "vitest";

import {
  beginLogin,
  codeChallenge,
  completeLogin,
  demoLogin,
  type OidcConfig,
  refreshLogin,
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
    return Response.json({
      access_token: accessToken,
      id_token: "id",
      refresh_token: "refresh-2",
    });
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
      "openid email offline_access audience:server:client_id:nomnom-bff",
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
      refreshToken: "refresh-2",
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

describe("refresh", () => {
  it("renews the access token and keeps the rotated refresh token", async () => {
    const calls: Call[] = [];
    const token = jwt({ exp: 2_000_000_000, sub: "owner-sub" });
    const login = await refreshLogin(
      config,
      "refresh-1",
      "owner",
      fakeIdP(calls, token),
    );
    const body = calls.at(-1)?.body;
    expect(body?.get("grant_type")).toBe("refresh_token");
    expect(body?.get("refresh_token")).toBe("refresh-1");
    expect(login).toMatchObject({
      accessToken: token,
      refreshToken: "refresh-2",
      user: "owner-sub",
      purpose: "owner",
    });
  });

  it("fails when the provider rejects the refresh token", async () => {
    const rejecting: typeof fetch = async (input) =>
      String(input).endsWith("openid-configuration")
        ? Response.json({
            authorization_endpoint: `${config.issuer}/auth`,
            token_endpoint: `${config.issuer}/token`,
          })
        : new Response("invalid_grant", { status: 400 });
    await expect(
      refreshLogin(config, "spent", "owner", rejecting),
    ).rejects.toThrow("HTTP 400");
  });
});

describe("demo sign-in", () => {
  it("signs in the demo user with one password grant", async () => {
    const calls: Call[] = [];
    const token = jwt({ exp: 2_000_000_000, email: "demo@nomnom.test" });
    const login = await demoLogin(
      { ...config, demoEmailDomain: "nomnom.test", demoPassword: "nomnom" },
      "demo",
      fakeIdP(calls, token),
    );
    const body = calls.at(-1)?.body;
    expect(calls.at(-1)?.url).toBe(`${config.issuer}/token`);
    expect(body?.get("grant_type")).toBe("password");
    expect(body?.get("username")).toBe("demo@nomnom.test");
    expect(body?.get("password")).toBe("nomnom");
    expect(body?.get("scope")).toContain(
      "audience:server:client_id:nomnom-bff",
    );
    expect(login).toMatchObject({
      accessToken: token,
      refreshToken: "refresh-2",
      user: "demo@nomnom.test",
      purpose: "demo",
    });
  });

  it("needs a configured demo user", async () => {
    await expect(demoLogin(config, "demo", fakeIdP([], ""))).rejects.toThrow(
      "No demo user configured",
    );
  });
});
