import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Found = {
  type: "possible_duplicate" | "uncategorized" | "no_payee";
  kind: string;
  provider_ids: string[];
  dates: string[];
  amount: string;
  finding: string;
  next_step: string;
  question: string;
};
type Scan = { capture: { id: string; evidence_id: string; finished_at: string } | null; scanned?: number; items: Found[] };

const labels: Record<Found["type"], [string, string]> = {
  possible_duplicate: ["Possible duplicate", "warn"],
  uncategorized: ["Holding account", "info"],
  no_payee: ["No payee", "info"],
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
  async function copy(items: Found[]) {
    try {
      await navigator.clipboard.writeText(items.map((item) => `- ${item.question}`).join("\n"));
      setCopied(`Copied ${items.length} question${items.length === 1 ? "" : "s"}. Paste them into a client request.`);
    } catch {
      setCopied("The browser blocked the clipboard. Select the questions below and copy them.");
    }
  }
  return (
    <section aria-label="Exceptions the agent found">
      <div className="section-title">
        <h2>Exceptions the agent found</h2>
        {scan.items.length > 0 && (
          <span>
            <button type="button" onClick={() => void copy(scan.items)}>
              Copy client questions
            </button>{" "}
            <Link to="?tab=requests#client-request">Draft a client request</Link>
          </span>
        )}
      </div>
      {!scan.capture ? (
        <p className="empty">
          Capture this month's QuickBooks transactions on the <Link to="?tab=evidence">Evidence</Link> tab. The agent then checks every
          transaction for possible duplicates, holding-account postings and payments without a payee.
        </p>
      ) : (
        <>
          <p>
            Checked all {scan.scanned} transactions in the capture from {new Date(scan.capture.finished_at).toLocaleString()} (evidence #
            {scan.capture.evidence_id}). Each item is a question with the evidence still missing, not a conclusion. Nothing is changed in
            QuickBooks.
          </p>
          {copied && <p role="status">{copied}</p>}
          {scan.items.length === 0 ? (
            <p className="empty">No possible duplicates, holding-account postings or payments without a payee in this capture.</p>
          ) : (
            <div className="rows">
              <div className="rows-head">
                <span>What the agent found</span>
                <span>Question for the client</span>
                <span>QuickBooks</span>
                <span />
              </div>
              {scan.items.map((item) => (
                <div className="row" key={item.type + item.provider_ids.join()}>
                  <div>
                    <span className={`status ${labels[item.type][1]}`}>{labels[item.type][0]}</span>
                    <p>{item.finding}</p>
                    <small>{item.next_step}</small>
                  </div>
                  <span>{item.question}</span>
                  <small>
                    {item.kind} {item.provider_ids.map((id) => `#${id}`).join(", ")}
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
