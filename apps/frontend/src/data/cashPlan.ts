import { queryOptions } from "@tanstack/react-query";
import { browserRead, type Read } from "./queries";

// A close's cash plan view: the plan (as it stands, or under a chosen scenario), the open obligations (not an
// executive's) and the scenarios, read together from their three endpoints so the figures, the schedules and the
// selector always come from the same moment. Under the workspace key, so any change made through the app reads it
// again.
export function cashPlanQuery<P, O, S>(closeId: string, scenario: string, readOnly: boolean, read: Read = browserRead) {
  return queryOptions({
    queryKey: ["workspace", `cash-plan-view:${closeId}:${scenario}:${readOnly ? "read" : "firm"}`],
    queryFn: async () => {
      const [plan, obligations, scenarios] = await Promise.all([
        read(`/${closeId}/cash-plan${scenario ? `?scenario=${encodeURIComponent(scenario)}` : ""}`),
        readOnly ? { obligations: [] } : read(`/${closeId}/cash-obligations`),
        read(`/${closeId}/cash-scenarios`),
      ]);
      return { plan: plan as P, obligations: (obligations as { obligations: O[] }).obligations, scenarios: (scenarios as { scenarios: S[] }).scenarios };
    },
  });
}
