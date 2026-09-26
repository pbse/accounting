import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type DriveFile = { id: string; name: string; filename: string; size: string; modified: string };

// Off unless the firm has configured Google Drive through WorkOS; a picked file goes through the ordinary evidence upload.
export function DriveEvidence({ closeId, onAdded }: { closeId: string; onAdded: () => void }) {
  const [state, setState] = useState<{ configured: boolean; connected?: boolean; reason?: string; files?: DriveFile[] }>();
  const [search, setSearch] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function load(q = search) {
    setMessage("");
    try {
      setState(await workspaceRequest(`/${closeId}/drive?q=${encodeURIComponent(q)}`));
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Google Drive is unavailable.");
    }
  }
  useEffect(() => {
    void load("");
  }, [closeId]);
  async function connect() {
    try {
      window.location.assign((await workspaceRequest(`/${closeId}/drive/connect`, {})).url);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Google Drive connection is unavailable.");
    }
  }
  async function add(file: DriveFile) {
    setBusy(true);
    setMessage("");
    try {
      const blob = await workspaceRequest(`/${closeId}/drive/files/${encodeURIComponent(file.id)}`, undefined, true);
      const data = new FormData();
      data.append("file", new File([blob], file.filename));
      await workspaceRequest(`/${closeId}/evidence`, data);
      setMessage(`${file.filename} saved as evidence.`);
      onAdded();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The file could not be added.");
    } finally {
      setBusy(false);
    }
  }
  if (!state?.configured) return message ? <p role="status">{message}</p> : null;
  return (
    <details>
      <summary>Add from Google Drive</summary>
      <p>Search your Google Drive for a PDF, text or CSV file and add it as evidence, with the same checks as an upload. Only you can see your Drive; access is through your firm's WorkOS connection.</p>
      {state?.connected === false && <p>{state.reason} <button type="button" onClick={() => void connect()}>Connect Google Drive</button></p>}
      {state?.connected && (
        <>
          <form onSubmit={(e) => { e.preventDefault(); void load(); }}>
            <label>Search file names<input value={search} maxLength={100} onChange={(e) => setSearch(e.target.value)} /></label>
            <button>Search</button>
          </form>
          {state.files?.length === 0 && <p>No PDF, text or CSV files match.</p>}
          <ul>
            {state.files?.map((file) => (
              <li key={file.id}>
                {file.name} · {file.modified?.slice(0, 10)}{" "}
                <button type="button" disabled={busy} onClick={() => void add(file)}>Add as evidence</button>
              </li>
            ))}
          </ul>
        </>
      )}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
