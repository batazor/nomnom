import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: process.env.GRAPHQL_SCHEMA ?? "../bff/plugins/bff/src/schema.graphql",
  documents: ["src/features/**/*.graphql"],
  ignoreNoDocuments: true,
  generates: {
    "src/shared/graphql/generated/": {
      preset: "client",
    },
  },
};

export default config;
