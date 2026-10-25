import { useCallback, useEffect, useState } from "react";
import { useQuery } from "urql";

import {
  BrowseRestaurantsDocument,
  type BrowseRestaurantsQuery,
  type BrowseRestaurantsQueryVariables,
} from "../../shared/graphql/generated/graphql";

type Connection = BrowseRestaurantsQuery["restaurants"];
export type ListedRestaurant = Connection["edges"][number]["node"];

export const restaurantsPageSize = 6;

// Cursor of the first page.
const firstPage = "";

// Loads the catalog page by page: every "show more" requests the page after
// the last loaded endCursor and appends it to the pages already shown.
export function useRestaurantPages() {
  const [cursors, setCursors] = useState<string[]>([firstPage]);
  const [pages, setPages] = useState<ReadonlyMap<string, Connection>>(
    () => new Map(),
  );
  const after = cursors[cursors.length - 1];
  const [result, reexecute] = useQuery({
    query: BrowseRestaurantsDocument,
    variables: { first: restaurantsPageSize, after: after || null },
  });

  const { data, fetching, error, operation } = result;

  useEffect(() => {
    if (fetching || !data) return;
    const variables = operation?.variables as
      | BrowseRestaurantsQueryVariables
      | undefined;
    const cursor = variables?.after ?? firstPage;
    setPages((current) => new Map(current).set(cursor, data.restaurants));
  }, [data, fetching, operation]);

  const restaurants = cursors.flatMap(
    (cursor) => pages.get(cursor)?.edges.map((edge) => edge.node) ?? [],
  );
  const lastPage = pages.get(after);

  const loadMore = useCallback(() => {
    const endCursor = lastPage?.pageInfo.endCursor;
    if (!endCursor) return;
    setCursors((current) => [...current, endCursor]);
  }, [lastPage]);

  const retry = useCallback(
    () => reexecute({ requestPolicy: "network-only" }),
    [reexecute],
  );

  return {
    restaurants,
    // Restaurants of the pages before the one being loaded or shown last.
    loadedBefore: cursors
      .slice(0, -1)
      .reduce(
        (count, cursor) => count + (pages.get(cursor)?.edges.length ?? 0),
        0,
      ),
    initialLoading: cursors.length === 1 && !lastPage && !error,
    initialFailed: cursors.length === 1 && !lastPage && Boolean(error),
    loadingMore: cursors.length > 1 && !lastPage && !error,
    moreFailed: cursors.length > 1 && !lastPage && Boolean(error),
    hasMore: Boolean(lastPage?.pageInfo.hasNextPage),
    loadMore,
    retry,
  };
}
