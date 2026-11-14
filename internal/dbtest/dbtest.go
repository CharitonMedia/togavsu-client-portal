// Package dbtest gives tests the module's schema in Postgres, set up the way the core
// provisions a module (internal/modules/schema.go in the core): a stand-in core schema
// holding bare record ids, a module login role that owns mod_client_portal, has its
// search_path set to it, USAGE on core and REFERENCES (id) on the core records, and the
// module's migrations applied as that role.
//
// It uses only the togavsu_client_portal database, which it creates beside the database
// in the superuser URL the test passes (the test reads TOGAVSU_TEST_DATABASE_URL), and
// drops with the module role when the test ends.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"testing"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Database is the only database the tests create and use.
const Database = "togavsu_client_portal"

// Schema is the module's schema.
const Schema = "mod_client_portal"

// Role is the module's database login in tests.
const Role = "togavsu_client_portal_mod"

// CoreTables are the core records the module's tables point to.
var CoreTables = []string{"contacts", "files", "matters", "users"}

// DB is the test database.
type DB struct {
	// Admin reaches every schema (the fake core mirrors record ids through it).
	Admin *sql.DB
	// Module is the module's own login, as the module runs in production.
	Module *sql.DB
}

func open(t testing.TB, u url.URL) *sql.DB {
	d, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// New resets the core stand-in and the module schema and applies the migrations, on
// the server that serverURL, a Postgres superuser URL, names.
func New(t testing.TB, serverURL string) *DB {
	t.Helper()
	ctx := context.Background()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	server := open(t, *u)
	// Registered before the pools below, so it runs after they are closed.
	t.Cleanup(func() {
		for _, st := range []string{`DROP DATABASE IF EXISTS ` + Database + ` WITH (FORCE)`, `DROP ROLE IF EXISTS ` + quote(Role)} {
			if _, err := server.ExecContext(ctx, st); err != nil {
				t.Errorf("%s: %v", st, err)
			}
		}
	})
	var exists bool
	if err := server.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, Database).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := server.ExecContext(ctx, `CREATE DATABASE `+Database); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatal(err)
		}
	}
	du := *u
	du.Path = "/" + Database
	admin := open(t, du)

	var b [16]byte
	_, _ = rand.Read(b[:])
	password := hex.EncodeToString(b[:])
	var roleExists bool
	if err := admin.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, Role).Scan(&roleExists); err != nil {
		t.Fatal(err)
	}
	stmts := []string{`DROP SCHEMA IF EXISTS ` + Schema + ` CASCADE`, `DROP SCHEMA IF EXISTS core CASCADE`, `CREATE SCHEMA core`}
	for _, tbl := range CoreTables {
		stmts = append(stmts, `CREATE TABLE core.`+tbl+` (id TEXT PRIMARY KEY)`)
	}
	if roleExists {
		stmts = append(stmts, `ALTER ROLE `+quote(Role)+` LOGIN PASSWORD '`+password+`'`)
	} else {
		stmts = append(stmts, `CREATE ROLE `+quote(Role)+` LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '`+password+`'`)
	}
	stmts = append(stmts,
		`GRANT `+quote(Role)+` TO CURRENT_USER`,
		`CREATE SCHEMA `+Schema+` AUTHORIZATION `+quote(Role),
		`REVOKE ALL ON SCHEMA `+Schema+` FROM PUBLIC`,
		`ALTER ROLE `+quote(Role)+` SET search_path TO `+Schema,
		`GRANT USAGE ON SCHEMA core TO `+quote(Role),
		`GRANT CONNECT ON DATABASE `+Database+` TO `+quote(Role),
	)
	for _, tbl := range CoreTables {
		stmts = append(stmts, `GRANT REFERENCES (id) ON core.`+tbl+` TO `+quote(Role))
	}
	for _, st := range stmts {
		if _, err := admin.ExecContext(ctx, st); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	if err := Migrate(ctx, admin); err != nil {
		t.Fatal(err)
	}
	mu := du
	mu.User = url.UserPassword(Role, password)
	return &DB{Admin: admin, Module: open(t, mu)}
}

// Migrate applies the packaged migrations in order, each in its own transaction, as
// the module's role with search_path set to its schema, as the core does.
func Migrate(ctx context.Context, admin *sql.DB) error {
	files, err := clientportal.Files()
	if err != nil {
		return err
	}
	var names []string
	for name := range files {
		if strings.HasPrefix(name, "migrations/") && strings.HasSuffix(name, ".sql") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		tx, err := admin.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, st := range []string{`SET LOCAL ROLE ` + quote(Role), `SET LOCAL search_path TO ` + Schema, string(files[name])} {
			if _, err := tx.ExecContext(ctx, st); err != nil {
				_ = tx.Rollback()
				return &fs.PathError{Op: "migrate", Path: name, Err: err}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
