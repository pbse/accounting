import { Fragment, useState } from "react";
import { useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { VerticalTabs, type VerticalTab } from "@close/ui/components/VerticalTabs";
import { isEnrollmentError } from "../closes/ClosesList";
import { FirmEnrollment } from "../closes/Enterprise";
import { ClientRoster } from "../closes/Roster";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceKey, workspaceQuery } from "../../data/queries";
import { FirmTab } from "./FirmTab";
import { People } from "./People";
import { Policies, usePolicy } from "./Policies";
import { SecurityTab } from "./SecurityTab";
import { Connections, Notifications, Profile, type Me } from "./You";

const to = (tab: string) => `/dashboard/settings?tab=${tab}`;
// The person's own sections first, then the firm's. The sections only a firm administrator can use show only to one.
const yours: VerticalTab[] = [
  { key: "profile", label: "Profile", to: "/dashboard/settings", icon: "profile", group: "You" },
  { key: "notifications", label: "Notifications", to: to("notifications"), icon: "bell", group: "You" },
  { key: "connections", label: "Connections", to: to("connections"), icon: "link", group: "You" },
];
const firm = (members: boolean, roster: boolean): VerticalTab[] => [
  { key: "firm", label: "Firm", to: to("firm"), icon: "firm", group: "Firm" },
  ...(members ? [{ key: "people", label: "People", to: to("people"), icon: "people", group: "Firm" } as const] : []),
  ...(roster ? [{ key: "clients", label: "Clients", to: to("clients"), icon: "clients", group: "Firm" } as const] : []),
  { key: "policies", label: "Policies", to: to("policies"), icon: "sliders", group: "Firm" },
  ...(members ? [{ key: "security", label: "Security", to: to("security"), icon: "shield", group: "Firm" } as const] : []),
];

const descriptions: Record<string, string> = {
  profile: "Your name and time zone.",
  notifications: "What counts as new for you, and whether this browser says so.",
  connections: "Agents and services connected for you alone.",
  firm: "The firm's email sender, support and records.",
  people: "Who is in the firm, their roles, invitations and client assignments.",
  clients: "The clients the firm closes each month, and the monthly template.",
  policies: "The firm's rules for review, posting, client requests and the agent.",
  security: "Connected services, posting authority, people's agents and recent changes.",
};

// One page for a person and their firm: the person's own settings, then the firm's, each a section of its own.
export default function Settings() {
  const [params] = useSearchParams();
  const { policy, error: policyError, refresh: refreshPolicy } = usePolicy();
  // While the policy cannot be read, every firm section stays reachable; each refuses on its own if it must.
  const items = [...yours, ...firm(policy ? policy.can_manage_members : !!policyError, policy ? policy.can_read_roster : !!policyError)];
  const asked = params.get("tab");
  // A section the person cannot open, or none, opens the profile. A firm section asked for before the policy that gates
  // it has been read stays selected, so a failed read shows there, with the way to try again, not a different section.
  const tab = items.find((t) => t.key === asked)?.key ?? (asked && !policy && firm(true, true).some((t) => t.key === asked) ? asked : "profile");
  const queryClient = useQueryClient();
  const meRead = useQuery(workspaceQuery<Me>("/me"));
  const me = meRead.data;
  const error = meRead.error ? meRead.error.message || "Your settings are unavailable." : "";
  const refresh = () => void queryClient.invalidateQueries({ queryKey: workspaceKey("/me") });
  // Enrolling the firm makes every read that refused with the membership error succeed, so each is made again, and the
  // sections that read on their own are mounted afresh.
  const [generation, setGeneration] = useState(0);
  const enrolled = () => {
    void queryClient.invalidateQueries({ queryKey: ["workspace"] });
    setGeneration((g) => g + 1);
  };
  const firmSection = yours.every((t) => t.key !== tab);
  const panel = () => {
    if (firmSection) {
      // People, Clients and Security read their own routes, so a failed policy read does not close them.
      if (tab === "people") return <People />;
      if (tab === "clients") return <ClientRoster />;
      if (tab === "security") return <SecurityTab />;
      const unavailable = policyError ? (
        isEnrollmentError(policyError) ? (
          <FirmEnrollment onEnrolled={enrolled} />
        ) : (
          <section aria-label="Firm policy unavailable">
            <p role="alert">{policyError}</p>
            <p className="setting-actions">
              <button type="button" onClick={refreshPolicy}>
                Try again
              </button>
            </p>
          </section>
        )
      ) : !policy ? (
        <p role="status">Opening…</p>
      ) : null;
      if (tab === "policies") return unavailable ?? <Policies policy={policy!} refresh={refreshPolicy} />;
      return <FirmTab policy={policy} unavailable={unavailable} />;
    }
    if (error) return <p role="alert">{error}</p>;
    if (!me) return <p role="status">Opening…</p>;
    if (tab === "connections") return <Connections />;
    if (tab === "notifications") return <Notifications me={me} onSaved={refresh} />;
    return <Profile me={me} onSaved={refresh} />;
  };
  return (
    <>
      {/* Keyed by section so each section counts as its own rendered page (scripts/layout-check.mjs). */}
      <PageHeader key={tab} icon="settings" title="Settings">
        {descriptions[tab]}
      </PageHeader>
      <VerticalTabs label="Settings sections" items={items} value={tab}>
        <Fragment key={generation}>{panel()}</Fragment>
      </VerticalTabs>
    </>
  );
}
