import { useState, type ReactNode } from "react";

// An action staff take: it always asks why, and the answer goes to the audit log. Firm-level changes
// ask for at least 10 characters, as the firm's own screens do.
export function ActionForm({ label, danger, allowed = true, minLength = 1, onSubmit, children }: { label: string; danger?: boolean; allowed?: boolean; minLength?: number; onSubmit: (reason: string) => Promise<void>; children?: ReactNode }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!allowed) return <p className="muted">{label}: your role does not allow this.</p>;
  return (
    <form className="action" onSubmit={async (event) => {
      event.preventDefault();
      setBusy(true);
      setError("");
      try {
        await onSubmit(reason.trim());
        setReason("");
      } catch (e) {
        setError((e as Error).message);
      } finally {
        setBusy(false);
      }
    }}>
      {children}
      <label>Reason<input required value={reason} minLength={minLength} maxLength={2000} onChange={(event) => setReason(event.target.value)} /></label>
      <button className={danger ? "danger" : "primary"} disabled={busy || reason.trim().length < minLength}>{label}</button>
      {error && <p role="alert">{error}</p>}
    </form>
  );
}
