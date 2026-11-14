// Command client-portal runs the Client Portal module, or packages it.
//
//	client-portal serve -core http://core:8080 -secrets /run/secrets -db-host postgres:5432 -db-name togavsu
//	client-portal package -key signing.key -out client-portal.signed.json
//
// Secrets come from the Docker secrets the core hands the supervisor:
// togavsu_api_token, togavsu_db_role and togavsu_db_password. The module subscribes to
// no events, so it reads no webhook secret.
//
// package waits on the core's public signing and packaging tool; until then it stops
// with a message instead of writing a package the core might not accept.
package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
	"github.com/CharitonMedia/togavsu-client-portal/internal/portal"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal("usage: client-portal serve|package [flags]")
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "package":
		pack(os.Args[2:])
	default:
		log.Fatalf("unknown command %q", os.Args[1])
	}
}

func secret(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		log.Fatalf("secret %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "address to listen on")
	core := fs.String("core", "http://core:8080", "core base URL inside the stack")
	dir := fs.String("secrets", "/run/secrets", "directory of Docker secrets")
	host := fs.String("db-host", "postgres:5432", "Postgres host:port")
	name := fs.String("db-name", "togavsu", "database name")
	header := fs.String("read-header-timeout", "10s", "HTTP read-header timeout")
	_ = fs.Parse(args)
	u := url.URL{Scheme: "postgres", User: url.UserPassword(secret(*dir, "togavsu_db_role"), secret(*dir, "togavsu_db_password")), Host: *host, Path: "/" + *name}
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		log.Fatal(err)
	}
	timeout, err := time.ParseDuration(*header)
	if err != nil {
		log.Fatal(err)
	}
	srv := portal.New(portal.Config{Core: *core, Token: secret(*dir, "togavsu_api_token"), DB: db})
	log.Printf("client-portal listening on %s", *listen)
	log.Fatal((&http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: timeout}).ListenAndServe())
}

func pack(args []string) {
	fs := flag.NewFlagSet("package", flag.ExitOnError)
	keyFile := fs.String("key", "", "file holding a base64 ed25519 seed")
	out := fs.String("out", "client-portal.signed.json", "signed package file to write")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		log.Fatal("the key file must hold a base64 ed25519 seed")
	}
	pkg, err := clientportal.Build(clientportal.Unavailable{}, ed25519.NewKeyFromSeed(seed), nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, pkg, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", *out)
}
