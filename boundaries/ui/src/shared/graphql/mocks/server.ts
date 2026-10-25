import { setupServer } from "msw/node";

import { graphqlHandlers } from "./handlers";

export const mockServer = setupServer(...graphqlHandlers);
