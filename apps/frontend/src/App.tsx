import { lazy, Suspense } from "react";
import { BrowserRouter, Link, Route, Routes } from "react-router";
import { MarketingLayout } from "./features/marketing/Layout";
import { Home } from "./features/marketing/Home";
import { Contact } from "./features/marketing/Contact";
import { Login } from "./features/auth/Login";

const ClientRequest = lazy(() => import("./features/closes/ClientRequest"));
const WorkspaceLayout = lazy(() => import("./features/dashboard/Layout"));
const AccrualPreparation = lazy(() => import("./features/accruals/Prepare"));
const PrepaidPreparation = lazy(() => import("./features/prepaids/Prepare"));
const Closes = lazy(() => import("./features/closes/Closes"));
const Reconciliation = lazy(() => import("./features/reconciliation/Prepare"));
const Settlement = lazy(() => import("./features/settlements/Prepare"));
const Dashboard = lazy(() => import("./features/dashboard/Dashboard"));

export function App() {
  return (
    <BrowserRouter>
      <Suspense
        fallback={
          <main>
            <p role="status">Opening your workspace…</p>
          </main>
        }
      >
        <Routes>
          <Route path="request" element={<ClientRequest />} />
          <Route element={<MarketingLayout />}>
            <Route index element={<Home />} />
            <Route path="contact" element={<Contact />} />
            <Route path="login" element={<Login />} />
            <Route
              path="*"
              element={
                <main id="main" className="public-page">
                  <h1>We couldn’t find that page.</h1>
                  <Link to="/">Return home</Link>
                </main>
              }
            />
          </Route>
          <Route path="dashboard" element={<WorkspaceLayout />}>
            <Route index element={<Dashboard view="work" />} />
            <Route path="close" element={<Dashboard view="close" />} />
            <Route path="review" element={<Dashboard view="review" />} />
            <Route path="closes" element={<Closes />} />
            <Route path="closes/:id" element={<Closes />} />
            <Route path="reconciliations/new" element={<Reconciliation />} />
            <Route path="settlements/new" element={<Settlement />} />
            <Route path="accruals/new" element={<AccrualPreparation />} />
            <Route path="prepaids/new" element={<PrepaidPreparation />} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
  );
}
