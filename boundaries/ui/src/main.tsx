import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { AppProviders } from "./app/providers";
import { AppRouter } from "./app/router";
import { getRuntimeConfig } from "./shared/config/runtime-config";
import "./styles.css";

async function bootstrap() {
  const config = getRuntimeConfig();

  if (config.enableMocks) {
    const { mockWorker } = await import("./shared/graphql/mocks/browser");
    await mockWorker.start({ onUnhandledFrame: "bypass" });
  }

  const root = document.getElementById("root");
  if (!root) throw new Error("Application root element is missing");

  createRoot(root).render(
    <StrictMode>
      <AppProviders config={config}>
        <AppRouter />
      </AppProviders>
    </StrictMode>,
  );
}

void bootstrap();
