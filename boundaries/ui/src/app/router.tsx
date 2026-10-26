import { withFaroRouterInstrumentation } from "@grafana/faro-react";
import {
  createBrowserRouter,
  createMemoryRouter,
  Navigate,
  RouterProvider,
} from "react-router-dom";
import { MarketGate } from "../features/choose-market";
import { FlowBoard } from "../features/flow-board";
import { SignInCallback } from "../shared/auth/sign-in";
import { AppLayout } from "./layout";

// The whole flow lives on one screen; anything else leads back to it.
export const routes = [
  {
    path: "/",
    element: <AppLayout />,
    children: [
      // The board is market-scoped (ADR 41): first the viewer picks a Market.
      {
        index: true,
        element: (
          <MarketGate>
            <FlowBoard />
          </MarketGate>
        ),
      },
      // Redirect URI of the browser login (ADR 35).
      { path: "auth/callback", element: <SignInCallback /> },
      { path: "*", element: <Navigate replace to="/" /> },
    ],
  },
];

// Created on first render, after initTelemetry: Faro instruments the live router.
let browserRouter: ReturnType<typeof createBrowserRouter> | undefined;

function getBrowserRouter() {
  browserRouter ??= withFaroRouterInstrumentation(createBrowserRouter(routes));
  return browserRouter;
}

export function AppRouter({ initialEntries }: { initialEntries?: string[] }) {
  const router = initialEntries
    ? createMemoryRouter(routes, { initialEntries })
    : getBrowserRouter();
  return <RouterProvider router={router} />;
}
