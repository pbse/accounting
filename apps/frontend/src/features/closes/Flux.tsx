import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Payee = { name: string; this_month: string; last_month: string };
type Change = {
  account_id: string;
  account: string;
  class: string;
  this_month: string;
  last_month: string;
  change: string;
  causes_unavailable?: string;
  new?: Payee[];
  missing?: Payee[];
  changed?: Payee[];
  unattributed?: { this_month: string; last_month: string };
  reconciled?: boolean;
  omitted?: number;
  questions?: string[];
};
type Month = { period: string; snapshot_id: string; captured_at: string; stale: boolean };
type Flux = { state: "ready" | "waiting"; reason?: string; currency?: string; this?: Month; last?: Month; changes?: Change[]; note?: string };

const kind = (c: Change) => (c.class === "Expense" ? "Expense" : c.class === "Revenue" ? "Income" : "Reported amount");

export function Flux({ closeId }: { closeId: string }) {
  const [flux, setFlux] = useState<Flux>();
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/flux`)
      .then((data) => live && setFlux(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Changes are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId]);
  if (!flux) return error ? <p role="alert">{error}</p> : null;
  const questions = (flux.changes ?? []).flatMap((c) => c.questions ?? []);
  async function copy() {
    try {
      await navigator.clipboard.writeText(questions.map((q) => `- ${q}`).join("\n"));
      setCopied(`Copied ${questions.length} question${questions.length === 1 ? "" : "s"}.`);
    } catch {
      setCopied("The browser blocked the clipboard. Select the questions below and copy them.");
    }
  }
  return (
    <section aria-label="Changes from last month">
      <div className="section-title">
        <h2>Changes from last month</h2>
        {questions.length > 0 && (
          <button type="button" onClick={() => void copy()}>
            Copy client questions
          </button>
        )}
      </div>
      {copied && <p role="status">{copied}</p>}
      {flux.state === "waiting" ? (
        <p className="empty">{flux.reason}</p>
      ) : (
        <>
          <p>
            Profit and Loss {flux.this!.period} (snapshot #{flux.this!.snapshot_id}, captured {new Date(flux.this!.captured_at).toLocaleString()}) against{" "}
            {flux.last!.period} (snapshot #{flux.last!.snapshot_id}, captured {new Date(flux.last!.captured_at).toLocaleString()}), largest movements first.{" "}
            {flux.note}
          </p>
          {(flux.this!.stale || flux.last!.stale) && <p role="status">A snapshot's source was replaced or its connection is inactive; capture again before relying on these figures.</p>}
          {flux.changes!.length === 0 && <p className="empty">No account moved between the two months.</p>}
          <div className="rows">
            {flux.changes!.map((c) => (
              <div className="row" key={c.account_id}>
                <div>
                  <strong>{c.account}</strong>{" "}
                  <span>
                    {kind(c)} {c.change.startsWith("-") ? "fell" : "rose"} {c.change.replace("-", "")} {flux.currency}, from {c.last_month} to {c.this_month}.
                  </span>
                  {c.causes_unavailable ? (
                    <p>
                      <small>{c.causes_unavailable}</small>
                    </p>
                  ) : (
                    <ul>
                      {c.new!.map((p) => (
                        <li key={"n" + p.name}>New this month: {p.name} {p.this_month}</li>
                      ))}
                      {c.missing!.map((p) => (
                        <li key={"m" + p.name}>Not in this month's capture: {p.name} ({p.last_month} last month)</li>
                      ))}
                      {c.changed!.map((p) => (
                        <li key={"c" + p.name}>
                          {p.name}: {p.last_month} → {p.this_month}
                        </li>
                      ))}
                      {c.unattributed && (c.unattributed.this_month !== "0.00" || c.unattributed.last_month !== "0.00") && (
                        <li>
                          Without a payee name: {c.unattributed.last_month} → {c.unattributed.this_month}
                        </li>
                      )}
                      {(c.omitted ?? 0) > 0 && <li>And {c.omitted} more payee{c.omitted === 1 ? "" : "s"} not shown.</li>}
                      {!c.reconciled && <li>These are possible drivers; the ledger's payees do not add up to the whole movement.</li>}
                    </ul>
                  )}
                  {(c.questions ?? []).map((q) => (
                    <p key={q}>
                      <small>{q}</small>
                    </p>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </section>
  );
}
