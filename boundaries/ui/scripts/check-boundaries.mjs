import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const uiRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const sourceRoot = join(uiRoot, "src");
const sourceExtensions = new Set([".ts", ".tsx", ".js", ".jsx"]);
const forbiddenBackendImports = [
  /(?:from\s+|import\s*\()["'][^"']*(?:boundaries|bff\/plugins|grpc)[^"']*["']/,
  /(?:from\s+|import\s*\()["'](?:kafkajs|@temporalio\/|pg|mongodb)[^"']*["']/,
];

function sourceFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return sourceExtensions.has(extname(path)) ? [path] : [];
  });
}

export function checkSource(filename, source) {
  const violations = [];
  const normalized = filename.replaceAll("\\", "/");

  if (!normalized.includes("/shared/ui/") && /["']@astra-rn\//.test(source)) {
    violations.push("Astra must be imported through src/shared/ui");
  }

  if (forbiddenBackendImports.some((pattern) => pattern.test(source))) {
    violations.push("Browser code may only call the public GraphQL API");
  }

  return violations;
}

function runSelfTest() {
  assert.deepEqual(
    checkSource("src/app/bad.ts", 'import {Button} from "@astra-rn/react";'),
    ["Astra must be imported through src/shared/ui"],
  );
  assert.deepEqual(
    checkSource(
      "src/app/bad.ts",
      'import client from "../../boundaries/orders";',
    ),
    ["Browser code may only call the public GraphQL API"],
  );
  assert.deepEqual(
    checkSource(
      "src/shared/ui/button.tsx",
      'import {Button} from "@astra-rn/react/button";',
    ),
    [],
  );
}

function runCheck() {
  const violations = sourceFiles(sourceRoot).flatMap((filename) =>
    checkSource(filename, readFileSync(filename, "utf8")).map(
      (message) => `${relative(uiRoot, filename)}: ${message}`,
    ),
  );

  if (violations.length > 0) {
    console.error(violations.join("\n"));
    process.exitCode = 1;
  }
}

if (process.argv.includes("--self-test")) runSelfTest();
else if (statSync(sourceRoot).isDirectory()) runCheck();
