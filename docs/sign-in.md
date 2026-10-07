# Sign-in and firms by company domain

People sign in to Krith with their firm's **Google Workspace** account. The login page (`/login`) has one button, **Continue with Google**, which goes straight to Google through WorkOS; there is no intermediate AuthKit page.

**Firms on Microsoft 365 cannot sign in yet.** WorkOS's Microsoft OAuth serves personal Microsoft accounts only, never a firm's Microsoft 365 accounts, so there is no Microsoft button. Setting such a firm up by hand would not help: its people would still have no way to sign in. The page says that Krith signs firms in with Google Workspace for now, and offers "Talk to us". A Microsoft 365 firm can be admitted once one of two paths is built and validated: a work-email code (WorkOS Magic Auth on our own page), or the firm's own Entra ID through WorkOS SSO. Someone at a Microsoft 365 firm whose Google account uses their work address can already sign in with Google. Personal addresses cannot sign in. A firm is created from its company domain the first time someone from it signs in, and colleagues from the same domain join it automatically. This needs the server-held session (`AUTH_SESSION=server`, see [auth and sandbox staging](auth-sandbox-staging.md#sign-in-session)); with WorkOS's browser session the page keeps a single **Sign in** button, because its SDK always opens AuthKit's own page.

## Who may sign in

- **Company addresses only.** A domain belonging to a personal mail provider is refused: Gmail, Outlook.com and Hotmail, Live, Yahoo, iCloud, Proton, GMX, Zoho Mail, Yandex, AOL, the large ISPs and the country variants of each (`companyDomain` in `services/backend/internal/transport/domains.go`). The login page says to use the firm's Google Workspace or Microsoft 365 account. Firm invitations to a personal address are refused for the same reason.
- **Firms on Google Workspace or Microsoft 365.** No list of personal providers is complete, so a firm is created by sign-in only when the domain's mail is hosted by one of the two products the login page offers (its MX records point at Google or at Microsoft 365's `mail.protection.outlook.com`; `hostedMail`). A shared mail provider's domain fails, and a domain's MX records are set by whoever controls it. A firm whose mail goes through another gateway (Mimecast, Proofpoint and the like) is refused with `hosting` and set up by hand instead.
- **Verified addresses only** sign in, create or join a firm. A member whose address WorkOS stops counting as verified is turned away too, and their session ends at its next renewal (within five minutes). WorkOS's `email_verified` decides. This matters for Microsoft: a company's IT can set any address on its own accounts, so the address alone proves nothing until WorkOS has verified it (WorkOS requires email verification in this environment).

## The first person creates the firm

When someone with a verified company address signs in and belongs to no firm, the API (`domainFirm`):

1. Takes the domain's lock, so two colleagues signing in at once make one firm.
2. Checks that the domain's mail is hosted by Google Workspace or Microsoft 365, then finds or makes the WorkOS organization: one WorkOS already holds for the domain is used (one set up by hand, one WorkOS placed the person in, or one left by an attempt that failed); otherwise it creates one named after the domain, with the domain marked verified.
3. Gives the person a WorkOS membership with the **Firm admin** role (`firm-admin`), raising an existing membership to it.
4. Registers the firm (`organizations.email_domain`, migration 125) and records the person as its first member.
5. Renews the sign-in into that organization, so their session carries the firm and its permissions.

A firm the app already knows under that WorkOS organization (one set up by hand) is not given a new administrator while it has members: the domain is recorded and the person joins as a member. A firm with no member yet (set up by hand and never enrolled, or one whose members were all removed) takes the person who arrives as its Firm admin, as a new firm does, so someone can always enroll people and give roles.

## Colleagues join as members with no access

A person WorkOS placed in an organization the app has not registered goes through the same steps, so that organization becomes the firm. Anyone who later signs in with a verified address at the same domain joins that firm (`joinFirm`): a WorkOS membership with the **Member** role, which holds no permissions, and a record that they joined by their address (actor `company-domain`). A role WorkOS held for them before is set to Member too: what they may open is the administrator's decision. Someone the firm invited is the exception: they join through the invitation, with its role. Until WorkOS shows the invitation accepted by them, they are asked to accept it, even if WorkOS has already given them a membership by their domain. An expired invitation counts for nothing. They can sign in but open nothing until the firm's administrator gives them a role, or assigns them clients: a client assignment is the administrator's grant too, and shows that client's closes to the person whatever their role, as it does for any member. WorkOS's own domain provisioning, which adds people at the organization's verified domain as members, ends in the same place: the API records them the first time they sign in (`domainMember`). A firm set up by hand without a recorded domain takes the domain then, if its WorkOS organization has that domain verified and no other firm holds it.

Someone the firm removed is not let back in by their address, nor is anyone while the firm is suspended. A WorkOS membership that has been deactivated is never revived here, and one still pending (an invitation awaiting acceptance) gives no access until accepted. A membership is created only for someone joining for the first time: for a person the firm already enrolled, a WorkOS membership that was removed stays removed, and they are turned away. **Firms set up by hand.** A firm set up by hand begins as a WorkOS organization whose administrator holds `org:enroll`. Signed in to that organization, they keep it, whatever their mail provider, and use **Enroll a new firm** as before; the workspace opens once the firm is enrolled. This is also the path for firms whose mail runs through another gateway.

`firmFor` decides which firm a session is for, in this order:
1. **A firm where they are a member or have accepted its invitation.** If WorkOS signs the person in to such a registered firm, they stay there. An invitation counts only once WorkOS shows it accepted by them; every open invitation is checked. An accountant a client's firm invited keeps working there, even when their own domain has a firm.
2. **Their domain's registered firm.** Otherwise they go to it, joining if need be.
3. **Another registered firm.** Otherwise they stay in the registered firm WorkOS holds them in, only if it admits them there (a firm set up by hand whose WorkOS organization has their domain verified); one that removed them or has not admitted them does not hold them.
4. **Their domain's claim.** Otherwise the claim runs, which may create the firm or take over an organization WorkOS placed them in.

**Choosing a firm.** When WorkOS asks which organization to use and the person works in more than one firm (holds its access, or has accepted its invitation), the login page asks them which to open (`/login?choose=1`).
- The pending sign-in waits, sealed in a ten-minute HttpOnly cookie.
- `GET /v1/auth/choices` lists their firms.
- `POST /v1/auth/choose` finishes the sign-in into the one they pick, under the same rules. Only from the site itself, and only one of the listed firms.

With a single such firm, the order is:
1. a firm whose invitation WorkOS shows accepted;
2. the firm they last signed in to;
3. their domain's firm.

An invitation not yet accepted never takes them away from a firm they work in.

Every firm session starts from a token issued after the firm is settled, so it carries the role the person holds after joining. Switching firms within a session is not offered: sign out and sign in again to choose.

## Roles

The firm's administrator manages roles in the workspace under **Security center → Firm operations and membership → People and roles**. It lists the firm's people, fifty at a time (**Show more people**), with their role and firm access. **Change role** gives a person a role, with a reason and confirmation:

- Only a role whose permissions the administrator holds can be given, never one's own role.
- Only someone with current firm access can be given a role; a removed person is enrolled again first.
- A change is recorded under the firm's row lock, with the person's role read then. A person can have one change pending at a time, and a change made from a stale list (the role it saw is no longer theirs) is refused. Changes to different people are independent; their records are in the order they were made.
- WorkOS holds the role. The firm keeps each change in `member_role_changes`: before, after, why and by whom.
- The change is recorded as pending before WorkOS is asked. It is marked applied when WorkOS confirms, and failed when WorkOS refuses. When WorkOS's answer is lost, the next load of the list settles it from the role WorkOS reports: the new role means applied; the old one, a minute later, means failed; any other role (changed in WorkOS meanwhile) is recorded as unknown rather than guessed. One change per person can be pending at a time.
- The change takes effect when the person's session next renews, within five minutes.

Endpoints: `GET /v1/closes/members/people` and `POST /v1/closes/members/role`. Both need `clients:all` and `members:manage`.

## Signed out, there is no workspace

A signed-out visitor to any `/dashboard` page goes to `/login` and, once signed in, back to that page, anchor included. Nothing of the workspace shows first, not even while the session is checked: no sidebar, no preparation tools, no examples. A session that cannot be checked (the API or WorkOS unreachable) is not treated as a sign-out: the workspace stays closed and says so, with a way to try again.

The worked example (the example close and review, a synthetic prepaid schedule) is Krith's own. Only the firm registered for `STAFF_EMAIL_DOMAIN` sees it in its sidebar and can read it (`GET /v1/closes/examples/prepaid`); every other firm gets 404 there. Local development, without WorkOS, keeps it.

## Welcome emails

Krith emails each person once when they come into a firm by signing in:
- **Welcome to Krith:** to whoever becomes a firm's administrator by signing in, either by creating it or by being the first member of a firm with none. It says the firm is set up, links to the workspace, and says colleagues at the domain join with no access until given a role or clients.
- **You've joined <firm> on Krith:** to each colleague who joins by their address. It says they can't open any clients until the administrator decides.

How it works:
- **Queued with the sign-in.** `domainFirm` and `joinFirm` write the email to `platform_emails` (migration 130) in the sign-in's own transaction, so a sign-in that fails sends nothing. A person gets one per firm, however often they sign in. People who come by invitation get WorkOS's invitation email instead.
- **Sent by the worker.** The worker's `platform_emails` loop (`delivery.RunPlatformEmails`) sends each one through Resend, with Krith's own send-only key and sender, never a firm's connection. The idempotency key is the row's, so a retry never sends twice.
- **Only while they belong.** Before sending, the worker checks that the firm is still enabled and the person still a member. If either has changed, nothing more is sent: an email never tried is cancelled, and one tried before stays unknown, since it may have gone.
- **Retries and refusals.** An attempt with an uncertain outcome (no answer, a server error, or success without Resend's message ID) is retried twice, a minute apart. If it is still uncertain, it is recorded as unknown, because Resend may have sent it. A refusal is final: failed, or unknown when it answers a retry, since the attempt before may have been sent. A last attempt the worker stopped holding before Resend answered is unknown too.
- **Unknown emails alert.** Each unknown email raises the platform alert `platform.unknown_email` ([operations](operations.md)). The alert names the row and its idempotency key `krith-platform/<id>`, never the address. Reconcile by finding that key in Resend's log. The alert resolves when the row is settled or deleted.
- **Expiry and deletion.** Anything never tried within a day of the sign-in expires; anything tried but unconfirmed by then is unknown. Rows are deleted 30 days after they finish, and with their firm.

Settings:
- `PLATFORM_EMAIL_FROM` is a GitHub environment variable, e.g. `Krith <hello@mail.getkrith.com>`, on the sender domain verified in Resend.
- `PLATFORM_EMAIL_KEY` is a Cloudflare credential of the queue worker: a Resend key with sending access to that domain only, installed with `node scripts/cloudflare-credentials.mjs PLATFORM_EMAIL_KEY production < key-file`.
- Links use `PUBLIC_APP_URL`.
- Without the key and a valid sender, the worker logs that platform emails are off and sends nothing.

## Why a sign-in is turned away

The callback sends the person back to `/login?error=…`, and the page explains it. Nothing about the person goes in the address.

| Code | Meaning |
| --- | --- |
| `company` | A personal address |
| `ask` | Their domain's organization is a firm registered under another domain; they join it by invitation |
| `invited` | The firm invited this person and they have not accepted yet |
| `hosting` | A new firm's domain whose mail is not hosted by Google Workspace or Microsoft 365 |
| `unverified` | An address WorkOS has not verified |
| `removed` | The firm removed this person, or WorkOS deactivated their membership |
| `suspended` | The firm is paused |
| `staff` | A staff console sign-in from an address not at `STAFF_EMAIL_DOMAIN` ([admin panel](admin-panel.md#staff-by-email-domain)) |
| `unavailable` | WorkOS refused the code (already used, or too old) or could not be reached, or the database could not finish; try again. A staff console sign-in goes back to the console page it was for instead |
| `expired` | The callback did not match a sign-in started in this browser: a reload, the back button, or an old tab. Nothing is asked of WorkOS. A staff console sign-in goes back to the console page it was for instead |

After `expired` or `unavailable`, the login page keeps the workspace page the sign-in was for (`return`), so signing in again goes there.

**A sign-in WorkOS started itself.** WorkOS returns a sign-in that did not start on our login page (an accepted invitation, or a tab left from before server sessions) to its default redirect URI, `/login`, with a code. Only a sign-in started here can use a code, since its state is checked against the browser's cookie. So the login page removes the code from the address and says "Your sign-in isn't finished yet. Continue with Google to finish it."

## Setup (once per environment)

In Google Cloud (the owner, since it issues a client secret):

1. **APIs & Services → OAuth consent screen:**
   - Audience External, published.
   - App name Krith, home page `https://getkrith.com`, privacy policy `https://getkrith.com/privacy`, authorised domain `getkrith.com`.
   - No extra scopes: WorkOS asks for basic sign-in only.
2. **Clients → Create client:** a Web application whose authorised redirect URI is the one WorkOS shows under Authentication → OAuth providers → Google → Manage.

In WorkOS (production environment):

- **Authentication → OAuth providers → Google:** enter the client ID and secret ("Your app's credentials") and enable it. Done 2026-10-06.
- **Turn off password and passkey sign-in, and keep every other social provider (Microsoft among them) off**, so AuthKit's own page (still used for any unknown provider name) offers only Google. Check the AuthKit page afterwards. Done in production 2026-10-06: Google is the only method on (password, passkeys and Magic Auth off; Microsoft OAuth was never enabled).
- **Roles:** `firm-admin` and `member` exist. Keep `member` without permissions.
- **Redirect URIs:** keep `https://getkrith.com/v1/auth/callback` registered, the default redirect URI `https://getkrith.com/login` (where WorkOS sends a sign-in it started itself), and the sign-out URI `https://getkrith.com/`.

In GitHub (production environment variables): set `AUTH_SESSION=server`, then run Deploy production. The API needs `WORKOS_API_KEY` and `SECRETS_KEY`, which production already has.

## Tests

`TestOnlyCompanyAddressesCount` covers the address rules. `TestSignInMakesOrJoinsTheFirmOfTheAddressDomain` runs the whole flow against a fake WorkOS:

- the first person creates the firm, with its domain verified, and becomes its administrator;
- a colleague joins as a member with no access;
- Gmail, unverified, removed and non-hosted-domain people are turned away;
- someone WorkOS provisioned by domain is recorded;
- an organization WorkOS placed someone in becomes the firm;
- the administrator and the colleague each have one email queued, and signing in again queues none;
- a hand-made firm takes its verified domain;
- the administrator lists people and changes a role, but not their own, and only to a role they hold;
- a refused change is recorded as failed, and a change whose answer was lost settles as applied;
- a person in two firms lands in their domain's firm.

The welcome emails have their own tests in `internal/delivery`:
- `TestPlatformEmailsSay` checks what each email says, and that a malformed recipient or an unknown kind is refused.
- `TestPlatformEmailsNeedKrithsSender` checks that nothing is sent without the key and a valid sender.
- `TestPlatformEmailsAreSentOnceWithKrithsKey` runs against a fake Resend. It checks that emails go out with Krith's key and each row's own idempotency key. An uncertain attempt is retried under the same key, including a success without Resend's message ID, and is recorded as unknown after three tries. A refusal is final, but unknown when it answers a retry. An abandoned last attempt, or one unconfirmed after a day, is unknown. `TestUnknownPlatformEmailsAlertUntilSettled` checks the alert: it names the key, not the address, and resolves once the email is settled. An email for someone removed before it went out, or for a suspended firm, is cancelled unsent, or stays unknown if an attempt went unanswered. An email older than a day expires unsent, and a row finished over 30 days ago is deleted.
