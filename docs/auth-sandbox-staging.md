# Authentication and sandbox setup

WorkOS AuthKit is the implemented hosted identity provider. The application supports multiple enrolled firms and client assignments; it also has a shared local-development token. Provider activation and live checks are separate from code availability. Follow [onboarding](onboarding.md) and [deployment](deployment.md) for the current complete setup.

## Configure WorkOS

1. Use separate WorkOS staging and production environments. Enable Google sign-in and the desired email login option. The provided auth domain is sufficient; a paid custom auth domain is optional.
2. Register the frontend origin, `/login` on that origin as redirect/login URI, and the origin as sign-out URI. Configure the firm's MFA policy in WorkOS.
3. Set backend `WORKOS_CLIENT_ID` and exact `WORKOS_ISSUER`. Frontend uses `VITE_WORKOS_CLIENT_ID` and `VITE_WORKOS_API_HOSTNAME`; Compose and GitHub derive these from the root/environment WorkOS settings. Copy the environment's hostname and issuer, not values from an unverified incoming token.
4. Create a WorkOS organization for each accounting firm and invite staff. Give the first administrator `org:enroll`, `members:manage` and `clients:all`, then sign in and enroll the firm in the app.
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
