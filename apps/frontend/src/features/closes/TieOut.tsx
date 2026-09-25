import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Status = "ties" | "ties_after_entries" | "differs" | "posting_unknown" | "unsupported";
type Row = {
  account_id: string;
  account: string;
  class: string;
  balance: string;
  expected: string | null;
  difference: string | null;
  status: Status;
  provisional: boolean;
  hint?: string;
  supports: { paper_id: string; kind: string; label: string; amount: string; review_state: string }[];
  entries: { paper_id: string; kind: string; amount: string; posting: "proposed" | "posted_after_capture" | "unknown" }[];
};
type Board = {
  snapshot: { id: string; captured_at: string; stale: boolean; source: "quickbooks" | "xero" } | null;
  currency?: string;
  error?: string;
  rows: Row[];
  unmatched: { account: string; paper_id: string; kind: string; amount: string; reason: string }[];
  unclassified: { account_id: string; account: string; balance: string }[];
};

const labels: Record<Status, [string, string]> = {
  ties: ["Ties", "ok"],
  ties_after_entries: ["Ties after proposed entries", "info"],
  differs: ["Differs", "warn"],
  posting_unknown: ["Posting outcome unknown", "warn"],
  unsupported: ["No support yet", "info"],
};
const postings = { proposed: "proposed", posted_after_capture: "posted after this snapshot", unknown: "posting outcome unknown" };

// Debit-minus-credit amounts read as an accountant writes them.
const side = (amount: string) => (/^-?0(\.0+)?$/.test(amount) ? "0.00" : amount.startsWith("-") ? `${amount.slice(1)} Cr` : `${amount} Dr`);

export function TieOut({ closeId }: { closeId: string }) {
  const [board, setBoard] = useState<Board>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/tie-out`)
      .then((data) => live && setBoard(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "The tie-out is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId]);
  if (!board) return error ? <p role="alert">{error}</p> : null;
  const count = (status: Status) => board.rows.filter((row) => row.status === status).length;
  const summary = [
    `${count("ties")} of ${board.rows.length} accounts tie`,
    count("ties_after_entries") && `${count("ties_after_entries")} tie once proposed entries post`,
    count("differs") && `${count("differs")} differ`,
    count("posting_unknown") && `${count("posting_unknown")} wait on a posting outcome`,
    count("unsupported") && `${count("unsupported")} have no support yet`,
  ].filter(Boolean);
  return (
    <section aria-label="Balance sheet tie-out">
      <div className="section-title">
        <h2>Balance sheet tie-out</h2>
      </div>
      {!board.snapshot ? (
        <p className="empty">
          Capture a period snapshot on the <Link to="?tab=evidence">Evidence</Link> tab. The agent then compares every balance-sheet account
          with the balance its schedules, accruals, loans and reconciliations support.
        </p>
      ) : (
        <>
          <p>
            {board.snapshot.source === "xero" ? "Xero" : "QuickBooks"} Trial Balance from snapshot #{board.snapshot.id}, captured{" "}
            {new Date(board.snapshot.captured_at).toLocaleString()}, against the latest version of each workpaper in this close
            {board.currency ? ` (${board.currency})` : ""}. Nothing is changed in the ledger.
          </p>
          {board.snapshot.stale && <p role="status">This snapshot's source was replaced or the connection is no longer active. Capture a new snapshot before relying on it.</p>}
          {board.error ? (
            <p role="alert">The agent could not read this Trial Balance: {board.error}.</p>
          ) : (
            <>
              <p>
                <strong>{summary.join(" · ")}.</strong>
              </p>
              {board.unclassified.length > 0 && (
                <p role="status">
                  {board.unclassified.length} Trial Balance account{board.unclassified.length === 1 ? " has" : "s have"} no class in the account list, so
                  this board may be incomplete: {board.unclassified.map((a) => `${a.account} ${side(a.balance)}`).join(", ")}.
                </p>
              )}
              <div className="rows">
                <div className="rows-head">
                  <span>Account</span>
                  <span>Trial Balance</span>
                  <span>Supported</span>
                  <span />
                </div>
                {board.rows.map((row) => (
                  <div className="row" key={row.account_id + row.account}>
                    <div>
                      <strong>{row.account}</strong>{" "}
                      <span className={`status ${labels[row.status][1]}`}>{labels[row.status][0]}</span>
                      {row.provisional && <small> · support not yet approved or has blockers</small>}
                      {row.difference && row.status !== "ties" && <p>Difference: {side(row.difference)}</p>}
                      {row.hint && <small>{row.hint}</small>}
                      {(row.supports.length > 0 || row.entries.length > 0) && (
                        <details>
                          <summary>What supports it</summary>
                          <ul>
                            {row.supports.map((s) => (
                              <li key={s.paper_id + s.label}>
                                <Link to={`/dashboard/closes/${closeId}/workpapers/${s.paper_id}`}>{s.label}</Link>: {side(s.amount)} · {s.review_state.replaceAll("_", " ")}
                              </li>
                            ))}
                            {row.entries.map((e, i) => (
                              <li key={e.paper_id + i}>
                                Entry from <Link to={`/dashboard/closes/${closeId}/workpapers/${e.paper_id}`}>{e.kind} #{e.paper_id}</Link>: {side(e.amount)} · {postings[e.posting]}
                              </li>
                            ))}
                          </ul>
                        </details>
                      )}
                    </div>
                    <span>{side(row.balance)}</span>
                    <span>{row.expected === null ? "—" : side(row.expected)}</span>
                    <span />
                  </div>
                ))}
              </div>
              {board.unmatched.length > 0 && (
                <>
                  <h3>Support not compared</h3>
                  <ul>
                    {board.unmatched.map((u, i) => (
                      <li key={u.paper_id + i}>
                        <Link to={`/dashboard/closes/${closeId}/workpapers/${u.paper_id}`}>
                          {u.kind} #{u.paper_id}
                        </Link>
                        {u.account && ` names "${u.account}" (${side(u.amount)})`}. {u.reason}
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
