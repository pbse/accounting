// Only 204 means the backend has durably accepted this delivery.
// The current backend returns 503, so no unfinished work is acknowledged.
export async function deliver(
  body: unknown,
  send: (request: Request) => Promise<Response>,
): Promise<void> {
  const response = await send(
    new Request("http://worker/internal/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  if (response.status !== 204) throw new Error("Durable handoff not confirmed");
}
