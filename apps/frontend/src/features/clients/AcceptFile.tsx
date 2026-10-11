import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";

// A file's checks and its acceptance (docs/books-sources.md, How an upload becomes the month's books, steps 6 and 7):
// each check passes, holds what depends on it, or is a note, and none refuses. A reviewer accepts the reading as the
// client's books for the month, from a named system on a named date, with its attested scope and basis.

type Check = { key: string; title: string; state: "passes" | "holds" | "note"; detail?: string; holds?: string; items?: string[] };
type Acceptance = {
  id: string;
  period: string;
  system: string;
  exported_on: string;
  final_adjustments: boolean;
  explained?: string;
  checks: Check[];
  reason: string;
  actor: string;
  at: string;
  current: boolean;
  map_changed?: boolean;
};
// A file accepted for a month whose books come from a live ledger supplements them: reconciled to its capture, and not
// used when it differs.
type Supplement = {
  state: "reconciled" | "differs" | "unavailable";
  period: string;
  ledger?: "quickbooks" | "xero";
  captured_at?: string;
  accounts?: number;
  differences?: string[];
  reason?: string;
};
// A file for a month that already accepted another's books: how the month changed since, and what was built on them.
type AcceptedBooks = { file: string; accepted_at: string; same: boolean; differences: string[]; effects: string[] };
type AcceptanceRead = {
  confirmed: boolean;
  // ledger says the file is the month's ledger detail, read but not yet a month's books.
  ledger?: string;
  accepted_books?: AcceptedBooks | null;
  supplement?: Supplement | null;
  checks: Check[];
  holds?: number;
  period?: string;
  reading?: string;
  scope?: string;
  map_stamp?: string;
  acceptance: Acceptance | null;
  on: boolean;
  can_accept: boolean;
};

const acceptancePath = (client: string, id: string) => `/client/import/acceptance?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}`;
const statePills = { passes: ["pill", "Passes"], holds: ["pill danger", "Holds"], note: ["pill neutral", "Note"] } as const;

export function AcceptFile({ client, id, onClose }: { client: string; id: string; onClose: () => void }) {
  const read = useQuery(workspaceQuery<AcceptanceRead>(acceptancePath(client, id)));
  if (!read.data) return read.error ? <p role="alert">{read.error.message || "The file's checks could not be read."}</p> : <p role="status">Checking the file…</p>;
  return <AcceptForm key={`${read.data.reading ?? ""}:${read.data.scope ?? ""}:${read.data.acceptance?.id ?? ""}`} client={client} id={id} data={read.data} onClose={onClose} />;
}

function AcceptForm({ client, id, data, onClose }: { client: string; id: string; data: AcceptanceRead; onClose: () => void }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const was = data.acceptance;
  const [system, setSystem] = useState(was?.system ?? "");
  const [exportedOn, setExportedOn] = useState(was?.exported_on ?? "");
  const [finalAdjustments, setFinalAdjustments] = useState(was?.final_adjustments ?? false);
  const [explained, setExplained] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const scopeHolds = data.checks.find((c) => c.key === "scope" && c.state === "holds" && c.items?.length);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(acceptancePath(client, id), {
        system: system.trim(),
        exported_on: exportedOn,
        final_adjustments: finalAdjustments,
        explained: explained.trim(),
        reason: reason.trim(),
        reading: data.reading,
        scope: data.scope,
        based_on: data.map_stamp ?? "",
      });
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
    } catch (err) {
      setError(err instanceof Error ? err.message : "The file could not be accepted.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="ks-import" onSubmit={(e) => void submit(e)}>
      {!data.confirmed ? (
        <p className="note">Confirm how the file is read first: the checks run on that reading.</p>
      ) : (
        <>
          {was && (
            <p>
              Accepted {dates.dayMonth(was.at)} by {was.actor} as the client's books for {monthName(was.period)}, from {was.system}, exported {was.exported_on}
              {was.final_adjustments ? ", with the final adjustments in." : ", before the final adjustments."}{" "}
              {!was.current && <strong>{was.map_changed ? "The account map has changed how this file's accounts are classified since then: the month's books keep the earlier map until you accept this one again." : "That was of an earlier reading or attestation: accept this one again."}</strong>}
            </p>
          )}
          {data.ledger && <p className="note">{data.ledger}</p>}
          {data.accepted_books && (
            <>
              <p>
                {monthName(data.period ?? "")}’s books were accepted {dates.dayMonth(data.accepted_books.accepted_at)} from {data.accepted_books.file}.{" "}
                {data.accepted_books.same
                  ? "This file matches them, account for account."
                  : "This file differs from them: the month changed after its books were accepted. Accepting this file makes it the month’s books."}
              </p>
              {!data.accepted_books.same && (
                <ul>
                  {data.accepted_books.differences.map((d) => (
                    <li key={d}>{d}</li>
                  ))}
                </ul>
              )}
              {data.accepted_books.effects.map((e) => (
                <p key={e} role="status">
                  {e}
                </p>
              ))}
            </>
          )}
          {data.supplement && (
            <>
              <p>
                {monthName(data.supplement.period)}’s books come from {data.supplement.ledger === "xero" ? "Xero" : data.supplement.ledger === "quickbooks" ? "QuickBooks" : "its ledger"}, so this file supplements them and never
                replaces them.{" "}
                {data.supplement.state === "unavailable"
                  ? data.supplement.reason
                  : data.supplement.state === "reconciled"
                    ? `It agrees with the capture of ${dates.dateTime(data.supplement.captured_at ?? "")} to the cent, across ${data.supplement.accounts} accounts.`
                    : `It does not agree with the capture of ${dates.dateTime(data.supplement.captured_at ?? "")}, and is not used.`}
              </p>
              {data.supplement.state === "differs" && (
                <ul>
                  {(data.supplement.differences ?? []).map((d) => (
                    <li key={d}>{d}</li>
                  ))}
                </ul>
              )}
            </>
          )}
          <p>{data.holds ? `${data.holds} check${data.holds === 1 ? "" : "s"} hold${data.holds === 1 ? "s" : ""} what depend${data.holds === 1 ? "s" : ""} on them. None refuses the file.` : "Every check passes."}</p>
          <ul className="ks-filelist">
            {data.checks.map((c) => (
              <li key={c.key}>
                <span className={statePills[c.state][0]}>{statePills[c.state][1]}</span> <strong>{c.title}.</strong> {c.detail}
                {c.holds && (
                  <>
                    {" "}
                    <em>Holds:</em> {c.holds}
                  </>
                )}
                {c.items && c.items.length > 0 && (
                  <ul>
                    {c.items.slice(0, 20).map((item) => (
                      <li key={item}>{item}</li>
                    ))}
                    {c.items.length > 20 && <li>and {c.items.length - 20} more</li>}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
      {data.confirmed && !data.scope && <p className="note">Attest the file's scope and basis before accepting it.</p>}
      {data.can_accept && (
        <>
          <label>
            System the books were exported from
            <input value={system} maxLength={200} placeholder="QuickBooks Desktop" onChange={(e) => setSystem(e.target.value)} />
          </label>
          <label>
            Exported on
            <input type="date" value={exportedOn} onChange={(e) => setExportedOn(e.target.value)} />
          </label>
          <label className="checkbox-label">
            <input type="checkbox" checked={finalAdjustments} onChange={(e) => setFinalAdjustments(e.target.checked)} /> The final adjustments for the month are in
          </label>
          {scopeHolds && (
            <label>
              Why those accounts are not in this file
              <input value={explained} maxLength={1000} onChange={(e) => setExplained(e.target.value)} />
            </label>
          )}
          <label>
            Reason
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
        </>
      )}
      {!data.on && <p className="note">File imports are switched off for your firm: the acceptance can be read, not given.</p>}
      {error && <p role="alert">{error}</p>}
      <DialogActions>
        <button type="button" onClick={onClose}>
          Close
        </button>
        {data.can_accept && (
          <button className="primary" disabled={busy || !system.trim() || !exportedOn || !reason.trim()}>
            {busy ? "Accepting…" : `Accept as ${data.period ? monthName(data.period) : "the month"}'s books`}
          </button>
        )}
      </DialogActions>
    </form>
  );
}
