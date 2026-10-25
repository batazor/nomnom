import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { AppProviders } from "./app/providers";
import { AppRouter } from "./app/router";
import { getRuntimeConfig } from "./shared/config/runtime-config";
import { initTelemetry } from "./shared/telemetry/faro";
import "./styles.css";

async function bootstrap() {
  const config = getRuntimeConfig();
  const faro = initTelemetry(config);

  if (config.enableMocks) {
    const { mockWorker } = await import("./shared/graphql/mocks/browser");
    await mockWorker.start({ onUnhandledFrame: "bypass" });
  }

  const root = document.getElementById("root");
  if (!root) throw new Error("Application root element is missing");

  createRoot(root).render(
    <StrictMode>
      <AppProviders config={config} faro={faro}>
        <AppRouter />
      </AppProviders>
    </StrictMode>,
  );
}

void bootstrap();
