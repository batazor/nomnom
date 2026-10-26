/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_PUBLIC_API_URL?: string;
  readonly VITE_LOCALE?: string;
  readonly VITE_ENABLE_MOCKS?: string;
  readonly VITE_FARO_URL?: string;
  readonly VITE_DEPLOYMENT_ENVIRONMENT?: string;
  readonly VITE_APP_VERSION?: string;
  readonly VITE_OIDC_ISSUER?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  readonly VITE_OIDC_AUDIENCE?: string;
  readonly VITE_OIDC_DEMO_EMAIL_DOMAIN?: string;
  readonly VITE_OIDC_DEMO_PASSWORD?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
