import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Grant = { id: string; workpaper_id: string; granted_by: string; reason: string; created_at: string; in_force: boolean };

const covers = {
  prepaid: "this schedule's later carried months while its terms and evidence are unchanged. A revised schedule, or any monthly entry, still needs a person.",
  accrual: "next months' recurring accrual carrying the same amount, accounts and rule. Posting its entry still needs a person's posting approval.",
  loan: "later months' drafts for this loan while every term matches (lender, agreement, rate, day count, accounts and choices) and nothing is left open. Posting its entries still needs a person's posting approval.",
};

export function StandingApproval({ closeId, paperId, kind, approved }: { closeId: string; paperId: string; kind: keyof typeof covers; approved: boolean }) {
  const [grant, setGrant] = useState<Grant | null>();
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `/${closeId}/workpapers/${paperId}/standing-approval`;
  async function load() {
    try {
      setGrant((await workspaceRequest(path)).grant);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Standing approval is unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [path]);
  async function act(action: "grant" | "revoke") {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, { action, reason, grant_id: grant?.id ?? "", confirmed });
      setConfirmed(false);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Standing approval could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  if (grant === undefined || (!grant && !approved)) return error ? <p role="alert">{error}</p> : null;
  return (
    <section aria-label="Standing approval">
      <h2>Approve once, not every month</h2>
      {grant ? (
        <>
          <p>
            Standing approval #{grant.id} by {grant.granted_by} since {new Date(grant.created_at).toLocaleDateString()}
            {grant.workpaper_id !== paperId ? `, granted on draft #${grant.workpaper_id}` : ""}: {grant.reason}
          </p>
          {grant.in_force ? (
            <p className="note">
              Approved automatically, under this person's name and while the firm's review rules allow it: {covers[kind]}
            </p>
          ) : (
            <p role="status">
              Not in effect: the granting draft was reviewed again, or the granter or the firm is no longer enabled. Nothing is approved automatically;
              revoke it and grant a new one if later months should keep approving themselves.
            </p>
          )}
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> Stop approving later months automatically
          </label>{" "}
          <button type="button" disabled={busy || !confirmed} onClick={() => void act("revoke")}>
            Revoke
          </button>
        </>
      ) : (
        <>
          <p className="note">
            Approve automatically {covers[kind]} Approvals are recorded under your name, only while you remain a member of the firm; revoke it if your
            review role changes.
          </p>
          <label>
            Reason
            <textarea value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I approve these later months
          </label>{" "}
          <button type="button" disabled={busy || !confirmed || !reason.trim()} onClick={() => void act("grant")}>
            Grant standing approval
          </button>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
