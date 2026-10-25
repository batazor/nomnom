/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_PUBLIC_GRAPHQL_URL?: string;
  readonly VITE_LOCALE?: string;
  readonly VITE_ENABLE_MOCKS?: string;
  readonly VITE_FARO_URL?: string;
  readonly VITE_DEPLOYMENT_ENVIRONMENT?: string;
  readonly VITE_APP_VERSION?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
