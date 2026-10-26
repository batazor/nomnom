import { Link, Outlet } from "react-router-dom";

import { MarketSwitcher, WhenMarketChosen } from "../features/choose-market";
import { SimulatorToggle } from "../features/demo-simulator";
import { AccountMenu, boardUser } from "../shared/auth/sign-in";
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
          <MarketSwitcher />
          {/* The Simulator lives in the Cell of the Market. */}
          <WhenMarketChosen>
            <SimulatorToggle />
          </WhenMarketChosen>
          {/* Mocks need no sign-in; against BFF the board acts as one user. */}
          <AccountMenu role={boardUser} roleLabel={t("auth.role.demo")} />
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
