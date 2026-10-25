import { HttpResponse } from "msw";
import { graphql } from "msw/graphql";

import { bffInfoFixture } from "./fixtures";

const publicGraphql = graphql.link(/\/graphql$/);

export const graphqlHandlers = [
  publicGraphql.query("BffInfo", () =>
    HttpResponse.json({ data: { bffInfo: bffInfoFixture } }),
  ),
];
