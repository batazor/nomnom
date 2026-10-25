import schemaSource from "@public-schema?raw";
import { buildSchema } from "graphql";

// The same public BFF schema that codegen reads.
export const publicSchema = buildSchema(schemaSource);
