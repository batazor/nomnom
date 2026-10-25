/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_PUBLIC_GRAPHQL_URL?: string;
  readonly VITE_LOCALE?: string;
  readonly VITE_ENABLE_MOCKS?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
