package transport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"

	"close.local/backend/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// Hosted readiness: the runtime role must not bypass policies, and every table and view must apply them.
const rowSecurityEnforced = `SELECT NOT r.rolsuper AND NOT r.rolbypassrls
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r' AND (NOT c.relrowsecurity OR pg_has_role(current_user,c.relowner,'MEMBER') OR NOT EXISTS(SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid)))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='v' AND NOT coalesce('security_invoker=true'=ANY(c.reloptions),false))
 FROM pg_roles r WHERE r.rolname=current_user`

type systemKey struct{}

// system marks work not tied to one signed-in firm: token-checked public routes and
// provider webhooks. Row-level security then admits every firm.
func system(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(context.WithValue(r.Context(), systemKey{}, true)))
	}
}

func tenantOf(ctx context.Context, fallback string) string {
	if actor := auth.Actor(ctx); actor != nil {
		if actor.Organization != "" {
			return actor.Organization
		}
		return "local-development"
	}
	if ctx.Value(systemKey{}) != nil {
		return "*"
	}
	return fallback
}

// OpenTenantDB sets app.organization before each statement from the request's signed-in firm.
// Without an actor or a system mark it uses fallback: "" denies every row, "*" admits all.
func OpenTenantDB(url, fallback string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// The firm is a session setting; a transaction pooler could hand one firm's session to another's statement.
	for _, host := range append([]*pgconn.FallbackConfig{{Host: config.Host}}, config.Fallbacks...) {
		if strings.Contains(host.Host, "-pooler") {
			return nil, errors.New("DATABASE_URL must be a direct connection, not a transaction pooler")
		}
	}
	return sql.OpenDB(tenantConnector{stdlib.GetConnector(*config), fallback}), nil
}

type tenantConnector struct {
	driver.Connector
	fallback string
}

func (c tenantConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &tenantConn{Conn: conn.(*stdlib.Conn), fallback: c.fallback, tenant: "\x00"}, nil
}

type tenantConn struct {
	*stdlib.Conn
	fallback, tenant string
}

func (c *tenantConn) use(ctx context.Context) error {
	tenant := tenantOf(ctx, c.fallback)
	if tenant == c.tenant {
		return nil
	}
	if _, err := c.Conn.ExecContext(ctx, `SELECT set_config('app.organization',$1,false)`, []driver.NamedValue{{Ordinal: 1, Value: tenant}}); err != nil {
		return err
	}
	c.tenant = tenant
	return nil
}

func (c *tenantConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.use(ctx); err != nil {
		return nil, err
	}
	return c.Conn.QueryContext(ctx, query, args)
}

func (c *tenantConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.use(ctx); err != nil {
		return nil, err
	}
	return c.Conn.ExecContext(ctx, query, args)
}

// A prepared statement would run later as whichever firm the connection last served.
func (c *tenantConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported with row-level security")
}

func (c *tenantConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.use(ctx); err != nil {
		return nil, err
	}
	tx, err := c.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return tenantTx{tx, c}, nil
}

// A rollback also undoes a setting changed inside the transaction.
type tenantTx struct {
	driver.Tx
	conn *tenantConn
}

func (t tenantTx) Rollback() error {
	t.conn.tenant = "\x00"
	return t.Tx.Rollback()
}
