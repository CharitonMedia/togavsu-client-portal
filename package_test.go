package clientportal_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
	"github.com/CharitonMedia/togavsu-client-portal/internal/coremain"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, key
}

// skipIfPending skips when the core refused the real manifest for a field that waits
// on a module-core item, and fails on any other refusal.
func skipIfPending(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if item := coremain.Item(err); item != "" {
		t.Skipf("needs %s: %v", item, err)
	}
	t.Fatal(err)
}

func TestPackageFiles(t *testing.T) {
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"migrations/0001_messages.sql", "migrations/0002_portal_state.sql", "togavsu-module.yaml"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("package files = %v, want %v", names, want)
	}
	m := string(files[clientportal.ManifestFile])
	for _, line := range []string{"id: client-portal", "author: Chariton Media", "price: Free", "client_home: home"} {
		if !strings.Contains(m, line) {
			t.Errorf("manifest lacks %q", line)
		}
	}
	// The embedded files are the package directory as the kit reads it from disk, so
	// the package command and `togavsu-module check package` see the same bytes.
	disk, err := modulekit.ReadDir("package")
	if err != nil {
		t.Fatal(err)
	}
	if len(disk) != len(files) {
		t.Fatalf("package dir has %d files, embedded %d", len(disk), len(files))
	}
	for name, b := range disk {
		if !bytes.Equal(b, files[name]) {
			t.Errorf("%s differs between the package dir and the embedded files", name)
		}
	}
}

// The real package dir passes the core's install check once CORE-03, CORE-08 and
// CORE-10 land; until then the check names the field it refuses and this skips.
func TestPackageDirPassesCoreCheck(t *testing.T) {
	files, err := modulekit.ReadDir("package")
	if err != nil {
		t.Fatal(err)
	}
	_, ms, err := modulekit.Check(files)
	skipIfPending(t, err)
	if len(ms) != 2 {
		t.Fatalf("migrations = %d, want 2", len(ms))
	}
	raw, err := clientportal.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := modulekit.CheckPackage(raw); err != nil {
		t.Fatal(err)
	}
}

// The package without the CORE-gated manifest fields passes the core's install check
// today: manifest, migrations and everything else in it.
func TestCoreMainVariantPassesCoreCheck(t *testing.T) {
	if err := clientportal.Check(coremain.Edit); err != nil {
		t.Fatal(err)
	}
	raw, err := clientportal.Archive(coremain.Edit)
	if err != nil {
		t.Fatal(err)
	}
	pkg, ms, err := modulekit.CheckPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.ID != clientportal.ID || len(ms) != 2 {
		t.Fatalf("package = %s with %d migrations", pkg.Manifest.ID, len(ms))
	}
	if err := modulekit.CheckMigrations(pkg.Manifest, ms); err != nil {
		t.Fatal(err)
	}
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modulekit.ParseManifest(coremain.Manifest(files[clientportal.ManifestFile])); err != nil {
		t.Fatal(err)
	}
}

func verify(t *testing.T, signed []byte, pub ed25519.PublicKey) (modulekit.Verified, []modulekit.Migration, error) {
	t.Helper()
	return modulekit.VerifyFile(signed, []modulekit.TrustedKey{modulekit.Trust(pub)})
}

func TestBuildSignsAPackageTheCoreVerifies(t *testing.T) {
	pub, key := testKey(t)
	signed, err := clientportal.Build(key, coremain.Edit)
	if err != nil {
		t.Fatal(err)
	}
	v, ms, err := verify(t, signed, pub)
	if err != nil {
		t.Fatal(err)
	}
	if v.ModuleID != clientportal.ID || v.Version != "1.0.0" || v.Signer.KeyID != modulekit.KeyID(pub) || len(ms) != 2 {
		t.Fatalf("verified %s %s by %s with %d migrations", v.ModuleID, v.Version, v.Signer.KeyID, len(ms))
	}

	// Archive then SignFile is the same signed package.
	raw, err := clientportal.Archive(coremain.Edit)
	if err != nil {
		t.Fatal(err)
	}
	again, err := clientportal.SignFile(key, raw)
	if err != nil {
		t.Fatal(err)
	}
	if v2, _, err := verify(t, again, pub); err != nil || v2.Digest != v.Digest {
		t.Fatalf("Archive+SignFile verified %v with digest %s, Build %s", err, v2.Digest, v.Digest)
	}
}

func TestBuildFullPackageVerifies(t *testing.T) {
	pub, key := testKey(t)
	signed, err := clientportal.Build(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = verify(t, signed, pub)
	skipIfPending(t, err)
}

func TestSignedPackageRefusesTamperAndStrangers(t *testing.T) {
	pub, key := testKey(t)
	signed, err := clientportal.Build(key, coremain.Edit)
	if err != nil {
		t.Fatal(err)
	}
	stranger, _ := testKey(t)
	if _, _, err := verify(t, signed, stranger); err == nil {
		t.Fatal("a package verified against a key that didn't sign it")
	}

	var file map[string]any
	if err := json.Unmarshal(signed, &file); err != nil {
		t.Fatal(err)
	}
	pkg, err := base64.StdEncoding.DecodeString(file["package_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	pkg[len(pkg)/2] ^= 1
	file["package_base64"] = base64.StdEncoding.EncodeToString(pkg)
	flipped, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = verify(t, flipped, pub)
	if err == nil {
		t.Fatal("a package with a flipped byte verified")
	}
	if code := modulekit.Code(err); code != "tampered_module" {
		t.Fatalf("flipped byte refused with %q (%v), want tampered_module", code, err)
	}
}

func TestBuildEditChangesOnlyTheCopy(t *testing.T) {
	_, key := testKey(t)
	if _, err := clientportal.Build(key, func(f map[string][]byte) { f["migrations/0001_messages.sql"] = []byte("-- changed") }); err != nil {
		t.Fatal(err)
	}
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	if string(files["migrations/0001_messages.sql"]) == "-- changed" {
		t.Fatal("edit leaked into the embedded files")
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
