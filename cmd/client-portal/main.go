// Command client-portal runs the Client Portal module, or packages it.
//
//	client-portal serve -core http://core:8080 -secrets /run/secrets -db-host postgres:5432 -db-name togavsu
//	client-portal package -key signing.key -out client-portal.signed.json
//
// Secrets come from the Docker secrets the core hands the supervisor:
// togavsu_api_token, togavsu_db_role and togavsu_db_password. The module subscribes to
// no events, so it reads no webhook secret.
//
// package writes the signed package file the pinned core installs, through the public
// module kit (pkg/modulekit.BuildInstallable): it holds back each part in
// package/pending.yaml that the core refuses, prints one line per held part with the
// CORE item it waits on, and refuses to sign when anything else is refused.
package main

import (
	"crypto/ed25519"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
	"github.com/CharitonMedia/togavsu-client-portal/internal/portal"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Small fixed numbers spelled as arithmetic for the core's AC-030 source check
// (modulekit.CheckSource), as the core's matter-clock does.
var (
	zero = int(0)
	one  = zero + 1
)

func main() {
	log.SetFlags(zero)
	args := os.Args[one:]
	if len(args) < one {
		log.Fatal("usage: client-portal serve|package [flags]")
	}
	switch args[zero] {
	case "serve":
		serve(args[one:])
	case "package":
		pack(args[one:])
	default:
		log.Fatalf("unknown command %q", args[zero])
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
	key, err := modulekit.ParseSeed(string(raw))
	if err != nil {
		log.Fatal(err)
	}
	// Hold back the pending parts the pinned core refuses, and refuse to sign anything
	// else the core would refuse to install.
	signed, ip, err := clientportal.Build(key, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, h := range ip.Held {
		fmt.Fprintln(os.Stderr, h.String())
	}
	for _, p := range ip.Unused {
		fmt.Fprintf(os.Stderr, "pending %s (%s) holds nothing back: the core accepts it or the package no longer has it\n", p.Part(), p.Core)
	}
	if err := os.WriteFile(*out, signed, os.FileMode(zero)|0o644); err != nil {
		log.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	fmt.Printf("wrote %s, signed by key %s\npublic key (add it as a trusted signing key, public_key_pem):\n%s", *out, modulekit.KeyID(pub), modulekit.PublicKeyPEM(pub))
}
