import { Link, Outlet } from "react-router-dom";

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
          <span>{t("app.brand")}</span>
        </Link>
        <span className="location-label">{t("app.location")}</span>
      </header>
      <div className="app-sheet">
        <main className="app-content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
