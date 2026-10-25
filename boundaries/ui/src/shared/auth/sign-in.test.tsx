import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { OidcConfig } from "./oidc";
import { SignInProvider, useSignedInToken } from "./sign-in";

const config: OidcConfig = {
  issuer: "http://localhost:5556/dex",
  clientId: "nomnom-ui",
  audience: "nomnom-bff",
  redirectUri: "http://localhost:4173/auth/callback",
};

const storageKey = "nomnom.oidc.sessions";

function jwt(claims: Record<string, unknown>): string {
  const part = (value: unknown) =>
    btoa(JSON.stringify(value)).replaceAll("=", "");
  return `${part({ alg: "RS256" })}.${part(claims)}.sig`;
}

function idp(tokenResponse: () => Response): typeof fetch {
  return vi.fn(async (input: RequestInfo | URL) =>
    String(input).endsWith("openid-configuration")
      ? Response.json({
          authorization_endpoint: `${config.issuer}/auth`,
          token_endpoint: `${config.issuer}/token`,
        })
      : tokenResponse(),
  ) as typeof fetch;
}

function OwnerToken() {
  return <p>{useSignedInToken("owner") ?? "signed out"}</p>;
}

describe("sign-in session", () => {
  beforeEach(() => sessionStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it("restores a sign-in after a reload with the stored refresh token", async () => {
    const token = jwt({ exp: Date.now() / 1000 + 3600, email: "owner@x" });
    sessionStorage.setItem(
      storageKey,
      JSON.stringify({ owner: { refreshToken: "refresh-1", user: "owner@x" } }),
    );
    vi.stubGlobal(
      "fetch",
      idp(() =>
        Response.json({ access_token: token, refresh_token: "refresh-2" }),
      ),
    );

    render(
      <SignInProvider config={config}>
        <OwnerToken />
      </SignInProvider>,
    );

    expect(await screen.findByText(token)).toBeInTheDocument();
    // The rotated refresh token replaces the spent one.
    expect(JSON.parse(sessionStorage.getItem(storageKey) ?? "")).toEqual({
      owner: { refreshToken: "refresh-2", user: "owner@x" },
    });
  });

  it("forgets a sign-in whose refresh token is rejected", async () => {
    sessionStorage.setItem(
      storageKey,
      JSON.stringify({ owner: { refreshToken: "spent", user: "owner@x" } }),
    );
    vi.stubGlobal(
      "fetch",
      idp(() => new Response("invalid_grant", { status: 400 })),
    );

    render(
      <SignInProvider config={config}>
        <OwnerToken />
      </SignInProvider>,
    );

    await waitFor(() =>
      expect(JSON.parse(sessionStorage.getItem(storageKey) ?? "")).toEqual({}),
    );
    expect(screen.getByText("signed out")).toBeInTheDocument();
  });
});
