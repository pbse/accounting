import { useState } from "react";
import { workspaceRequest } from "./workspace";

type Doc = { id: string; date: string; open: string; party_id: string; party: string };
type Suggestion = { kind: "payment" | "vendor_credit"; source_id: string; target_id: string; amount: string; basis: string };
type Application = { id: string; kind: "payment" | "vendor_credit"; source_id: string; target_id: string; amount: string; txn_date?: string; rule_reference: string; digest: string; state: string; approved_by: string; external_id: string; error: string };
const labels = {
  payment: { title: "Apply a customer payment to an invoice", source: "Payment", target: "Invoice", sourceKey: "Payment", targetKey: "Invoice" },
  vendor_credit: { title: "Apply a vendor credit to a bill", source: "Vendor credit", target: "Bill", sourceKey: "VendorCredit", targetKey: "Bill" },
} as const;

// An unapplied payment or unused vendor credit applied to an open document of the same party, under the accountant's rule.
export function CreditApplications({ closeId }: { closeId: string }) {
  const [candidates, setCandidates] = useState<Record<string, Doc[]>>({});
  const [applications, setApplications] = useState<Application[]>([]);
  const [suggestions, setSuggestions] = useState<{ items: Suggestion[]; capture: string }>({ items: [], capture: "" });
  const [fromCapture, setFromCapture] = useState("");
  const [kind, setKind] = useState<"payment" | "vendor_credit">("payment");
  const [form, setForm] = useState({ source_id: "", target_id: "", amount: "", txn_date: "", bank_account_id: "", reason: "", rule_reference: "" });
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [found, setFound] = useState<Record<string, string>>({});
  const l = labels[kind];
  async function load() {
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks/applications`);
      setCandidates(result.candidates ?? {});
      setApplications(result.applications ?? []);
      setSuggestions({ items: result.suggestions ?? [], capture: result.capture_id ?? "" });
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Applications unavailable.");
    }
  }
  async function send(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest(`/${closeId}/quickbooks/applications`, body);
      setMessage(done);
      setConfirmed(false);
      if (body.action === "preview") setFromCapture("");
      await load();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The application could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  const sources = candidates[l.sourceKey] ?? [];
  const party = sources.find((d) => d.id === form.source_id)?.party_id;
  const targets = (candidates[l.targetKey] ?? []).filter((d) => d.party_id === party);
  const set = (key: keyof typeof form, value: string) => setForm((f) => ({ ...f, [key]: value }));
  function use(s: Suggestion) {
    setKind(s.kind);
    setForm((f) => ({ ...f, source_id: s.source_id, target_id: s.target_id, amount: s.amount, reason: s.basis, rule_reference: "" }));
    setFromCapture(suggestions.capture);
    setConfirmed(false);
  }
  return (
    <details onToggle={(e) => e.currentTarget.open && void load()}>
      <summary>Apply a payment or vendor credit</summary>
      <p>
        From the latest transaction capture's open items, under the accountant's cited rule. A customer payment's unapplied amount is linked to an open invoice of the same customer; a vendor credit is applied to an open bill of the same vendor by a bill payment that moves no money. Both documents are read back. It writes to the client's production QuickBooks company under a grant that covers the kind, or to the configured sandbox company. No automatic retry follows an uncertain write.
      </p>
      {suggestions.items.length > 0 && (
        <div>
          <h4>Suggested by the agent</h4>
          <p>Each pairs one open document with the only one of the same party it equals or its reference names. Using one fills the form; add the accountant's rule, review and confirm before the preview. Nothing is saved or sent until then.</p>
          <ul>
            {suggestions.items.map((s) => (
              <li key={`${s.kind}-${s.source_id}`}>
                {labels[s.kind].source} #{s.source_id} → {labels[s.kind].target} #{s.target_id}, {s.amount} · {s.basis}{" "}
                <button type="button" onClick={() => use(s)}>Use</button>
              </li>
            ))}
          </ul>
        </div>
      )}
      <label>
        What to apply
        <select value={kind} onChange={(e) => { setKind(e.target.value as typeof kind); setForm((f) => ({ ...f, source_id: "", target_id: "" })); setFromCapture(""); }}>
          <option value="payment">{labels.payment.title}</option>
          <option value="vendor_credit">{labels.vendor_credit.title}</option>
        </select>
      </label>
      <div className="form-grid">
        <label>
          {l.source}
          <select value={form.source_id} onChange={(e) => { set("source_id", e.target.value); set("target_id", ""); }}>
            <option value="">Choose one</option>
            {sources.map((d) => <option key={d.id} value={d.id}>#{d.id} · {d.party} · {d.date} · {d.open} open</option>)}
          </select>
        </label>
        <label>
          {l.target} of the same {kind === "payment" ? "customer" : "vendor"}
          <select value={form.target_id} onChange={(e) => set("target_id", e.target.value)}>
            <option value="">Choose one</option>
            {targets.map((d) => <option key={d.id} value={d.id}>#{d.id} · {d.date} · {d.open} open</option>)}
          </select>
        </label>
        <label>Amount<input inputMode="decimal" value={form.amount} onChange={(e) => set("amount", e.target.value)} /></label>
        {kind === "vendor_credit" && <label>Bill payment date (in the close month)<input type="date" value={form.txn_date} onChange={(e) => set("txn_date", e.target.value)} /></label>}
        {kind === "vendor_credit" && <label>Bank account ID for the bill payment (no money moves)<input inputMode="numeric" value={form.bank_account_id} onChange={(e) => set("bank_account_id", e.target.value)} /></label>}
        <label>Accountant reason and source<textarea maxLength={2000} value={form.reason} onChange={(e) => set("reason", e.target.value)} /></label>
        <label>Accountant's rule it follows<input maxLength={200} value={form.rule_reference} onChange={(e) => set("rule_reference", e.target.value)} /></label>
      </div>
      <label><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I reviewed both documents, the amount and the rule.</label>
      <button type="button" disabled={busy || !confirmed || !form.source_id || !form.target_id || !form.amount} onClick={() => void send({ action: "preview", kind, ...form, confirmed, ...(fromCapture && { capture_id: fromCapture }) }, "Saved the exact application.")}>Preview exact application</button>
      {applications.length > 0 && (
        <ul>
          {applications.map((a) => (
            <li key={a.id}>
              {labels[a.kind].source} #{a.source_id} → {labels[a.kind].target} #{a.target_id}, {a.amount}{a.txn_date && ` on ${a.txn_date}`} · {a.state}
              {a.approved_by && ` · approved by ${a.approved_by}`}
              {a.external_id && ` · bill payment #${a.external_id}`} · rule {a.rule_reference}
              {a.error && <span role="alert"> · {a.error}</span>}{" "}
              {a.state === "preview" && <button type="button" disabled={busy || !confirmed} onClick={() => void send({ action: "approve", id: a.id, digest: a.digest, confirmed }, "Approved for 10 minutes.")}>Approve for 10 minutes</button>}
              {a.state === "approved" && <button type="button" disabled={busy || !confirmed} onClick={() => void send({ action: "execute", id: a.id, digest: a.digest, confirmed }, "QuickBooks readback verified. Refresh the transaction capture.")}>Apply once</button>}
              {a.kind === "vendor_credit" && !a.external_id && ["executing", "unknown"].includes(a.state) && <label>Bill payment ID found in QuickBooks<input inputMode="numeric" value={found[a.id] ?? ""} onChange={(e) => setFound((f) => ({ ...f, [a.id]: e.target.value }))} /></label>}
              {["executing", "unknown", "verified"].includes(a.state) && <button type="button" disabled={busy} onClick={() => void send({ action: "verify", id: a.id, digest: a.digest, bill_payment_id: found[a.id] ?? "" }, "Canonical readback verified.")}>Read back from QuickBooks</button>}
            </li>
          ))}
        </ul>
      )}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
