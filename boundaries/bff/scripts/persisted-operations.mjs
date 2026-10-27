// Persisted operations BFF accepts (local ADR 3): the Router of every Cell
// serves only these operations and blocks the rest. It reads them from the
// file system, one persisted-operations/<sha256>.json ({version, body}) per
// operation; the Router 0.363 cannot load a whole manifest from files.
// Sources:
//   - persisted-documents.json of the UI codegen (hex SHA-256 -> text);
//   - ops/kind/e2e/operations/*.graphql, the operations of the kind e2e tests,
//     hashed without trailing newlines like ops/kind/scripts/graphql-body.sh.
// The set only grows: files of operations no source has any more stay, so a
// Router with the new set still serves the UI release before it. Stale
// operations are removed by hand once no released client sends them.
//
// Usage: node scripts/persisted-operations.mjs [--check]
//   --check fails when a source operation has no committed file or a file is
//   not the SHA-256 of its body.
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const bff = join(dirname(fileURLToPath(import.meta.url)), "..");
const root = join(bff, "..", "..");
const uiDocuments = join(
  root,
  "boundaries/ui/src/shared/graphql/generated/persisted-documents.json",
);
const e2eOperations = join(root, "ops/kind/e2e/operations");
const operationsDir = join(bff, "persisted-operations");

const sha256 = (text) => createHash("sha256").update(text).digest("hex");
const fileContent = (body) =>
  `${JSON.stringify({ version: 1, body }, null, 2)}\n`;

const sources = new Map();
const add = (hash, body, source) => {
  if (sha256(body) !== hash) {
    throw new Error(`${source}: ${hash} is not the SHA-256 of its text`);
  }
  sources.set(hash, body);
};
for (const [hash, body] of Object.entries(
  JSON.parse(readFileSync(uiDocuments, "utf8")),
)) {
  add(hash, body, uiDocuments);
}
for (const file of readdirSync(e2eOperations).filter((name) =>
  name.endsWith(".graphql"),
)) {
  const body = readFileSync(join(e2eOperations, file), "utf8").replace(
    /\n+$/,
    "",
  );
  add(sha256(body), body, join(e2eOperations, file));
}

mkdirSync(operationsDir, { recursive: true });
const committed = readdirSync(operationsDir).filter((name) =>
  name.endsWith(".json"),
);
for (const file of committed) {
  const { version, body } = JSON.parse(
    readFileSync(join(operationsDir, file), "utf8"),
  );
  if (version !== 1 || sha256(body) !== file.replace(/\.json$/, "")) {
    throw new Error(`${join(operationsDir, file)} is not an operation file`);
  }
}

const missing = [...sources.entries()].filter(
  ([hash, body]) =>
    !existsSync(join(operationsDir, `${hash}.json`)) ||
    readFileSync(join(operationsDir, `${hash}.json`), "utf8") !==
      fileContent(body),
);
if (process.argv.includes("--check")) {
  if (missing.length > 0) {
    console.error(
      `${operationsDir} misses ${missing.length} operations: run npm run operations in boundaries/bff`,
    );
    process.exit(1);
  }
} else {
  for (const [hash, body] of missing) {
    writeFileSync(join(operationsDir, `${hash}.json`), fileContent(body));
  }
  console.log(
    `${operationsDir}: ${missing.length} added, ${committed.length + missing.length} operations`,
  );
}
