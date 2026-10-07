# Authentication and sandbox setup

WorkOS AuthKit is the implemented hosted identity provider. The application supports multiple enrolled firms and client assignments; it also has a shared local-development token. Provider activation and live checks are separate from code availability. Follow [onboarding](onboarding.md) and [deployment](deployment.md) for the current complete setup.

## Configure WorkOS

1. Use separate WorkOS staging and production environments. Enable Google sign-in and turn off password and passkey sign-in ([sign-in](sign-in.md#setup-once-per-environment)). Choose the sign-in session below: the provided auth domain is enough with `AUTH_SESSION=server`; `AUTH_SESSION=client` needs a paid custom authentication domain in production.
2. Register the frontend origin, `/login` on that origin as redirect/login URI, and the origin as sign-out URI. Configure the firm's MFA policy in WorkOS.
3. Set backend `WORKOS_CLIENT_ID` and exact `WORKOS_ISSUER`. Frontend uses `VITE_WORKOS_CLIENT_ID` and `VITE_WORKOS_API_HOSTNAME`; Compose and GitHub derive these from the root/environment WorkOS settings. Copy the environment's hostname and issuer, not values from an unverified incoming token.
4. With `AUTH_SESSION=server`, firms create themselves from their company domain at first sign-in, and colleagues join as members ([sign-in](sign-in.md)). Otherwise, create a WorkOS organization for each accounting firm and invite staff; give the first administrator `org:enroll`, `members:manage` and `clients:all`, then sign in and enroll the firm in the app.
5. Record active application memberships and client assignments. WorkOS role assignment alone does not enroll an application member. Keep `clients:all` for intended firm staff; client accountants get explicit assignments plus action permissions. [Permissions](firm-procedures.md#permissions)
6. Leave `WORKOS_ORGANIZATION_ID` empty for multi-firm SaaS. Setting it deliberately restricts accepted sessions to one firm and identifies the owner of the legacy global sandbox configuration.

Go verifies WorkOS signing keys, RS256 signatures, expiry, issuer, user/session identity and organization claims; the application then checks current membership, client access and action permissions. Revoke app membership/assignment and WorkOS membership/session access together. Role changes remain subject to already-issued token lifetime. A configured WorkOS client disables the development-token fallback. Hosted ingress does not forward a workspace token or offer a hosted auth bypass.

## Local versus hosted testing

The local token is appropriate for synthetic calculations, evidence, durable work and manual links. It cannot prove separate human identities, firm isolation, MFA or posting authority. Use WorkOS staging with two organizations and separate preparer/reviewer identities for those tests. Local interactive provider OAuth needs the trusted HTTPS origins/callbacks required by that connector; use staging rather than weakening callback checks.

For native Go, settings must be exported explicitly; Go does not read `.env` automatically. Compose reads root `.env` and forwards the supported settings. Never reuse Compose's development database superuser in a hosted environment.

## Two sandbox paths

**Per-client connectors:** use [QuickBooks onboarding](quickbooks-onboarding.md) for organization/client-bound OAuth, company confirmation, token refresh, captures and supported native sandbox corrections. This is the multi-client test path. An Intuit accounting scope is broader than read-only; app operations remain constrained by code and approval.

**Legacy prepaid sandbox journal:** the saved workpaper's sandbox posting panel uses global `QBO_SANDBOX_*` credentials and the configured `WORKOS_ORGANIZATION_ID`. It is intentionally single-firm and uses a short-lived operator-supplied token; it is not automatically selected from a per-client OAuth connection. Test this only in a separate isolated single-firm configuration. Keep it disabled in the shared multi-firm environment.

For either supported posting path, inspect the saved source-linked draft and exact proposal, approve with the required human permissions, then execute once. Posting approval expires after ten minutes. Stale evidence, book-close dates, changed ledger preconditions and firm independence policy can refuse execution. A signed-off close must be reopened before journal approval/execution. Earlier attempts can still be read back.

An `executing` or `unknown` result requires investigation and canonical provider readback; never infer that a timeout means nothing was written. A readback receipt is distinct from a complete close or sign-off. [Production and correction controls](production-posting.md)

## Verification boundary

Use the [staging acceptance matrix](onboarding.md#staging-acceptance-matrix) and retain the release revision and evidence of each result. Existing tests use synthetic identities/provider responses; they do not prove live WorkOS, Intuit, Xero or Cloudflare configuration. Historical validation entries remain in [validation](validation.md).

References: [WorkOS React SDK](https://github.com/workos/authkit-react), [WorkOS sessions](https://workos.com/docs/authkit/sessions).

## Sign-in session

One setting, `AUTH_SESSION` (a GitHub environment variable; the workflows pass it to both the frontend build and the API), chooses where the WorkOS session lives:

- **`client` (the default when unset):** WorkOS's AuthKit browser session, as before. WorkOS's client-only SDK needs a custom authentication domain in production (`WORKOS_API_HOSTNAME=auth.<your-domain>`, $99/month); with `api.workos.com` its refresh cookie is third-party, and the deploy prints a warning.
- **`server`:** WorkOS AuthKit still shows its sign-in page, but it returns to the API (`/v1/auth/callback`). The API exchanges the code with `WORKOS_API_KEY`, keeps WorkOS's access and refresh tokens sealed with `SECRETS_KEY` in `auth_sessions` (migration 099, readable only by system work), and gives the browser a random `__Host-close_session` cookie (HttpOnly, Secure, SameSite=Lax, 30 days) whose SHA-256 is all the table keeps. On every `/v1` request the API turns the cookie into the usual Bearer token, so token verification, firm membership, client access and row-level security are unchanged. It refreshes an access token with under a minute left, once, with the row locked, and keeps WorkOS's rotated refresh token; WorkOS declining the refresh, or a refreshed token for another person, session or firm, ends the session, while a WorkOS outage returns 503 and keeps it. Any change other than a GET must come from the app's own origin. MCP (`/mcp`) never accepts the cookie. Sign-out revokes the row and returns WorkOS's logout URL. An `AUTH_SESSION=server` API without `WORKOS_CLIENT_ID`, `WORKOS_API_KEY`, `SECRETS_KEY` and an https `PUBLIC_APP_URL`, or with an unknown value, refuses to start, and the deploy refuses a separate API origin.

The staff console (`/admin`, platform staff only) gets the same server session in its own cookie, `__Host-close_staff`. Its sign-in (`/v1/auth/login?staff=1`) returns only to `/admin` pages. It goes straight to Google and keeps the session only for a verified address at `STAFF_EMAIL_DOMAIN` ([staff by email domain](admin-panel.md#staff-by-email-domain)); without that setting it answers 503. The staff cookie opens only `/v1/admin/*` and the firm cookie only the other `/v1` routes, so a person who is both staff and a firm member stays signed in to both, and signing out of one (`/v1/auth/logout?staff=1` for staff) leaves the other. The session check for each rejects the other kind's row. The edge Worker serves the console's pages and files only to a request whose staff cookie the API accepts; a signed-out visitor goes to staff sign-in. The console is therefore **served only with `AUTH_SESSION=server`**; in `client` mode, `/admin` answers 503.

**Turning server sessions on** (a merge alone does not; production deploys on every merge but keeps `client` until the variable changes):

1. Install `WORKOS_API_KEY` directly in Cloudflare with `node scripts/cloudflare-credentials.mjs WORKOS_API_KEY production < private-key-file` (the API and worker both receive it); use `staging` for that environment.
2. In the WorkOS dashboard, add `https://<app>/v1/auth/callback` as a redirect URI, and `https://<app>/` and `https://<app>/admin/` as sign-out redirects.
3. Set the variable `AUTH_SESSION=server` and deploy.
4. Sign in, reload the page, wait more than five minutes and reload again (a refresh), then sign out. Do the same as staff at `/admin/`.

**Going back to WorkOS's browser session** with a custom domain: set up the WorkOS custom authentication domain, set `WORKOS_API_HOSTNAME=auth.<your-domain>` and `AUTH_SESSION=client`, and deploy. Everyone signs in once more. The staff console is then not served until it has another gate that the edge can check, such as Cloudflare Access on `/admin` (see [admin panel](admin-panel.md#network-phase-a4)). The backend session is otherwise unused and can be removed: `services/backend/internal/transport/session.go`, its registration in `apiWithStore`, the server branches in `apps/frontend/src/features/auth/Auth.tsx` and `apps/admin/src/auth.tsx`, and the `auth_sessions` table.

