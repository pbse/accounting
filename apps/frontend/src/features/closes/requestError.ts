// A workspace request the server refused, with its HTTP status, so a section can tell "not for you" from a failure.
export class RequestError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

// forbidden says whether a request was refused for want of permission: a section built on it does not apply to the
// reader, rather than having failed.
export const forbidden = (e: unknown) => e instanceof RequestError && e.status === 403;
