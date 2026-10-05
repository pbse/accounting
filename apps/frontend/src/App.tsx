import { lazy, Suspense, type ReactNode } from "react";
import { BrowserRouter, Route, Routes, useSearchParams } from "react-router";
import { MarketingLayout } from "./features/marketing/Layout";
import { Home } from "./features/marketing/Home";
import { Contact } from "./features/marketing/Contact";
import { Security } from "./features/marketing/Security";
import { Privacy } from "./features/marketing/Privacy";
import { NotFound } from "./features/marketing/NotFound";
import { Login } from "./features/auth/Login";
import { WorkspaceGate } from "./features/dashboard/Access";
import { EffortTracker } from "./features/closes/Effort";

const ClientRequest = lazy(() => import("./features/closes/ClientRequest"));
const WorkspaceLayout = lazy(() => import("./features/dashboard/Layout"));
const AccrualPreparation = lazy(() => import("./features/accruals/Prepare"));
const LoanPreparation = lazy(() => import("./features/loans/Prepare"));
const OwnerPreparation = lazy(() => import("./features/owners/Prepare"));
const PayrollPreparation = lazy(() => import("./features/payroll/Prepare"));
const PrepaidPreparation = lazy(() => import("./features/prepaids/Prepare"));
const ClosesList = lazy(() => import("./features/closes/ClosesList"));
const ClientClose = lazy(() => import("./features/closes/ClientClose"));
const WorkpaperView = lazy(() => import("./features/closes/WorkpaperView"));
const Firm = lazy(() => import("./features/closes/Firm"));
const Measure = lazy(() => import("./features/closes/Measure"));
const Attention = lazy(() => import("./features/closes/Attention"));
const SecurityCenter = lazy(() => import("./features/closes/Security"));
const Executive = lazy(() => import("./features/executive/Executive"));

function Gated({ children }: { children: ReactNode }) {
  return <WorkspaceGate>{children}</WorkspaceGate>;
}
function CloseScoped({ children }: { children: ReactNode }) {
  const [params] = useSearchParams();
  const close = params.get("close");
  return close ? (
    <Gated>
      <EffortTracker closeId={close} activity="prepare" />
      {children}
    </Gated>
  ) : (
    children
  );
}
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
            <Route path="security" element={<Security />} />
            <Route path="privacy" element={<Privacy />} />
            <Route path="login" element={<Login />} />
            <Route path="*" element={<NotFound />} />
          </Route>
          <Route path="dashboard" element={<WorkspaceLayout />}>
            <Route index element={<Gated><Dashboard view="work" /></Gated>} />
            <Route path="close" element={<Dashboard view="close" />} />
            <Route path="review" element={<Dashboard view="review" />} />
            <Route path="closes" element={<Gated><ClosesList /></Gated>} />
            <Route path="closes/:id" element={<Gated><ClientClose /></Gated>} />
            <Route path="closes/:id/workpapers/:paperId" element={<Gated><WorkpaperView /></Gated>} />
            <Route path="firm" element={<Gated><Firm /></Gated>} />
            <Route path="attention" element={<Gated><Attention /></Gated>} />
            <Route path="security" element={<Gated><SecurityCenter /></Gated>} />
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
