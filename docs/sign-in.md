# Sign-in and firms by company domain

People sign in to Krith with their firm's **Google Workspace** or **Microsoft 365** account. The login page (`/login`) has two buttons, **Continue with Google** and **Continue with Microsoft**, and each goes straight to that provider through WorkOS; there is no intermediate AuthKit page. Personal addresses cannot sign in. A firm is created from its company domain the first time someone from it signs in, and colleagues from the same domain join it automatically. This needs the server-held session (`AUTH_SESSION=server`, see [auth and sandbox staging](auth-sandbox-staging.md#sign-in-session)); with WorkOS's browser session the page keeps a single **Sign in** button, because its SDK always opens AuthKit's own page.

## Who may sign in

- **Company addresses only.** A domain belonging to a personal mail provider is refused: Gmail, Outlook.com and Hotmail, Live, Yahoo, iCloud, Proton, GMX, Zoho Mail, Yandex, AOL, the large ISPs and the country variants of each (`companyDomain` in `services/backend/internal/transport/domains.go`). The login page says to use the firm's Google Workspace or Microsoft 365 account. Firm invitations to a personal address are refused for the same reason.
- **Firms on Google Workspace or Microsoft 365.** No list of personal providers is complete, so a firm is created by sign-in only when the domain's mail is hosted by one of the two products the login page offers (its MX records point at Google or at Microsoft 365's `mail.protection.outlook.com`; `hostedMail`). A shared mail provider's domain fails, and a domain's MX records are set by whoever controls it. A firm whose mail goes through another gateway (Mimecast, Proofpoint and the like) is refused with `hosting` and set up by hand instead.
- **Verified addresses only** sign in, create or join a firm; a member whose address WorkOS stops counting as verified is turned away too. WorkOS's `email_verified` decides. This matters for Microsoft: a company's IT can set any address on its own accounts, so the address alone proves nothing until WorkOS has verified it (WorkOS requires email verification in this environment).

## The first person creates the firm

When someone with a verified company address signs in and belongs to no firm, the API (`domainFirm`):

1. Takes the domain's lock, so two colleagues signing in at once make one firm.
2. Checks that the domain's mail is hosted by Google Workspace or Microsoft 365, then finds or makes the WorkOS organization: one WorkOS already holds for the domain is used (one set up by hand, one WorkOS placed the person in, or one left by an attempt that failed); otherwise it creates one named after the domain, with the domain marked verified.
3. Gives the person a WorkOS membership with the **Firm admin** role (`firm-admin`), raising an existing membership to it.
4. Registers the firm (`organizations.email_domain`, migration 124) and records the person as its first member.
5. Renews the sign-in into that organization, so their session carries the firm and its permissions.

A firm the app already knows under that WorkOS organization (one set up by hand) is not given a new administrator while it has members: the domain is recorded and the person joins as a member. A firm with no member yet (set up by hand and never enrolled, or one whose members were all removed) takes the person who arrives as its Firm admin, as a new firm does, so someone can always enroll people and give roles.

## Colleagues join as members with no access

A person WorkOS placed in an organization the app has not registered goes through the same steps, so that organization becomes the firm. Anyone who later signs in with a verified address at the same domain joins that firm (`joinFirm`): a WorkOS membership with the **Member** role, which holds no permissions, and a record that they joined by their address (actor `company-domain`). A role WorkOS held for them before is set to Member too: what they may open is the administrator's decision. Someone the firm invited is the exception: they join through the invitation, with its role. Until WorkOS shows the invitation accepted by them, they are asked to accept it, even if WorkOS has already given them a membership by their domain. An expired invitation counts for nothing. They can sign in but open nothing until the firm's administrator gives them a role, or assigns them clients: a client assignment is the administrator's grant too, and shows that client's closes to the person whatever their role, as it does for any member. WorkOS's own domain provisioning, which adds people at the organization's verified domain as members, ends in the same place: the API records them the first time they sign in (`domainMember`). A firm set up by hand without a recorded domain takes the domain then, if its WorkOS organization has that domain verified and no other firm holds it.

Someone the firm removed is not let back in by their address, nor is anyone while the firm is suspended. A WorkOS membership that has been deactivated is never revived here. `firmFor` decides which firm a session is for, in this order:
1. **A firm where they are a member or have accepted its invitation.** If WorkOS signs the person in to such a registered firm, they stay there. An invitation counts only once WorkOS shows it accepted by them; every open invitation is checked. An accountant a client's firm invited keeps working there, even when their own domain has a firm.
2. **Their domain's registered firm.** Otherwise they go to it, joining if need be.
3. **Another registered firm.** Otherwise they stay in the registered firm WorkOS holds them in.
4. **Their domain's claim.** Otherwise the claim runs, which may create the firm or take over an organization WorkOS placed them in.

When WorkOS asks which organization to use, the order is:
1. a firm whose invitation WorkOS shows they have accepted (their first sign-in after accepting);
2. the firm they last signed in to where they are still a member;
3. their domain's firm.

An invitation not yet accepted never takes them away from a firm they work in.

Every firm session starts from a token issued after the firm is settled, so it carries the role the person holds after joining. Switching between firms within a session is not offered yet.

## Roles

The firm's administrator manages roles in the workspace under **Security center → Firm operations and membership → People and roles**. It lists the firm's people, fifty at a time (**Show more people**), with their role and firm access. **Change role** gives a person a role, with a reason and confirmation:

- Only a role whose permissions the administrator holds can be given, never one's own role.
- Only someone with current firm access can be given a role; a removed person is enrolled again first.
- WorkOS holds the role. The firm keeps each change in `member_role_changes`: before, after, why and by whom.
- The change is recorded as pending before WorkOS is asked. It is marked applied when WorkOS confirms, and failed when WorkOS refuses. When WorkOS's answer is lost, the next load of the list settles it from the role WorkOS reports: the new role means applied; the old one, a minute later, means failed. One change per person can be pending at a time.
- The change takes effect when the person's session next renews, within five minutes.

Endpoints: `GET /v1/closes/members/people` and `POST /v1/closes/members/role`. Both need `clients:all` and `members:manage`.

## Why a sign-in is turned away

The callback sends the person back to `/login?error=…`, and the page explains it. Nothing about the person goes in the address.

| Code | Meaning |
| --- | --- |
| `company` | A personal address |
| `invited` | The firm invited this person and they have not accepted yet |
| `hosting` | A new firm's domain whose mail is not hosted by Google Workspace or Microsoft 365 |
| `unverified` | An address WorkOS has not verified |
| `removed` | The firm removed this person, or WorkOS deactivated their membership |
| `suspended` | The firm is paused |
| `unavailable` | WorkOS or the database could not finish; try again |

## Setup (once per environment)

In Google Cloud and Microsoft Entra (the owner, since each issues a client secret):

- **Google:** create an OAuth client (Web application) with the redirect URI WorkOS shows for Google OAuth. Use an External consent screen limited to the `openid`, `email` and `profile` scopes.
- **Microsoft:** register an Entra application for **accounts in any organizational directory**, so work accounts only, with the redirect URI WorkOS shows for Microsoft OAuth. Then create a client secret.

In WorkOS (production environment):

- **Authentication → Google OAuth and Microsoft OAuth:** enter each client ID and secret, then enable them.
- **Turn off password and passkey sign-in**, so AuthKit's own page (still used by the staff console) offers only Google and Microsoft.
- **Roles:** `firm-admin` and `member` exist. Keep `member` without permissions.
- **Redirect URIs:** keep `https://getkrith.com/v1/auth/callback` registered, and the sign-out URI `https://getkrith.com/`.

In GitHub (production environment variables): set `AUTH_SESSION=server`, then run Deploy production. The API needs `WORKOS_API_KEY` and `SECRETS_KEY`, which production already has.

## Tests

`TestOnlyCompanyAddressesCount` covers the address rules. `TestSignInMakesOrJoinsTheFirmOfTheAddressDomain` runs the whole flow against a fake WorkOS:

- the first person creates the firm, with its domain verified, and becomes its administrator;
- a colleague joins as a member with no access;
- Gmail, unverified, removed and non-hosted-domain people are turned away;
- someone WorkOS provisioned by domain is recorded;
- an organization WorkOS placed someone in becomes the firm;
- a hand-made firm takes its verified domain;
- the administrator lists people and changes a role, but not their own, and only to a role they hold;
- a refused change is recorded as failed, and a change whose answer was lost settles as applied;
- a person in two firms lands in their domain's firm.
