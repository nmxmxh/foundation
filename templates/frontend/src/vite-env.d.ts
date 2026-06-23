/// <reference types="vite/client" />

interface ImportMetaEnv {
  // The projection read path derives its gateway endpoints from this origin
  // (falling back to the page origin) plus the standard /v1/projections path.
  readonly VITE_API_URL: string
  readonly VITE_APP_NAME: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
