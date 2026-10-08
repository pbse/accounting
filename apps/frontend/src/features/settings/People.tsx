import { useEffect, useState } from "react";
import { ClientAccess } from "../closes/ClientAccess";
import { FirmPeople, Invitations, MemberDecision } from "../closes/Enterprise";
import { workspaceRequest } from "../closes/workspace";

// The firm's people: who they are and their roles, who is invited, which clients each is assigned, and the enrolment
// decisions behind their access.
export function People() {
  // The client names an assignment can name come from the roster the firm keeps.
  const [clients, setClients] = useState<string[]>();
  useEffect(() => {
    let live = true;
    workspaceRequest("/roster")
      .then((data: { clients: { entity: string }[] }) => live && setClients(data.clients.map((c) => c.entity)))
      .catch(() => live && setClients([]));
    return () => {
      live = false;
    };
  }, []);
  return (
    <>
      <FirmPeople />
      <Invitations />
      <section aria-label="Client assignments">
        <h2>Client assignments</h2>
        <p>A person whose role does not see every client works only on the clients assigned to them here. Assignments cover every month of that client.</p>
        {clients && <ClientAccess entities={clients} />}
      </section>
      <section aria-label="Enrolment">
        <h2>Enrolment</h2>
        <p>A member's access to this firm is an audited decision. Sign-in and roles stay with the firm's sign-in provider; client assignments stay separate.</p>
        <MemberDecision />
      </section>
    </>
  );
}
