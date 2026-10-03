import { type FormEvent, useEffect, useState } from "react";
import { money, workspaceRequest } from "./workspace";

type Action = { id: string; owner: string; mine: boolean; status: string; expected_on: string; note: string; actor: string; created_at: string };
type Item = {
  kind: "Invoice" | "Bill";
  id: string;
  label: string;
  party: string;
  currency: string;
  due: string;
  days_past_due: number | null;
  balance: string;
  problem?: string;
  action: Action | null;
  settled?: boolean;
  reason?: string;
};
type Items =
  | { state: "waiting"; reason: string }
  | { state: "ready"; company: string; capture_id: string; captured_at: string; month_end: string; complete: boolean; receivables: Item[]; payables: Item[]; settled: Item[]; settled_total: number };

const statuses: Record<Item["kind"], Record<string, string>> = {
  Invoice: { open: "Open", following_up: "Following up", promised: "Promised to pay", disputed: "Disputed", on_hold: "On hold" },
  Bill: { open: "Open", following_up: "Following up", handed_to_bill_pay: "Handed to bill pay", disputed: "Disputed", on_hold: "On hold" },
};
const tone: Record<string, string> = { promised: "status ok", handed_to_bill_pay: "status ok", disputed: "status danger", on_hold: "status warn", following_up: "status info" };

export function OpenItems({ closeId }: { closeId: string }) {
  const [items, setItems] = useState<Items>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [failed, setFailed] = useState<{ item: string; message: string }>();
  const [saved, setSaved] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/open-items`)
      .then((data) => live && (setItems(data), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Open items are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, version]);
  async function decide(event: FormEvent<HTMLFormElement>, item: Item, capture: string) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const status = String(form.get("status"));
    setBusy(item.kind + item.id);
    setFailed(undefined);
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/open-items`, {
        kind: item.kind,
        item_id: item.id,
        capture_id: capture,
        status,
        expected_on: status === "promised" || status === "handed_to_bill_pay" ? String(form.get("expected_on") ?? "") : "",
        note: form.get("note"),
        assign: form.get("assign") || "",
        previous_id: item.action?.id ?? "",
      });
      setSaved(`Saved the follow-up for ${item.label}.`);
      setVersion((n) => n + 1);
    } catch (e) {
      setFailed({ item: item.kind + item.id, message: e instanceof Error ? e.message : "The decision could not be saved." });
    } finally {
      setBusy("");
    }
  }
  if (!items) return error ? <p role="alert">{error}</p> : null;
  const row = (item: Item, capture: string, company: string) => {
    const status = item.action?.status ?? "open";
    const key = item.kind + item.id;
    return (
      <tr key={`${company}-${key}-${item.action?.id ?? "none"}`}>
        <th scope="row" style={{ textAlign: "left" }}>
          {item.label}
        </th>
        <td style={{ whiteSpace: "nowrap" }}>{item.balance ? money(item.balance, item.currency) : item.problem}</td>
        <td>{item.due ? `${item.due}${item.days_past_due !== null && item.days_past_due > 0 ? ` · ${item.days_past_due} days late` : ""}` : "No due date"}</td>
        <td>
          <span className={tone[status] ?? "status"}>{statuses[item.kind][status] ?? status}</span>
          {item.action?.expected_on && <small> · {item.action.expected_on}</small>}
          {item.action?.owner && <small> · {item.action.mine ? "you" : item.action.owner}</small>}
          {item.action?.note && <small> · {item.action.note}</small>}
          <details>
            <summary aria-label={`Decide the follow-up for ${item.label}`}>Decide</summary>
            <form onSubmit={(e) => void decide(e, item, capture)}>
              <fieldset disabled={busy === key}>
                <label>
                  Status
                  <select name="status" defaultValue={status}>
                    {Object.entries(statuses[item.kind]).map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  {item.kind === "Invoice" ? "Promised payment date" : "Date the bill-pay tool will pay"} (used only for a promise or handoff)
                  <input type="date" name="expected_on" defaultValue={item.action?.expected_on} />
                </label>
                <label>
                  Note {item.kind === "Invoice" ? "(what was agreed or disputed)" : "(the decision handed to the client's bill-pay tool)"}
                  <textarea name="note" maxLength={2000} defaultValue={item.action?.note} />
                </label>
                <label>
                  Owner
                  <select name="assign" defaultValue="">
                    <option value="">{item.action?.owner ? (item.action.mine ? "Keep: you" : `Keep: ${item.action.owner}`) : "Keep: nobody"}</option>
                    <option value="take">Assign to me</option>
                    <option value="release">Unassign</option>
                  </select>
                </label>
                {failed?.item === key && <p role="alert">{failed.message}</p>}
                <button>Save decision</button>
              </fieldset>
            </form>
          </details>
        </td>
      </tr>
    );
  };
  const table = (caption: string, list: Item[], capture: string, company: string) =>
    list.length > 0 && (
      <table>
        <caption>{caption}</caption>
        <thead>
          <tr>
            <th scope="col" style={{ textAlign: "left" }}>
              Document
            </th>
            <th scope="col">Open balance</th>
            <th scope="col">Due at month-end</th>
            <th scope="col">Follow-up</th>
          </tr>
        </thead>
        <tbody>{list.map((item) => row(item, capture, company))}</tbody>
      </table>
    );
  return (
    <details>
      <summary>Follow up open invoices and bills</summary>
      {error && <p role="alert">{error}</p>}
      {items.state === "waiting" ? (
        <p className="empty">{items.reason}</p>
      ) : (
        <>
          <p className="note">
            Open in the capture of {new Date(items.captured_at).toLocaleString()}. A document is settled only when a later capture that read every open item no longer
            lists it. Promised dates and bill-pay handoffs are what the cash plan will use. Nothing here contacts a customer, pays a bill or writes anything off: a
            write-off is a correction prepared in the close.{" "}
            <button type="button" onClick={() => setVersion((n) => n + 1)}>
              Refresh
            </button>
          </p>
          {saved && <p role="status">{saved}</p>}
          {!items.complete && <p role="status">Incomplete: some open items could not be read in this capture.</p>}
          {table("Receivables", items.receivables, items.capture_id, items.company)}
          {table("Payables", items.payables, items.capture_id, items.company)}
          {items.receivables.length + items.payables.length === 0 && <p className="empty">No open invoices or bills in the latest capture.</p>}
          {items.settled.length > 0 && (
            <>
              <h3>No longer open in the latest capture</h3>
              <ul>
                {items.settled.map((item) => (
                  <li key={item.kind + item.id}>
                    {item.label}: {item.settled ? "settled in the ledger" : item.reason} Last decision:{" "}
                    {statuses[item.kind][item.action!.status] ?? item.action!.status}
                    {item.action!.note ? ` (${item.action!.note})` : ""}.
                  </li>
                ))}
              </ul>
              {items.settled_total > items.settled.length && <p className="note">Showing the {items.settled.length} most recent of {items.settled_total}.</p>}
            </>
          )}
        </>
      )}
    </details>
  );
}
