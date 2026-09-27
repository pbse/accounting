import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";
import { LedgerSelection } from "./Coverage";

type Found = {
  type: "possible_duplicate" | "uncategorized" | "no_payee" | "inconsistent_coding" | "past_due_receivable" | "past_due_payable" | "unapplied_payment" | "open_customer_credit" | "open_supplier_credit" | "unscheduled_prepaid" | "unscheduled_fixed_asset" | "unscheduled_deferred_revenue";
  kind: string;
  provider_ids: string[];
  dates: string[];
  amount: string;
  account?: string;
  finding: string;
  next_step: string;
  question: string;
};
type Balance = { type: "abnormal_balance" | "holding_balance"; account_id: string; account: string; side: string; amount: string; currency: string; finding: string; next_step: string; question: string };
type Scan = {
  capture: { id: string; source: "quickbooks" | "xero"; evidence_id?: string; finished_at: string; unread?: string } | null;
  scanned?: number;
  items: Found[];
  schedule_scan?: { state: "checked" | "incomplete" | "no_directory"; unknown_accounts: string[]; item_lines: number };
  balances: { snapshot: { id: string; captured_at: string; stale: boolean; source: "quickbooks" | "xero" } | null; items: Balance[]; error?: string };
};

const labels: Record<Found["type"], [string, string]> = {
  possible_duplicate: ["Possible duplicate", "warn"],
  uncategorized: ["Holding account", "info"],
  no_payee: ["No payee", "info"],
  inconsistent_coding: ["Inconsistent coding", "info"],
  past_due_receivable: ["Overdue invoice", "warn"],
  past_due_payable: ["Overdue bill", "warn"],
  unapplied_payment: ["Unapplied payment", "warn"],
  open_customer_credit: ["Unused customer credit", "info"],
  open_supplier_credit: ["Unused supplier credit", "info"],
  unscheduled_prepaid: ["Prepaid needs a schedule", "warn"],
  unscheduled_fixed_asset: ["Fixed asset needs a schedule", "warn"],
  unscheduled_deferred_revenue: ["Deferred revenue needs a schedule", "warn"],
};

export function Exceptions({ closeId }: { closeId: string }) {
  const [scan, setScan] = useState<Scan>();
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/quickbooks/exceptions`)
      .then((data) => live && setScan(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Exceptions are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId]);
  if (!scan) return error ? <p role="alert">{error}</p> : null;
  const balances = scan.balances;
  const questions = [...scan.items, ...balances.items].map((item) => item.question);
  const ledger = scan.capture?.source === "xero" ? "Xero" : "QuickBooks";
  const correction = (item: Found): [string, string] | undefined => {
    // The agent suggests applying an unapplied payment or a supplier credit when the open items support one pairing.
    const apply = ({ unapplied_payment: "apply-payment", open_supplier_credit: "apply-vendor_credit" } as Record<string, string>)[item.type];
    if (apply) return scan.capture?.source === "quickbooks" ? ["workpapers", apply] : undefined;
    return item.type !== "uncategorized" && item.type !== "inconsistent_coding"
      ? undefined
      : scan.capture?.source === "xero"
        ? item.kind === "Bill" ? ["evidence", "correct-xero"] : undefined
        : ({ Bill: ["workpapers", "correct-bills"], Purchase: ["workpapers", "correct-purchases"], Deposit: ["workpapers", "correct-deposits"] } as Record<string, [string, string]>)[item.kind];
  };
  async function copy(items: string[]) {
    try {
      await navigator.clipboard.writeText(items.map((question) => `- ${question}`).join("\n"));
      setCopied(`Copied ${items.length} question${items.length === 1 ? "" : "s"}. Paste them into a client request.`);
    } catch {
      setCopied("The browser blocked the clipboard. Select the questions below and copy them.");
    }
  }
  return (
    <section aria-label="Exceptions the agent found">
      <div className="section-title">
        <h2>Exceptions the agent found</h2>
        {questions.length > 0 && (
          <span>
            <button type="button" onClick={() => void copy(questions)}>
              Copy client questions
            </button>{" "}
            <Link to="?tab=requests#client-request">Draft a client request</Link>
          </span>
        )}
      </div>
      {copied && <p role="status">{copied}</p>}
      {!scan.capture ? (
        <p className="empty">
          Capture this month's QuickBooks transactions or Xero books on the <Link to="?tab=evidence">Evidence</Link> tab. The agent then checks every
          transaction for possible duplicates, holding-account postings, payments without a payee and payees coded to different accounts, and
          asks about open invoices more than 60 days and bills more than 30 days past due at month-end.
        </p>
      ) : scan.capture.unread ? (
        <p role="status">
          The newest Xero capture, from {new Date(scan.capture.finished_at).toLocaleString()}, has no transactions: {scan.capture.unread}
        </p>
      ) : (
        <>
          <p>
            Checked all {scan.scanned} transactions in the {ledger} capture from {new Date(scan.capture.finished_at).toLocaleString()} (
            {scan.capture.source === "xero" ? `Xero snapshot #${scan.capture.id}` : `evidence #${scan.capture.evidence_id}`}). Open invoice and bill
            balances are as of that capture; days past due are counted at month-end. Each item is a question with the evidence still missing,
            not a conclusion. Nothing is changed in {ledger}.
          </p>
          {scan.schedule_scan && scan.schedule_scan.state !== "checked" && (
            <p role="status">
              {scan.schedule_scan.state === "no_directory"
                ? "Schedule discovery waits on a month-end QuickBooks snapshot, which names each account's type."
                : [
                    "Schedule discovery is incomplete.",
                    scan.schedule_scan.unknown_accounts.length > 0 && `Not in the snapshot's account list: ${scan.schedule_scan.unknown_accounts.join(", ")}; capture a new snapshot.`,
                    scan.schedule_scan.item_lines > 0 && `${scan.schedule_scan.item_lines} bill or purchase line${scan.schedule_scan.item_lines === 1 ? " posts" : "s post"} through items, whose accounts are not captured; check those for prepaids or assets yourself.`,
                  ]
                    .filter(Boolean)
                    .join(" ")}
            </p>
          )}
          {scan.items.length === 0 ? (
            <p className="empty">No possible duplicates, holding-account postings, payments without a payee, inconsistent coding or overdue items in this capture.</p>
          ) : (
            <div className="rows">
              <div className="rows-head">
                <span>What the agent found</span>
                <span>Question for the client</span>
                <span>{ledger}</span>
                <span />
              </div>
              {scan.items.map((item) => (
                <div className="row" key={item.type + item.provider_ids.join() + (item.account ?? "")}>
                  <div>
                    <span className={`status ${labels[item.type][1]}`}>{labels[item.type][0]}</span>
                    <p>{item.finding}</p>
                    <small>{item.next_step}</small>
                  </div>
                  <span>{item.question}</span>
                  <small>
                    {item.kind} {item.provider_ids.map((id) => `#${id}`).join(", ")}
                  </small>
                  <span>
                    {item.provider_ids.slice(0, 5).map((id) => {
                      const target = correction(item);
                      return target && <Link key={id} to={`?tab=${target[0]}#${target[1]}-${encodeURIComponent(id)}`}>Prepare a correction for #{id}</Link>;
                    })}
                  </span>
                </div>
              ))}
            </div>
          )}
        </>
      )}
      <h3>Balances to review</h3>
      {!balances.snapshot ? (
        <p className="empty">
          Capture a period snapshot on the <Link to="?tab=evidence">Evidence</Link> tab. The agent then checks every Trial Balance account for a
          balance opposite its normal side, such as a credit in receivables or an overdrawn bank account, and for suspense, clearing,
          undeposited-funds or uncategorized accounts that still hold a balance.
        </p>
      ) : (
        <>
          <p>
            From the {balances.snapshot.source === "xero" ? "Xero" : "QuickBooks"} Trial Balance in snapshot #{balances.snapshot.id}, captured {new Date(balances.snapshot.captured_at).toLocaleString()}.
            Contra accounts, retained earnings and owner draws are skipped because they normally sit on the opposite side.
          </p>
          {balances.snapshot.stale && (
            <p role="status">This snapshot's source was replaced or the connection is no longer active. Capture a new snapshot before relying on it.</p>
          )}
          {balances.error ? (
            <p role="alert">The agent could not read this Trial Balance: {balances.error}.</p>
          ) : balances.items.length === 0 ? (
            <p className="empty">Every classified account in this Trial Balance sits on its normal side, and no holding or clearing account carries a balance.</p>
          ) : (
            <div className="rows">
              <div className="rows-head">
                <span>What the agent found</span>
                <span>Question for the client</span>
                <span>Balance</span>
                <span />
              </div>
              {balances.items.map((item) => (
                <div className="row" key={item.account_id}>
                  <div>
                    <span className="status warn">{item.type === "holding_balance" ? "Needs reconciliation" : "Abnormal balance"}</span>
                    <p>{item.finding}</p>
                    <small>{item.next_step}</small>
                    {item.type === "holding_balance" && balances.snapshot?.source === "quickbooks" && (
                      <details>
                        <summary>This month's ledger rows in {item.account}</summary>
                        <LedgerSelection closeId={closeId} snapshotId={balances.snapshot.id} accountId={item.account_id} stale={balances.snapshot.stale} suggestions={[]} />
                      </details>
                    )}
                  </div>
                  <span>{item.question}</span>
                  <small>
                    {item.amount} {item.currency} {item.side}
                  </small>
                  <span />
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </section>
  );
}
