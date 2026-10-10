import { workPath } from "./queries";

// A client's page and its reads (features/clients/ClientPage.tsx). The API keeps a client's routes off a close's path,
// so the client is named in the query; the page's address names it in the path.
export const clientPath = (name: string) => `/client?name=${encodeURIComponent(name)}`;
export const clientPage = (name: string) => `/dashboard/clients/${encodeURIComponent(name)}`;
// The requests waiting on the client, as Work's Client requests view lists them for its name.
export const waitingPath = (name: string) => workPath(undefined, "waiting_client", name);
