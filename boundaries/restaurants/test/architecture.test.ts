// Layer import rules of ADR 6. The checks read the sources, so a returning
// dependency fails `npm run architecture` and `npm test`.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const sourceRoots = ["cmd", "di", "infrastructure", "pkg", "slices"];

type Layer =
  | "domains"
  | "applications"
  | "infrastructure"
  | "pkg"
  | "di"
  | "cmd";

interface SourceImport {
  file: string;
  layer: Layer;
  /** Root-relative path of a local module, or the package specifier. */
  target: string;
  local: boolean;
}

/** Layer of a root-relative path; slice parts are read below `slices/<slice>/`. */
function layerOf(path: string): Layer | undefined {
  const parts = path.split("/");
  const head = parts[0] === "slices" ? parts[2] : parts[0];
  switch (head) {
    case "domains":
    case "applications":
    case "infrastructure":
    case "pkg":
    case "di":
    case "cmd":
      return head;
    default:
      return undefined;
  }
}

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory())
      return entry.name === "gen" ? [] : sourceFiles(path);
    return path.endsWith(".ts") ? [path] : [];
  });
}

const specifiers =
  /(?:^|\n)\s*(?:import|export)\b[^'"]*?from\s*["']([^"']+)["']|import\s*\(\s*["']([^"']+)["']\s*\)|(?:^|\n)\s*import\s*["']([^"']+)["']/g;

function sourceImports(): SourceImport[] {
  const imports = sourceRoots.flatMap((directory) =>
    sourceFiles(join(root, directory)).flatMap((path) => {
      const file = relative(root, path).split(sep).join("/");
      const layer = layerOf(file);
      if (layer === undefined) return [];
      return [...readFileSync(path, "utf8").matchAll(specifiers)].map(
        (match): SourceImport => {
          const specifier = match[1] ?? match[2] ?? match[3];
          const local = specifier.startsWith(".");
          const target = local
            ? relative(root, resolve(dirname(path), specifier))
                .split(sep)
                .join("/")
            : specifier;
          return { file, layer, target, local };
        },
      );
    }),
  );
  assert.ok(imports.length > 0, "no imports found under the source roots");
  return imports;
}

const imports = sourceImports();

function violations(
  layer: Layer,
  allowed: (source: SourceImport) => boolean,
): string[] {
  return imports
    .filter((source) => source.layer === layer && !allowed(source))
    .map((source) => `${source.file} -> ${source.target}`);
}

function localIn(source: SourceImport, ...layers: Layer[]): boolean {
  const layer = layerOf(source.target);
  return source.local && layer !== undefined && layers.includes(layer);
}

const isBuiltin = (source: SourceImport) => source.target.startsWith("node:");

describe("ADR 6 layer imports", () => {
  test("domains depend only on domains, decimal.js and Node built-ins", () => {
    const found = violations(
      "domains",
      (source) =>
        isBuiltin(source) ||
        source.target === "decimal.js" ||
        localIn(source, "domains"),
    );
    assert.deepEqual(found, []);
  });

  test("value objects do not import their aggregate", () => {
    const found = violations("domains", (source) => {
      const [domain, rest] = source.file.split("/vo/");
      return (
        rest === undefined ||
        !source.local ||
        source.target.startsWith(`${domain}/vo/`) ||
        !source.target.startsWith(`${domain}/`)
      );
    });
    assert.deepEqual(found, []);
  });

  test("applications depend only on domains, use cases and pkg/application", () => {
    const found = violations(
      "applications",
      (source) =>
        isBuiltin(source) ||
        localIn(source, "domains", "applications") ||
        (source.local && source.target.startsWith("pkg/application/")),
    );
    assert.deepEqual(found, []);
  });

  test("shared packages do not import slices or processes", () => {
    const found = violations(
      "pkg",
      (source) =>
        !source.local ||
        (!source.target.startsWith("slices/") &&
          !localIn(source, "infrastructure", "di", "cmd")),
    );
    assert.deepEqual(found, []);
  });

  test("infrastructure does not import the composition root", () => {
    const found = violations(
      "infrastructure",
      (source) =>
        !localIn(source, "di", "cmd") &&
        !(source.local && source.target.startsWith("pkg/di/")),
    );
    assert.deepEqual(found, []);
  });

  test("generated code is imported only by adapters and the composition root", () => {
    const found = imports
      .filter(
        (source) =>
          source.local &&
          source.target.includes("/gen/") &&
          !["infrastructure", "di", "cmd"].includes(source.layer),
      )
      .map((source) => `${source.file} -> ${source.target}`);
    assert.deepEqual(found, []);
  });
});
