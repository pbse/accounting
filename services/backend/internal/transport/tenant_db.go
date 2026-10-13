package transport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

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
		if strings.Contains(strings.ToLower(host.Host), "-pooler") {
			return nil, errors.New("DATABASE_URL must be a direct connection, not a transaction pooler")
		}
	}
	// The driver pings a connection idle for over a second before reusing it, a round trip on most requests. Here only a
	// connection idle for over a minute is pinged; one the driver already knows is closed is still discarded unpinged.
	ping := stdlib.OptionShouldPing(func(_ context.Context, p stdlib.ShouldPingParams) bool { return p.IdleDuration > time.Minute })
	return sql.OpenDB(tenantConnector{stdlib.GetConnector(*config, ping), fallback}), nil
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
	// inTx is set while a transaction is open; changedInTx records that the firm setting changed inside it, so only
	// then does a rollback (which undoes that change) leave the setting unknown.
	inTx, changedInTx bool
}

func (c *tenantConn) use(ctx context.Context) error {
	tenant := tenantOf(ctx, c.fallback)
	if tenant == c.tenant {
		return nil
	}
	defer roundTrip(ctx)()
	// JIT goes off in the same trip, and comes back with the firm whenever a rollback undoes it: the planner's cost for
	// a read like the attention inbox passes JIT's threshold on a few dozen rows, and compiling its plan took 670 ms of
	// a 700 ms read that runs in 30 ms without it.
	if _, err := c.Conn.ExecContext(ctx, `SELECT set_config('app.organization',$1,false),set_config('jit','off',false)`, []driver.NamedValue{{Ordinal: 1, Value: tenant}}); err != nil {
		c.tenant = "\x00"
		return err
	}
	c.tenant = tenant
	if c.inTx {
		c.changedInTx = true
	}
	return nil
}

func (c *tenantConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.use(ctx); err != nil {
		return nil, err
	}
	done := roundTrip(ctx)
	rows, err := c.Conn.QueryContext(ctx, query, args)
	if err != nil {
		done()
		return nil, err
	}
	// The driver reads a reply's later rows as they are asked for, so the trip ends when the rows are closed.
	if r, ok := rows.(*stdlib.Rows); ok {
		return &timedRows{Rows: r, done: done}, nil
	}
	done()
	return rows, nil
}

// timedRows is the driver's rows, with every column-type method it has, ending the round trip's timing on Close.
type timedRows struct {
	*stdlib.Rows
	done func()
	once sync.Once
}

func (r *timedRows) Close() error {
	err := r.Rows.Close()
	r.once.Do(r.done)
	return err
}

func (c *tenantConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.use(ctx); err != nil {
		return nil, err
	}
	defer roundTrip(ctx)()
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
	done := roundTrip(ctx)
	tx, err := c.Conn.BeginTx(ctx, opts)
	done()
	if err != nil {
		return nil, err
	}
	c.inTx, c.changedInTx = true, false
	return tenantTx{tx, c, ctx}, nil
}

// A rollback also undoes a setting changed inside the transaction, so only then is the connection's firm unknown
// afterwards. A setting made before BEGIN is a session setting and survives the rollback: a read-only page that ends
// its snapshot with a rollback leaves the next request on the same firm without another set_config.
type tenantTx struct {
	driver.Tx
	conn *tenantConn
	ctx  context.Context // the request's, so COMMIT and ROLLBACK count among its round trips
}

func (t tenantTx) Commit() error {
	done := roundTrip(t.ctx)
	err := t.Tx.Commit()
	done()
	if err != nil && t.conn.changedInTx {
		t.conn.tenant = "\x00"
	}
	t.conn.inTx, t.conn.changedInTx = false, false
	return err
}

func (t tenantTx) Rollback() error {
	if t.conn.changedInTx {
		t.conn.tenant = "\x00"
	}
	t.conn.inTx, t.conn.changedInTx = false, false
	defer roundTrip(t.ctx)()
	return t.Tx.Rollback()
}
