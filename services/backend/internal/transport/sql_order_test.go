package transport

import (
	"crypto/rand"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A query that selects a column cast to text (id::text) names its output column after the column, and in PostgreSQL a
// bare name in ORDER BY means the output column first: "ORDER BY id DESC" then sorts the ids as text, so "9" comes
// before "10" and the "latest" row is wrong once ids pass a power of ten. Every query that casts a column to text must
// order by it qualified (books_layouts.id), which always means the column. Queries are read whole, string constants
// they are built from included.
func TestNoQueryOrdersByAColumnItAlsoSelectsAsText(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	type pkgConsts map[string]ast.Expr
	consts := map[string]pkgConsts{}
	var files []*ast.File
	dirs := map[*ast.File]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if consts[dir] == nil {
			consts[dir] = pkgConsts{}
		}
		for _, decl := range f.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.CONST {
				for _, spec := range g.Specs {
					v := spec.(*ast.ValueSpec)
					for i, name := range v.Names {
						if i < len(v.Values) {
							consts[dir][name.Name] = v.Values[i]
						}
					}
				}
			}
		}
		files = append(files, f)
		dirs[f] = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// text is an expression's string value, string constants of its package inlined and anything else a placeholder.
	var text func(dir string, e ast.Expr, depth int) string
	text = func(dir string, e ast.Expr, depth int) string {
		if depth > 20 {
			return "?"
		}
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				s, err := strconv.Unquote(x.Value)
				if err == nil {
					return s
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				return text(dir, x.X, depth+1) + text(dir, x.Y, depth+1)
			}
		case *ast.Ident:
			if v, ok := consts[dir][x.Name]; ok {
				return text(dir, v, depth+1)
			}
		case *ast.ParenExpr:
			return text(dir, x.X, depth+1)
		}
		return "?"
	}
	cast := regexp.MustCompile(`(?:\b\w+\.)?(\w+)::text\b`)
	orderBy := regexp.MustCompile(`(?i)ORDER BY\s+((?:[^;)(]|\([^()]*\))*)`)
	for _, f := range files {
		path := dirs[f]
		dir := filepath.Dir(path)
		ast.Inspect(f, func(n ast.Node) bool {
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			// The outermost string expression is the query: a literal, or literals and constants joined by +.
			switch x := e.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
			case *ast.BinaryExpr:
				if x.Op != token.ADD {
					return true
				}
			default:
				return true
			}
			q := text(dir, e, 0)
			if !strings.Contains(strings.ToUpper(q), "ORDER BY") {
				return false
			}
			casted := map[string]bool{}
			for _, m := range cast.FindAllStringSubmatch(q, -1) {
				casted[m[1]] = true
			}
			for _, at := range orderBy.FindAllStringSubmatchIndex(q, -1) {
				m := []string{q[at[0]:at[1]], q[at[2]:at[3]]}
				before := strings.Join(strings.Fields(q[max(0, at[0]-120):at[0]]), " ")
				for _, part := range strings.Split(m[1], ",") {
					// A bare name, as the whole item: an expression (connection_id IS NOT DISTINCT FROM …) reads the column.
					words := strings.Fields(part)
					bare := len(words) > 0 && casted[words[0]] && (len(words) == 1 || strings.Contains(" ASC DESC NULLS LIMIT OFFSET FOR ", " "+strings.ToUpper(words[1])+" "))
					if bare {
						t.Errorf("%s: ORDER BY %s sorts the column's text, which the query also selects as %s::text: qualify it (table.%s), after …%s", fset.Position(e.Pos()), words[0], words[0], words[0], before)
					}
				}
			}
			return false
		})
	}
}

// The latest of a client's layouts is the newest, across a power of ten: ids 99 and 100 (or 999 and 1000) once sorted
// as text gave the older (issue #369).
func TestTheLatestLayoutIsTheNewestAcrossAPowerOfTen(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Order" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Order fixture')`, org)
	must(err)
	defer func() {
		db.Exec(`DELETE FROM books_layouts WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM books_imports WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	var file string
	must(db.QueryRow(`INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,actor) VALUES($1,'Acme Co','2026-09','a.csv',md5(random()::text)||md5(random()::text),'x','csv','fixture') RETURNING id::text`, org).Scan(&file))
	// The next two layouts take the ids either side of the next power of ten: only ever moved forward.
	_, err = db.Exec(`SELECT setval(pg_get_serial_sequence('books_layouts','id'), (10^(length((SELECT last_value FROM books_layouts_id_seq)::text)+1))::bigint-2)`)
	must(err)
	var older, newer string
	for _, id := range []*string{&older, &newer} {
		must(db.QueryRow(`INSERT INTO books_layouts(organization_id,client_entity,import_id,layout,reading,reason,actor,encoding,delimiter) VALUES($1,'Acme Co',$2::bigint,'{}','{}','Fixture','fixture','utf-8','comma') RETURNING id::text`, org, file).Scan(id))
	}
	if len(newer) == len(older) {
		t.Fatalf("the layouts straddle a power of ten: %s %s", older, newer)
	}
	for _, scope := range []string{file, ""} {
		if l, err := latestLayout(t.Context(), db, org, "Acme Co", scope); err != nil || l == nil || l.ID != newer {
			t.Fatalf("the latest layout (%q): %v %v, want %s", scope, l, err, newer)
		}
	}
}
