import {
  createBrowserRouter,
  createMemoryRouter,
  RouterProvider,
} from "react-router-dom";

import { AppLayout } from "./layout";
import { CustomerJourneyPreview } from "../features/customer-journey-preview/customer-journey-preview";
import { ViewOrderPage } from "../features/view-order/view-order-page";

export const routes = [
  {
    path: "/",
    element: <AppLayout />,
    children: [
      { index: true, element: <CustomerJourneyPreview /> },
      { path: "orders/:orderId", element: <ViewOrderPage /> },
    ],
  },
];

export const browserRouter = createBrowserRouter(routes);

export function AppRouter({ initialEntries }: { initialEntries?: string[] }) {
  const router = initialEntries
    ? createMemoryRouter(routes, { initialEntries })
    : browserRouter;
  return <RouterProvider router={router} />;
}
