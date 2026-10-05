import { useState } from "react";
import { useResource, when } from "../api";
import { PageHeader } from "@close/ui/components/PageHeader";

type Note = { id: number; name: string; email: string; firm: string; note: string; created_at: string };

// Notes left on the public site's contact page, newest first. Each is also forwarded to the team inbox when it can be;
// this list is the record, so a note that never arrived there is answered from here.
export function ContactNotes() {
  const [before, setBefore] = useState(0);
  const notes = useResource<{ notes: Note[]; next_before: number | null }>(`/v1/admin/contact-notes${before ? `?before=${before}` : ""}`);
  return (
    <>
      <PageHeader icon="file" title="Contact notes">
        Notes from the contact page on getkrith.com, newest first. Each is forwarded to the team inbox; reply from here if one never arrived.
      </PageHeader>
      {notes.error && <p role="alert">{notes.error}</p>}
      {!notes.data && !notes.error && <p role="status">Loading the notes…</p>}
      {notes.data && (
        <section>
          {notes.data.notes.length === 0 ? <p>No notes yet.</p> : (
            <table className="contact-notes">
              <thead><tr><th>Received</th><th>From</th><th>Firm</th><th>Note</th></tr></thead>
              <tbody>
                {notes.data.notes.map((n) => (
                  <tr key={n.id}>
                    <td>{when(n.created_at)}</td>
                    <td>{n.name}<br /><a href={`mailto:${n.email}?subject=${encodeURIComponent("Re: your note to Krith")}`}>{n.email}</a></td>
                    <td>{n.firm}</td>
                    <td className="pre-line">{n.note || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {(before > 0 || notes.data.next_before) && (
            <p className="title-actions">
              {before > 0 && <button onClick={() => setBefore(0)}>Newest</button>}
              {notes.data.next_before && <button onClick={() => setBefore(notes.data!.next_before!)}>Older</button>}
            </p>
          )}
        </section>
      )}
    </>
  );
}
