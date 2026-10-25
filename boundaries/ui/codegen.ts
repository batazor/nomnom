import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../bff/client-schema.graphql",
  documents: ["src/features/**/*.graphql"],
  ignoreNoDocuments: true,
  generates: {
    "src/shared/graphql/generated/": {
      preset: "client",
      // Fragments only share selections between a query and its subscription.
      presetConfig: { fragmentMasking: false },
    },
  },
};

export default config;
