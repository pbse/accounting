package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Firms by company email domain (docs/sign-in.md). Krith admits only company addresses: a personal provider's domain
// (Gmail, Outlook.com, iCloud and the like) cannot sign in. The first person with a verified address at a company
// domain creates that domain's firm, as its administrator; anyone who signs in later with a verified address at the
// same domain joins it as a member with no access until the firm's administrator gives them a role.

// personalDomains are mail providers anyone can sign up to, so an address there says nothing about a firm.
var personalDomains = map[string]bool{}

func init() {
	for _, d := range strings.Fields(`
		gmail.com googlemail.com yahoo.com ymail.com rocketmail.com outlook.com hotmail.com live.com msn.com passport.com
		icloud.com me.com mac.com aol.com aim.com proton.me protonmail.com protonmail.ch pm.me gmx.com gmx.net gmx.us
		web.de mail.com email.com zoho.com zohomail.com yandex.com yandex.ru mail.ru inbox.com hey.com fastmail.com
		fastmail.fm tutanota.com tuta.io tutamail.com duck.com hushmail.com rediffmail.com qq.com 163.com 126.com sina.com
		naver.com daum.net comcast.net verizon.net att.net sbcglobal.net bellsouth.net cox.net charter.net earthlink.net
		optonline.net btinternet.com sky.com virginmedia.com talktalk.net orange.fr free.fr laposte.net sfr.fr libero.it
		t-online.de shaw.ca rogers.com sympatico.ca bigpond.com optusnet.com.au`) {
		personalDomains[d] = true
	}
}

// personalFamilies are providers whose every country domain is personal: yahoo.co.uk, hotmail.fr, gmx.de and so on.
var personalFamilies = []string{"gmail", "googlemail", "yahoo", "ymail", "hotmail", "outlook", "live", "aol", "gmx", "yandex", "protonmail", "rediffmail"}

var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// companyDomain returns an address's domain, and whether it is a company's: well formed and not a personal provider's.
func companyDomain(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(email, '@')
	if at < 1 {
		return "", false
	}
	domain := strings.TrimSuffix(email[at+1:], ".")
	if len(domain) > 253 || !domainPattern.MatchString(domain) || personalDomains[domain] {
		return domain, false
	}
	first, rest, _ := strings.Cut(domain, ".")
	// A family member is personal only under a public suffix of one or two labels (hotmail.co.uk), never as part of a
	// company's own name (live.example-firm.com stays a company domain).
	if slices.Contains(personalFamilies, first) && strings.Count(rest, ".") <= 1 && len(strings.Split(rest, ".")[0]) <= 3 {
		return domain, false
	}
	return domain, true
}

// Why a sign-in was turned away; the login page explains each in words.
const (
	refusePersonal    = "company"
	refuseUnverified  = "unverified"
	refuseRemoved     = "removed"
	refuseSuspended   = "suspended"
	refuseUnavailable = "unavailable"
)

type firmMembership struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
	Role           struct {
		Slug string `json:"slug"`
	} `json:"role"`
}

var errMembershipInactive = errors.New("WorkOS membership is not active")

// ensureMembership gives the person a WorkOS membership of the firm with this role, unless they already hold one. A
// membership WorkOS has deactivated is never revived here; one they hold keeps its role unless raise is set, which the
// firm's first administrator needs.
func ensureMembership(ctx context.Context, org, user, role string, raise bool) error {
	held, err := workosList[firmMembership](ctx, "/user_management/organization_memberships", url.Values{"user_id": {user}, "organization_id": {org}, "limit": {"100"}})
	if err != nil {
		return err
	}
	for _, m := range held {
		if m.Status != "active" && m.Status != "pending" {
			return errMembershipInactive
		}
		if raise && m.Role.Slug != role {
			return workos(ctx, "PUT", "/user_management/organization_memberships/"+url.PathEscape(m.ID), map[string]string{"role_slug": role}, nil)
		}
		return nil
	}
	return workos(ctx, "POST", "/user_management/organization_memberships", map[string]string{"user_id": user, "organization_id": org, "role_slug": role}, nil)
}

// domainFirm finds or creates the firm for a person signing in with no firm yet. It runs one domain at a time: a
// firm already registered for the domain takes them in as a member with no access; otherwise the domain's WorkOS
// organization (created with the domain verified, unless one already carries it) becomes a new firm with them as its
// administrator. A person the firm removed, or a suspended firm, is turned away.
func domainFirm(ctx context.Context, db *sql.DB, user signedInUser) (org, refusal string) {
	domain, company := companyDomain(user.Email)
	switch {
	case !company:
		return "", refusePersonal
	case !user.EmailVerified:
		return "", refuseUnverified
	}
	ctx = systemContext(ctx)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", refuseUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('company-domain:'||$1))`, domain); err != nil {
		return "", refuseUnavailable
	}
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT id,enabled FROM organizations WHERE email_domain=$1`, domain).Scan(&org, &enabled)
	if err == nil {
		if refusal = joinFirm(ctx, tx, org, enabled, user, domain); refusal != "" {
			return "", refusal
		}
		return org, commitOr(tx)
	}
	if err != sql.ErrNoRows {
		return "", refuseUnavailable
	}
	// No firm has the domain yet. An organization WorkOS already holds for it (one set up by hand, or left by an
	// attempt that failed after creating it) is used rather than a second one.
	var found []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if found, err = workosList[struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}](ctx, "/organizations", url.Values{"domains": {domain}, "limit": {"10"}}); err != nil {
		return "", refuseUnavailable
	}
	name := domain
	if len(found) > 0 {
		org, name = found[0].ID, found[0].Name
	} else {
		var created struct {
			ID string `json:"id"`
		}
		body := map[string]any{"name": domain, "domain_data": []map[string]string{{"domain": domain, "state": "verified"}}}
		if workos(ctx, "POST", "/organizations", body, &created) != nil || !organizationID.MatchString(created.ID) {
			return "", refuseUnavailable
		}
		org = created.ID
	}
	if !organizationID.MatchString(org) || org == os.Getenv("PLATFORM_ORGANIZATION_ID") {
		return "", refuseUnavailable
	}
	// A firm the app already knows under that organization keeps its administrators: the domain is recorded and this
	// person joins as a member like anyone else.
	err = tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&enabled)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE organizations SET email_domain=$2 WHERE id=$1 AND email_domain IS NULL`, org, domain); err != nil {
			return "", refuseUnavailable
		}
		if refusal = joinFirm(ctx, tx, org, enabled, user, domain); refusal != "" {
			return "", refusal
		}
		return org, commitOr(tx)
	}
	if err != sql.ErrNoRows {
		return "", refuseUnavailable
	}
	if ensureMembership(ctx, org, user.ID, "firm-admin", true) != nil {
		return "", refuseUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organizations(id,name,email_domain) VALUES($1,$2,$3)`, org, name, domain); err != nil {
		return "", refuseUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,$3,$2)`,
		org, user.ID, "Created the firm by signing in with a verified address at "+domain); err != nil {
		return "", refuseUnavailable
	}
	return org, commitOr(tx)
}

// joinFirm takes a person into the domain's firm as a member with no access: a WorkOS membership with the member role,
// and the firm's record that they joined by their address. A removed person, or a suspended firm, stays out.
func joinFirm(ctx context.Context, tx *sql.Tx, org string, enabled bool, user signedInUser, domain string) string {
	if !enabled {
		return refuseSuspended
	}
	var granted sql.NullBool
	if err := tx.QueryRowContext(ctx, `SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject=$2`, org, user.ID).Scan(&granted); err != nil && err != sql.ErrNoRows {
		return refuseUnavailable
	}
	if granted.Valid && !granted.Bool {
		return refuseRemoved
	}
	switch err := ensureMembership(ctx, org, user.ID, "member", false); {
	case errors.Is(err, errMembershipInactive):
		return refuseRemoved
	case err != nil:
		return refuseUnavailable
	}
	if !granted.Valid {
		if _, err := tx.ExecContext(ctx, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,$3,'company-domain')`,
			org, user.ID, "Joined with a verified address at "+domain+"; no access until an administrator gives a role"); err != nil {
			return refuseUnavailable
		}
	}
	return ""
}

// domainMember records, the first time, that someone WorkOS already placed in a firm by its verified domain (WorkOS's
// own domain provisioning) belongs to it. Anyone else in the firm came by invitation and is enrolled as before.
func domainMember(ctx context.Context, db *sql.DB, org string, user signedInUser) string {
	domain, company := companyDomain(user.Email)
	if !company {
		return refusePersonal
	}
	ctx = systemContext(ctx)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return refuseUnavailable
	}
	defer tx.Rollback()
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 AND email_domain=$2 FOR UPDATE`, org, domain).Scan(&enabled)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		return refuseUnavailable
	}
	if !user.EmailVerified {
		return refuseUnverified
	}
	if refusal := joinFirm(ctx, tx, org, enabled, user, domain); refusal != "" {
		return refusal
	}
	return commitOr(tx)
}

func commitOr(tx *sql.Tx) string {
	if tx.Commit() != nil {
		return refuseUnavailable
	}
	return ""
}

var organizationID = regexp.MustCompile(`^org_[A-Za-z0-9]+$`)
