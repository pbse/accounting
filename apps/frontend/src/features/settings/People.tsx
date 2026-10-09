import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { ClientAccess } from "../closes/ClientAccess";
import { FirmPeople, Invitations, MemberDecision } from "../closes/Enterprise";

// The firm's people: who they are and their roles, who is invited, which clients each is assigned, and the enrolment
// decisions behind their access.
export function People() {
  // An assignment names a client with a saved close, as the access route requires; /security/clients lists exactly those.
  const read = useQuery(workspaceQuery<{ clients: string[] }>("/security/clients"));
  const clients = read.data?.clients;
  const clientsError = read.error ? read.error.message || "The firm's clients could not be read." : "";
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
            <button type="button" onClick={() => void read.refetch()}>
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
