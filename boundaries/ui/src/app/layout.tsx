import { Link, Outlet } from "react-router-dom";

import { SimulatorToggle } from "../features/demo-simulator";
import { useI18n } from "../shared/i18n/i18n";

export function AppLayout() {
  const { t } = useI18n();

  return (
    <div className="app-shell">
      <header className="app-topbar">
        <Link aria-label={t("app.brand")} className="brand" to="/">
          <span aria-hidden="true" className="brand-mark">
            NN
          </span>
          <span aria-hidden="true" className="brand-name">
            nom<span className="brand-name-accent">nom</span>
          </span>
        </Link>
        <div className="app-topbar-end">
          <span className="location-label">{t("app.location")}</span>
          <SimulatorToggle />
        </div>
      </header>
      <div className="app-sheet">
        <main className="app-content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
