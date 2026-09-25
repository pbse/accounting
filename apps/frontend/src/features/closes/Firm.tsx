import { useEffect, useState } from "react";
import { Icon } from "../dashboard/Icon";
import { AgentAccess } from "./AgentAccess";
import { ClientAccess } from "./ClientAccess";
import { isEnrollmentError } from "./ClosesList";
import { EmailSettings } from "./Email";
import { FirmEnrollment, FirmOperations } from "./Enterprise";
import { workspaceRequest } from "./workspace";

type Policy = { independent_review: boolean; link_passcode: boolean; hosted: boolean; can_manage: boolean; can_manage_links: boolean };

function IndependentReview() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { independent_review: !policy!.independent_review, reason });
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Independent review policy">
      <h2>Independent review</h2>
      <p>
        {policy.independent_review
          ? "On: nobody may approve a draft they prepared or confirmed, drafts without a recorded preparer cannot be approved, and production posting needs an approval that meets this rule."
          : "Off: any member with the reviewer role may approve a draft, including one they prepared."}
      </p>
      {!policy.hosted ? (
        <p className="note">Local development shares one identity, so this policy can only be enforced for hosted firms.</p>
      ) : policy.can_manage ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>
            {policy.independent_review ? "Turn off independent review" : "Require independent review"}
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

function LinkPasscodes() {
  const [policy, setPolicy] = useState<Policy>();
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/organization/policy")
      .then((data) => live && setPolicy(data))
      .catch((e) => live && setMessage(e instanceof Error ? e.message : "Firm policy is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!policy) return message ? <p role="alert">{message}</p> : null;
  async function change() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/policy", { link_passcode: !policy!.link_passcode, reason });
      setReason("");
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Policy change failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Client link passcodes">
      <h2>Client link passcodes</h2>
      <p>
        {policy.link_passcode
          ? "On: each new client request link also needs an 8-digit passcode. The person who issues the link sees it once and tells the client by phone or in person; five wrong tries close the link."
          : "Off: anyone holding a client request link can read the request and reply."}{" "}
        Links issued earlier keep working as they were issued.
      </p>
      {policy.can_manage_links ? (
        <>
          <label>
            Reason for the change
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || reason.trim().length < 5} onClick={() => void change()}>
            {policy.link_passcode ? "Stop requiring passcodes" : "Require passcodes on new links"}
          </button>
        </>
      ) : (
        <p className="note">A firm administrator with org:manage can change this policy.</p>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

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
          <IndependentReview />
      <LinkPasscodes />
          <AgentAccess />
          <FirmOperations />
          <ClientAccess entities={entities} />
          <EmailSettings />
        </div>
      )}
    </>
  );
}
