import { useState } from "react";
import { workspaceRequest } from "./workspace";

type Intent = {
  state: string;
  digest?: string;
  realm?: string;
  journal?: unknown;
  external_id?: string;
  error?: string;
  approval_expires_at?: string;
  configured?: boolean;
};
export function Posting({
  closeId,
  paperId,
}: {
  closeId: string;
  paperId: string;
}) {
  const [intent, setIntent] = useState<Intent>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const path = `/${encodeURIComponent(closeId)}/workpapers/${encodeURIComponent(paperId)}/posting`;
  async function run(action?: string) {
    setBusy(true);
    setError("");
    try {
      setIntent(
        await workspaceRequest(
          path,
          action
            ? { action, digest: intent?.digest ?? "", confirmation }
            : undefined,
        ),
      );
      setConfirmation("");
    } catch (e) {
      setIntent(undefined);
      setError(
        e instanceof Error
          ? e.message
          : "Outcome could not be confirmed. Refresh status before any further action.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h3>QuickBooks sandbox posting</h3>
      <p>
        Test company only. Review the exact company, accounts, date and amount
        before approval. Approval does not post an entry. Sources remain
        manually confirmed; this is not production posting.
      </p>
      <button disabled={busy} onClick={() => void run()}>
        Check posting status
      </button>
      {error && <p role="alert">{error}</p>}
      {intent && (
        <>
          <p role="status">
            State:{" "}
            {intent.state === "executing"
              ? "Attempt recorded; outcome must be investigated before any further write"
              : intent.state}
          </p>
          {intent.configured === false && (
            <p>
              The sandbox connection and account mapping are not configured.
            </p>
          )}
          {intent.error && <p>{intent.error}</p>}
          {intent.realm && <p>Sandbox company ID: {intent.realm}</p>}
          {intent.external_id && <p>Journal entry ID: {intent.external_id}</p>}
          {intent.journal !== undefined && (
            <details open>
              <summary>Exact journal payload for review</summary>
              <pre>{JSON.stringify(intent.journal, null, 2)}</pre>
            </details>
          )}
          {intent.state === "not_prepared" && (
            <button
              disabled={busy || intent.configured === false}
              onClick={() => void run("preview")}
            >
              Prepare sandbox posting preview
            </button>
          )}
          {(intent.state === "preview" || intent.state === "approved") && (
            <>
              <p>
                Approval expires after 10 minutes. An attempt can execute only
                once; uncertain outcomes stay unresolved.
              </p>
              <label>
                Type{" "}
                {intent.state === "preview"
                  ? "APPROVE SANDBOX DRAFT"
                  : "POST TO SANDBOX"}
                <input
                  value={confirmation}
                  onChange={(event) => setConfirmation(event.target.value)}
                  autoComplete="off"
                />
              </label>
              <button
                disabled={
                  busy ||
                  confirmation !==
                    (intent.state === "preview"
                      ? "APPROVE SANDBOX DRAFT"
                      : "POST TO SANDBOX")
                }
                onClick={() =>
                  void run(intent.state === "preview" ? "approve" : "execute")
                }
              >
                {intent.state === "preview"
                  ? "Approve this exact sandbox proposal"
                  : "Post approved entry to sandbox"}
              </button>
              {intent.state === "approved" && (
                <>
                  <p>
                    Approval expires:{" "}
                    {intent.approval_expires_at
                      ? new Date(intent.approval_expires_at).toLocaleString()
                      : "Unknown"}
                  </p>
                  <button
                    disabled={busy}
                    onClick={() => {
                      setIntent({ ...intent, state: "preview" });
                      setConfirmation("");
                    }}
                  >
                    Review and renew approval
                  </button>
                </>
              )}
            </>
          )}
          {intent.external_id && (
            <button disabled={busy} onClick={() => void run("verify")}>
              Verify saved entry in QuickBooks
            </button>
          )}
        </>
      )}
      {busy && <p role="status">Checking sandbox posting…</p>}
    </section>
  );
}
