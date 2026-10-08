import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { EmailSettings } from "../closes/Email";
import { workspaceRequest } from "../closes/workspace";
import type { Policy } from "./Policies";

// Who on our team supports the firm, and the firm's own records to take away at any time.
function SupportAndRecords({ policy }: { policy: Policy }) {
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function download() {
    setBusy(true);
    setMessage("");
    try {
      const link = document.createElement("a");
      link.href = URL.createObjectURL(await workspaceRequest("/firm-export", undefined, true));
      link.download = `firm-export-${new Date().toISOString().slice(0, 10)}.json`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(link.href), 60_000);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The firm export is unavailable.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Support and your records">
      <h2>Support and your records</h2>
      <p>{policy.support_owner ? `Your support owner is ${policy.support_owner}.` : "No support owner is named yet."}</p>
      <p>
        At any time, and when you leave, a data administrator can download the firm's own records: settings, people and access history, procedures, client records, and every
        close with where its review package downloads.
      </p>
      <p className="setting-actions">
        <button type="button" disabled={busy} onClick={() => void download()}>
          Download the firm's records
        </button>
      </p>
      {message && <p role="alert">{message}</p>}
    </section>
  );
}

// The firm itself: how it sends email, how much of its closes the agent carries, who supports it, and its records.
// The email sender and Human effort read on their own; only the support owner waits for the policy.
export function FirmTab({ policy, unavailable }: { policy?: Policy; unavailable: ReactNode }) {
  return (
    <>
      <EmailSettings />
      <section aria-label="Human effort">
        <h2>Human effort</h2>
        <p>How much of each month's closes the agent carries, how many minutes people put in, and the agent's track record across the firm.</p>
        <p className="setting-actions">
          <Link className="button" to="/dashboard/measure">
            Open Human effort
          </Link>
        </p>
      </section>
      {policy ? <SupportAndRecords policy={policy} /> : unavailable}
    </>
  );
}
