package transport

import (
	"context"
	"database/sql"

	"close.local/backend/internal/delivery"
)

// firmSender is the firm's current sender, read in tx: the integration the firm connected, or else Krith's own
// (docs/email-delivery.md#senders). Krith's is recorded the first time it is needed while this server has Krith's
// sending address, and recorded again when the firm's name or that address changed, since its From names both. With
// neither, the firm has no sender (sql.ErrNoRows).
func firmSender(ctx context.Context, tx *sql.Tx, org string) (id, sender, provider string, err error) {
	read := func() (string, bool, error) {
		var name string
		var enabled bool
		var current sql.NullString
		e := tx.QueryRowContext(ctx, `SELECT o.name,o.enabled,c.id::text,COALESCE(c.sender,''),COALESCE(c.provider,'') FROM organizations o
 LEFT JOIN email_connections c ON c.organization_id=o.id AND c.enabled WHERE o.id=$1`, org).Scan(&name, &enabled, &current, &sender, &provider)
		id = current.String
		return name, enabled, e
	}
	name, enabled, err := read()
	if err != nil {
		return "", "", "", err
	}
	address := delivery.KrithAddress()
	if id != "" && (provider != delivery.Krith || address == "" || sender == delivery.KrithSender(name, address)) {
		return id, sender, provider, nil
	}
	if address == "" || !enabled || org == "local-development" {
		if id == "" {
			return "", "", "", sql.ErrNoRows
		}
		return id, sender, provider, nil
	}
	// Lock the firm, as connecting a sender does, and decide again.
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM organizations WHERE id=$1 FOR UPDATE`, org); err != nil {
		return "", "", "", err
	}
	if name, enabled, err = read(); err != nil {
		return "", "", "", err
	}
	want := delivery.KrithSender(name, address)
	if id != "" && (provider != delivery.Krith || sender == want) || id == "" && !enabled {
		if id == "" {
			return "", "", "", sql.ErrNoRows
		}
		return id, sender, provider, nil
	}
	if id != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE email_connections SET enabled=false WHERE id=$1`, id); err != nil {
			return "", "", "", err
		}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO email_connections(organization_id,provider,sender,domain_id,actor) VALUES($1,'krith',$2,'','krith') RETURNING id::text`, org, want).Scan(&id)
	if err != nil {
		return "", "", "", err
	}
	return id, want, delivery.Krith, nil
}
