package clientportal_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sort"
	"strings"
	"testing"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
)

// recorder is a stand-in Packager that keeps what Build hands it. The core's real
// packaging and signing tool replaces it when the core publishes one.
type recorder struct {
	files map[string][]byte
	key   ed25519.PrivateKey
}

func (r *recorder) Package(files map[string][]byte, key ed25519.PrivateKey) ([]byte, error) {
	r.files, r.key = files, key
	return []byte("package"), nil
}

func testKey(t *testing.T) ed25519.PrivateKey {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestBuildHandsOverManifestAndMigrations(t *testing.T) {
	r := &recorder{}
	key := testKey(t)
	out, err := clientportal.Build(r, key, nil)
	if err != nil || string(out) != "package" {
		t.Fatalf("Build = %q, %v", out, err)
	}
	if !bytes.Equal(r.key, key) {
		t.Fatal("Build did not pass the signing key through")
	}
	var names []string
	for name := range r.files {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"migrations/0001_messages.sql", "migrations/0002_portal_state.sql", "togavsu-module.yaml"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("package files = %v, want %v", names, want)
	}
	m := string(r.files[clientportal.ManifestFile])
	for _, line := range []string{"id: client-portal", "author: Chariton Media", "price: Free", "client_home: home"} {
		if !strings.Contains(m, line) {
			t.Errorf("manifest lacks %q", line)
		}
	}
}

func TestBuildEditChangesOnlyTheCopy(t *testing.T) {
	r := &recorder{}
	if _, err := clientportal.Build(r, testKey(t), func(f map[string][]byte) { f["migrations/0001_messages.sql"] = []byte("-- changed") }); err != nil {
		t.Fatal(err)
	}
	if string(r.files["migrations/0001_messages.sql"]) != "-- changed" {
		t.Fatal("edit was not applied")
	}
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	if string(files["migrations/0001_messages.sql"]) == "-- changed" {
		t.Fatal("edit leaked into the embedded files")
	}
}

func TestBuildWithoutCorePackagerRefuses(t *testing.T) {
	if _, err := clientportal.Build(clientportal.Unavailable{}, testKey(t), nil); !errors.Is(err, clientportal.ErrNoPackager) {
		t.Fatalf("Build with the stub = %v, want ErrNoPackager", err)
	}
	if _, err := clientportal.Build(nil, testKey(t), nil); !errors.Is(err, clientportal.ErrNoPackager) {
		t.Fatalf("Build with no packager = %v, want ErrNoPackager", err)
	}
}

// Every migration is plain SQL that names no schema of its own and reaches the core
// only through REFERENCES core.<record> (id) ON DELETE RESTRICT, as the core's
// migration check requires.
func TestMigrationsStayInTheirSchema(t *testing.T) {
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		if !strings.HasPrefix(name, "migrations/") {
			continue
		}
		sql := strings.ToLower(string(b))
		for _, bad := range []string{"mod_client_portal.", "create schema", "set search_path", "grant ", "drop table core", "on delete cascade"} {
			if strings.Contains(sql, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		for _, line := range strings.Split(sql, "\n") {
			if i := strings.Index(line, "core."); i >= 0 {
				if !strings.Contains(line, "references core.") || !strings.Contains(line, "(id) on delete restrict") {
					t.Errorf("%s: core reached outside a RESTRICT key: %s", name, strings.TrimSpace(line))
				}
			}
		}
	}
}
