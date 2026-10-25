import { withFaroRouterInstrumentation } from "@grafana/faro-react";
import {
  createBrowserRouter,
  createMemoryRouter,
  RouterProvider,
} from "react-router-dom";
import { BrowseRestaurantsPage } from "../features/browse-restaurants/browse-restaurants-page";
import { ManageRestaurantPage } from "../features/manage-restaurant/manage-restaurant-page";
import { MyRestaurantsPage } from "../features/manage-restaurant/my-restaurants-page";
import { ViewOrderPage } from "../features/view-order/view-order-page";
import { ViewRestaurantPage } from "../features/view-restaurant/view-restaurant-page";
import { AppLayout } from "./layout";

export const routes = [
  {
    path: "/",
    element: <AppLayout />,
    children: [
      { index: true, element: <BrowseRestaurantsPage /> },
      { path: "restaurants/:restaurantId", element: <ViewRestaurantPage /> },
      { path: "orders/:orderId", element: <ViewOrderPage /> },
      { path: "manage/restaurants", element: <MyRestaurantsPage /> },
      {
        path: "manage/restaurants/:restaurantId",
        element: <ManageRestaurantPage />,
      },
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
