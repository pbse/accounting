import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Grant = { id: string; workpaper_id: string; granted_by: string; reason: string; created_at: string; in_force: boolean };

export function StandingApproval({ closeId, paperId, approved }: { closeId: string; paperId: string; approved: boolean }) {
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
      <h2>Approve the schedule, not every month</h2>
      {grant ? (
        <>
          <p>
            Standing approval #{grant.id} by {grant.granted_by} since {new Date(grant.created_at).toLocaleDateString()}
            {grant.workpaper_id !== paperId ? `, granted on draft #${grant.workpaper_id}` : ""}: {grant.reason}
          </p>
          {grant.in_force ? (
            <p className="note">
              Each later month's carried schedule is approved automatically, under this person's name, while its terms and evidence are unchanged and the
              firm's review rules allow it. A revised schedule, or any monthly entry, still needs a person.
            </p>
          ) : (
            <p role="status">
              Not in effect: the schedule was reviewed again, or the granter or the firm is no longer enabled. Nothing is approved automatically; revoke it
              and grant a new one if the schedule should keep approving itself.
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
            Approve this schedule's later carried months automatically while nothing changes. They are recorded under your name, only while you remain a
            member of the firm; revoke it if your review role changes. Revisions and monthly entries still need a person.
          </p>
          <label>
            Reason
            <textarea value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I approve the remaining months of this schedule
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
