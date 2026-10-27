import { createHash } from "node:crypto";

import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../bff/client-schema.graphql",
  documents: ["src/features/**/*.graphql"],
  ignoreNoDocuments: true,
  generates: {
    "src/shared/graphql/generated/": {
      preset: "client",
      // Fragments only share selections between a query and its subscription.
      // persistedDocuments: the hex SHA-256 of every operation text in its
      // document and persisted-documents.json (hash -> text), from which
      // BFF builds its operation manifest (ADR 3 of BFF). A bare 64-character
      // hex id is checked by the Router against the text.
      presetConfig: {
        fragmentMasking: false,
        persistedDocuments: {
          hashAlgorithm: (operation: string) =>
            createHash("sha256").update(operation).digest("hex"),
        },
      },
    },
  },
};

export default config;
