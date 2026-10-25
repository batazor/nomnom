import {
  createBrowserRouter,
  createMemoryRouter,
  RouterProvider,
} from "react-router-dom";

import { AppLayout } from "./layout";
import { CustomerJourneyPreview } from "../features/customer-journey-preview/customer-journey-preview";

export const routes = [
  {
    path: "/",
    element: <AppLayout />,
    children: [{ index: true, element: <CustomerJourneyPreview /> }],
  },
];

export const browserRouter = createBrowserRouter(routes);

export function AppRouter({ initialEntries }: { initialEntries?: string[] }) {
  const router = initialEntries
    ? createMemoryRouter(routes, { initialEntries })
    : browserRouter;
  return <RouterProvider router={router} />;
}
