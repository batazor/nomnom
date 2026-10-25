import {
  createBrowserRouter,
  createMemoryRouter,
  RouterProvider,
} from "react-router-dom";

import { AppLayout } from "./layout";
import { BrowseRestaurantsPage } from "../features/browse-restaurants/browse-restaurants-page";
import { ManageRestaurantPage } from "../features/manage-restaurant/manage-restaurant-page";
import { ViewOrderPage } from "../features/view-order/view-order-page";
import { ViewRestaurantPage } from "../features/view-restaurant/view-restaurant-page";

export const routes = [
  {
    path: "/",
    element: <AppLayout />,
    children: [
      { index: true, element: <BrowseRestaurantsPage /> },
      { path: "restaurants/:restaurantId", element: <ViewRestaurantPage /> },
      { path: "orders/:orderId", element: <ViewOrderPage /> },
      {
        path: "manage/restaurants/:restaurantId",
        element: <ManageRestaurantPage />,
      },
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
