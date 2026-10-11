package transport

import "context"

// clientNamed finds the client asked for under any of its names in firm org: its own name (its roster entry's, else the
// name its closes were opened with that is its own, else the own name its confirmed aliases record, or, with none of
// these, its newest close's), whether it is known, whether the member may open it under some name (allowed), and
// whether under its own name (whole). firm says the member has firm access.
func clientNamed(ctx context.Context, q rowReader, org, asked string, firm bool, who string) (name string, known, allowed, whole bool, err error) {
	err = q.QueryRowContext(ctx, `WITH client AS (SELECT client_identity($1,$2) AS id),
 names AS (SELECT r.client_entity AS name,0 AS rank,'' AS period FROM current_client_roster r,client WHERE r.organization_id=$1 AND client_identity(r.organization_id,r.client_entity)=client.id
  UNION ALL SELECT c.entity,CASE WHEN lower(c.entity)=client.id THEN 1 ELSE 3 END,c.period FROM client_closes c,client WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client.id
  UNION ALL SELECT a.client_entity,2,'' FROM client_aliases a,client WHERE a.organization_id=$1 AND a.retired_at IS NULL AND lower(a.client_entity)=client.id),
 own AS (SELECT name FROM names ORDER BY rank,period DESC LIMIT 1),
 granted AS (SELECT lower(g.client_entity) AS name FROM current_client_access g WHERE g.organization_id=$1 AND g.subject=$4 AND g.granted)
 SELECT COALESCE((SELECT name FROM own),''),EXISTS(SELECT 1 FROM names),
  $3 OR EXISTS(SELECT 1 FROM granted WHERE name IN (SELECT lower(name) FROM names)),
  $3 OR EXISTS(SELECT 1 FROM granted WHERE name=(SELECT lower(name) FROM own))`,
		org, asked, firm, who).Scan(&name, &known, &allowed, &whole)
	return
}
