// Layer import rules of ADR 6: `npm run architecture`. TypeScript 7 has no
// compiler API for dependency-cruiser, so sources are parsed by swc.

/** The acyclic graph of slices: each slice with the neighbours it may import. */
const SLICE_DEPENDENCIES = {
  restaurant: [],
  menu: ["restaurant"],
  scheduleslot: ["restaurant"],
  restaurantorder: ["restaurant", "menu"],
  cook: ["restaurantorder"],
  simulator: ["restaurantorder", "restaurant"],
};

/** @type {import("dependency-cruiser").IConfiguration} */
module.exports = {
  forbidden: [
    ...Object.entries(SLICE_DEPENDENCIES).map(([slice, neighbours]) => ({
      name: `slice-${slice}-dependencies`,
      comment: "A slice imports only the neighbours of the acyclic graph (ADR 6).",
      severity: "error",
      from: { path: `^slices/${slice}/` },
      to: {
        path: "^slices/",
        pathNot: `^slices/(${[slice, ...neighbours].join("|")})/`,
      },
    })),
    {
      name: "domains-depend-on-domains",
      comment: "Domains depend only on domains, decimal.js and Node built-ins.",
      severity: "error",
      from: { path: "^slices/[^/]+/domains/" },
      to: {
        dependencyTypesNot: ["core"],
        pathNot: "^(slices/[^/]+/domains/|node_modules/decimal\\.js/)",
      },
    },
    {
      name: "value-objects-without-aggregate",
      comment: "Value objects do not import their aggregate.",
      severity: "error",
      from: { path: "^slices/[^/]+/domains/([^/]+)/vo/" },
      to: {
        path: "^slices/[^/]+/domains/$1/",
        pathNot: "^slices/[^/]+/domains/$1/vo/",
      },
    },
    {
      name: "applications-without-adapters",
      comment:
        "Use cases depend only on domains, use cases and the pkg use case contracts.",
      severity: "error",
      from: { path: "^slices/[^/]+/applications/" },
      to: {
        dependencyTypesNot: ["core"],
        pathNot:
          "^(slices/[^/]+/(domains|applications)/|pkg/(usecase|authz|unit-of-work|outbox)/)",
      },
    },
    {
      name: "shared-packages-without-slices",
      comment: "Shared packages do not import slices or processes.",
      severity: "error",
      from: { path: "^pkg/" },
      to: { path: "^(slices|infrastructure|di|cmd)/" },
    },
    {
      name: "infrastructure-without-composition-root",
      comment: "Adapters do not import the composition root.",
      severity: "error",
      from: { path: "^(infrastructure|slices/[^/]+/infrastructure)/" },
      to: { path: "^(di|cmd|slices/[^/]+/di)/" },
    },
    {
      name: "generated-code-in-adapters",
      comment:
        "Generated code is imported only by adapters and the composition root (ADR 7).",
      severity: "error",
      from: {
        pathNot:
          "^(infrastructure|slices/[^/]+/infrastructure|di|slices/[^/]+/di|cmd)/",
      },
      to: { path: "(/gen/|^node_modules/@buf/)" },
    },
    {
      name: "resolvable-imports",
      comment: "An unresolved import would hide a dependency from the rules.",
      severity: "error",
      from: {},
      to: { couldNotResolve: true },
    },
  ],
  options: {
    parser: "swc",
    doNotFollow: { path: "node_modules" },
    enhancedResolveOptions: {
      exportsFields: ["exports"],
      conditionNames: ["import", "types", "node", "default"],
    },
  },
};
