import { lazy, Suspense, type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes, useSearchParams } from "react-router";
import { marketingRoutes } from "./features/marketing/routes";
import { Login } from "./features/auth/Login";
import { WorkspaceGate } from "./features/dashboard/Access";
import { EffortTracker } from "./features/closes/Effort";
import { pages } from "./pages";

const ClientRequest = lazy(() => import("./features/closes/ClientRequest"));
const WorkspaceLayout = pages.layout;
const AccrualPreparation = lazy(() => import("./features/accruals/Prepare"));
const LoanPreparation = lazy(() => import("./features/loans/Prepare"));
const OwnerPreparation = lazy(() => import("./features/owners/Prepare"));
const PayrollPreparation = lazy(() => import("./features/payroll/Prepare"));
const PrepaidPreparation = lazy(() => import("./features/prepaids/Prepare"));
const ClosesList = pages.closes;
const Preparation = pages.preparation;
const ClientClose = pages.close;
const WorkpaperView = pages.workpaper;
const Measure = pages.measure;
const Settings = pages.settings;
const Executive = lazy(() => import("./features/executive/Executive"));

function Gated({ children }: { children: ReactNode }) {
  return <WorkspaceGate>{children}</WorkspaceGate>;
}
function CloseScoped({ children }: { children: ReactNode }) {
  const [params] = useSearchParams();
  const close = params.get("close");
  return (
    <Gated>
      {close && <EffortTracker closeId={close} activity="prepare" />}
      {children}
    </Gated>
  );
}
const Reconciliation = lazy(() => import("./features/reconciliation/Prepare"));
const Settlement = lazy(() => import("./features/settlements/Prepare"));
const Dashboard = pages.dashboard;

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
          {marketingRoutes(<Route path="login" element={<Login />} />)}
          <Route path="dashboard" element={<WorkspaceLayout />}>
            <Route index element={<Gated><Dashboard view="work" /></Gated>} />
            <Route path="close" element={<Gated><Dashboard view="close" /></Gated>} />
            <Route path="review" element={<Gated><Dashboard view="review" /></Gated>} />
            <Route path="closes" element={<Gated><ClosesList /></Gated>} />
            <Route path="closes/:id" element={<Gated><ClientClose /></Gated>} />
            <Route path="closes/:id/workpapers/:paperId" element={<Gated><WorkpaperView /></Gated>} />
            <Route path="firm" element={<Navigate to="/dashboard/settings?tab=firm" replace />} />
            <Route path="attention" element={<Navigate to="/dashboard" replace />} />
            <Route path="work" element={<Gated><Preparation /></Gated>} />
            <Route path="security" element={<Navigate to="/dashboard/settings?tab=security" replace />} />
            <Route path="settings" element={<Gated><Settings /></Gated>} />
            <Route path="measure" element={<Gated><Measure /></Gated>} />
            <Route path="executive" element={<Gated><Executive view="overview" /></Gated>} />
            <Route path="executive/reports" element={<Gated><Executive view="reports" /></Gated>} />
            <Route path="executive/cash" element={<Gated><Executive view="cash" /></Gated>} />
            <Route path="executive/decisions" element={<Gated><Executive view="decisions" /></Gated>} />
            <Route path="reconciliations/new" element={<CloseScoped><Reconciliation /></CloseScoped>} />
            <Route path="settlements/new" element={<CloseScoped><Settlement /></CloseScoped>} />
            <Route path="accruals/new" element={<CloseScoped><AccrualPreparation /></CloseScoped>} />
            <Route path="loans/new" element={<CloseScoped><LoanPreparation /></CloseScoped>} />
            <Route path="owners/new" element={<CloseScoped><OwnerPreparation /></CloseScoped>} />
            <Route path="payroll/new" element={<CloseScoped><PayrollPreparation /></CloseScoped>} />
            <Route path="prepaids/new" element={<CloseScoped><PrepaidPreparation /></CloseScoped>} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
  );
}
