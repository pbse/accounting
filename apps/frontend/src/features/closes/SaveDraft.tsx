import type { AccrualInput } from "../../../../../packages/contracts/accrual";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import type { EvidenceFile } from "./Evidence";
import { workspaceRequest } from "./workspace";
import type { PrepaidInput } from "../../../../../packages/contracts/prepaid";
import type { ReconciliationInput } from "../../../../../packages/contracts/reconciliation";
import type { SettlementInput } from "../../../../../packages/contracts/settlement";
import type { LoanInput } from "../../../../../packages/contracts/loan";
import type { OwnerInput } from "../../../../../packages/contracts/owner";
import type { PayrollInput } from "../../../../../packages/contracts/payroll";

const roleLabels: Record<string, string> = { processor_report: "Stripe payout export", bank: "Bank CSV", activity_report: "Stripe activity report" };

export function SaveDraft({
  kind,
  input,
  initialSources = {},
}: {
  kind: "prepaid" | "reconciliation" | "accrual" | "settlement" | "loan" | "owner" | "payroll";
  input: PrepaidInput | ReconciliationInput | AccrualInput | SettlementInput | LoanInput | OwnerInput | PayrollInput;
  initialSources?: Record<string, { id: string; location: string }>;
}) {
  const [params] = useSearchParams();
  const close = params.get("close");
  const revises = params.get("revise");
  const work = params.get("work");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [files, setFiles] = useState<EvidenceFile[]>([]);
  const [sources, setSources] =
    useState<Record<string, { id: string; location: string }>>(initialSources);
  useEffect(() => {
    if (close)
      workspaceRequest(`/${encodeURIComponent(close)}/evidence`)
        .then(setFiles)
        .catch((e) => setStatus(e.message));
  }, [close]);
  const csvOnly = kind === "reconciliation" || kind === "settlement";
  const roles = kind === "owner" || kind === "payroll" ? ["support", "ledger"] : kind === "loan" ? ["statement", "support", "ledger"] : kind === "settlement" ? ["processor_report", "bank", ...((input as SettlementInput).activity_csv ? ["activity_report"] : [])] : kind === "reconciliation" ? ["statement", "ledger", ...((input as ReconciliationInput).account_type === "credit_card" ? ["procedure"] : []), ...((input as ReconciliationInput).prior_outstanding?.length ? ["support"] : []), ...((input as ReconciliationInput).current_outstanding?.some(item => item.treatment === "clearing") ? ["clearing"] : []), ...((input as ReconciliationInput).transfers?.length ? ["other_statement", "other_ledger"] : []), ...((input as ReconciliationInput).native_status && (input as ReconciliationInput).native_status !== "unverified" ? ["native_status"] : [])] : kind==="accrual" ? ["invoice","support"] : ("credit_amount" in input && input.credit_amount) || ("booked_balance" in input && input.booked_balance) || ("cancellation_method" in input && input.cancellation_method) ? ["invoice","support"] : ["invoice"];
  if (!close)
    return (
      <p>
        To save drafts,{" "}
        <Link to="/dashboard/closes">open a private client close</Link> and
        start preparation there. Export this draft before leaving.
      </p>
    );
  return (
    <section>
      {kind !== "settlement" && <>
      <h3>Keep this workpaper</h3>
      <p>
        Save an immutable draft to this client close. No approval or posting
        occurs.
      </p>
      <button
        disabled={busy || saved || !!revises || !!work}
        onClick={async () => {
          setBusy(true);
          setStatus("");
          try {
            await workspaceRequest(`/${encodeURIComponent(close)}/workpapers`, {
              kind,
              input,
            });
            setSaved(true);
            setStatus("Draft saved. You can reopen it from the client close.");
          } catch (error) {
            setStatus(
              error instanceof Error
                ? error.message
                : "Save failed. Your draft is still here.",
            );
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Saving…" : saved ? "Saved" : "Save draft to close"}
      </button>
      {(revises || work) && <p className="note">A revision or a job's draft is saved from linked evidence below, so its sources stay attached.</p>}
      </>}
      <h3>Prepare from linked evidence</h3>
      <p>
        Confirm the values against uploaded evidence, then save instructions for
        the durable close job. Supporting evidence must cover invoice/service terms, any credit and the current ledger balance.
        Reconciliation and Stripe CSV values will come from the selected files, replacing
        the pasted rows above. Card work also needs the applied approved reconciliation procedure;
        current clearing items need their own clearing-account evidence.
      </p>
      {files.length === 0 && (
        <p>
          Upload evidence on the client close first, then return to this form.
        </p>
      )}
      {roles.map((role) => (
        <div key={role}>
          <label>
            {roleLabels[role] ?? `${role} source`}
            <select
              value={sources[role]?.id ?? ""}
              onChange={(e) =>
                setSources({
                  ...sources,
                  [role]: {
                    id: e.target.value,
                    location: sources[role]?.location ?? "",
                  },
                })
              }
            >
              <option value="">Choose evidence</option>
              {files
                .filter(
                  (f) =>
                    !f.superseded_by &&
                    f.extraction_state === "ready" &&
                    (role === "procedure" ? f.procedure_kind === "reconciliation" : !csvOnly || role === "support" || role === "clearing" || role === "native_status" ||
                      f.name.toLowerCase().endsWith(".csv") || f.id === sources[role]?.id),
                )
                .map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.name} · #{f.id}
                  </option>
                ))}
            </select>
          </label>
          <label>
            Source location (lines or section)
            <input
              maxLength={200}
              value={sources[role]?.location ?? ""}
              onChange={(e) =>
                setSources({
                  ...sources,
                  [role]: {
                    id: sources[role]?.id ?? "",
                    location: e.target.value,
                  },
                })
              }
            />
          </label>
        </div>
      ))}
      <button
        disabled={
          busy ||
          roles.some(
            (role) => !sources[role]?.id || !sources[role]?.location.trim(),
          )
        }
        onClick={async () => {
          setBusy(true);
          setStatus("");
          try {
            const result = await workspaceRequest(
              `/${encodeURIComponent(close)}/preparations`,
              {
                kind,
                input,
                ...(revises ? { revises } : {}),
                ...(work
                  ? {
                      work_id: work,
                      work_revision: Number(params.get("revision")),
                    }
                  : {}),
                sources: roles.map((role) => ({ ...sources[role], role })),
              },
            );
            setStatus(
              result.job_id
                ? `Preparation job #${result.job_id} queued. Return to the close to review the new draft.`
                : `Instructions #${result.id} saved. Return to the close and select them under Prepare this close.`,
            );
          } catch (e) {
            setStatus(
              e instanceof Error ? e.message : "Could not save instructions.",
            );
          } finally {
            setBusy(false);
          }
        }}
      >
        {work
          ? "Confirm linked facts and continue this job"
          : revises
            ? "Confirm revision and recalculate"
            : "Save source-linked instructions"}
      </button>
      <p role="status">{status}</p>
      <Link to={`/dashboard/closes/${encodeURIComponent(close)}`}>
        Return to client close
      </Link>
    </section>
  );
}
