import { useEffect, useState } from "react";
import { ClientAccess } from "../closes/ClientAccess";
import { FirmPeople, Invitations, MemberDecision } from "../closes/Enterprise";
import { workspaceRequest } from "../closes/workspace";

// The firm's people: who they are and their roles, who is invited, which clients each is assigned, and the enrolment
// decisions behind their access.
export function People() {
  // An assignment names a client with a saved close, as the access route requires; the security read lists exactly those.
  const [clients, setClients] = useState<string[]>();
  const [clientsError, setClientsError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setClientsError("");
    workspaceRequest("/security")
      .then((data: { clients: string[] }) => live && setClients(data.clients))
      .catch((e) => live && setClientsError(e instanceof Error ? e.message : "The firm's clients could not be read."));
    return () => {
      live = false;
    };
  }, [attempt]);
  return (
    <>
      <FirmPeople />
      <Invitations />
      <section aria-label="Client assignments">
        <h2>Client assignments</h2>
        <p>A person whose role does not see every client works only on the clients assigned to them here. Assignments cover every month of that client.</p>
        {clientsError ? (
          <p role="alert">
            {clientsError}{" "}
            <button type="button" onClick={() => setAttempt((n) => n + 1)}>
              Try again
            </button>
          </p>
        ) : !clients ? (
          <p role="status">Reading the firm's clients…</p>
        ) : clients.length === 0 ? (
          <p className="empty">No client has a saved close yet; an assignment names a client with one.</p>
        ) : (
          <ClientAccess entities={clients} />
        )}
      </section>
      <section aria-label="Enrolment">
        <h2>Enrolment</h2>
        <p>A member's access to this firm is an audited decision. Sign-in and roles stay with the firm's sign-in provider; client assignments stay separate.</p>
        <MemberDecision />
      </section>
    </>
  );
}
