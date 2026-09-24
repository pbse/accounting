import { useEffect, useState } from "react";
import { Icon } from "../dashboard/Icon";
import { ClientAccess } from "./ClientAccess";
import { isEnrollmentError } from "./ClosesList";
import { EmailSettings } from "./Email";
import { FirmEnrollment, FirmOperations } from "./Enterprise";
import { workspaceRequest } from "./workspace";

export default function Firm() {
  const [entities, setEntities] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    setError("");
    workspaceRequest("")
      .then((data: { closes: { entity: string }[] }) => live && setEntities([...new Set(data.closes.map((close) => close.entity))]))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Firm settings are unavailable."))
      .finally(() => live && setLoaded(true));
    return () => {
      live = false;
    };
  }, [refresh]);
  return (
    <>
      <div className="page-title">
        <div>
          <span className="kpi-icon" aria-hidden="true">
            <Icon name="firm" />
          </span>
          <span>
            <h1>Firm settings</h1>
            <p>Membership, client access, approved email delivery and operational health for the whole firm.</p>
          </span>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={() => setRefresh((n) => n + 1)} />}
      {loaded && !error && (
        <div className="stack">
          <FirmOperations />
          <ClientAccess entities={entities} />
          <EmailSettings />
        </div>
      )}
    </>
  );
}
