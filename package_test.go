package clientportal_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	clientportal "github.com/CharitonMedia/togavsu-client-portal"
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
	want := []string{"migrations/0001_messages.sql", "migrations/0002_portal_state.sql", "pending.yaml", "togavsu-module.yaml"}
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

// signedFiles verifies signed against pub and returns the package files it carries.
func signedFiles(t *testing.T, signed []byte, pub ed25519.PublicKey) (modulekit.Verified, map[string][]byte) {
	t.Helper()
	v, _, err := verify(t, signed, pub)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := modulekit.ReadPackage(v.Package)
	if err != nil {
		t.Fatal(err)
	}
	return v, pkg.Files
}

// The package command's build: the full package with a key made now, every refusal of
// the pinned core covered by a pending entry and every entry holding something back.
// The signed file verifies, and the files it carries pass the core's install check
// with no refusals and no pending list. When a CORE item lands, its entries hold
// nothing back and this fails until they are deleted; a new refusal no entry covers
// fails Build.
func TestBuildSignsWhatThePinnedCoreInstalls(t *testing.T) {
	pub, key := testKey(t)
	signed, held, err := clientportal.Build(key, nil)
	if err != nil {
		t.Fatalf("Build with a valid key: %v", err)
	}
	entries, err := clientportal.Pending()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range modulekit.Unused(entries, held) {
		t.Errorf("pending.yaml: %s (%s) holds nothing back: the pinned core accepts it or the package no longer has it; delete the entry", p.Part(), p.Core)
	}
	for _, h := range held {
		t.Log(h.String())
	}
	v, files := signedFiles(t, signed, pub)
	if v.ModuleID != clientportal.ID || v.Version != "1.0.0" || v.Signer.KeyID != modulekit.KeyID(pub) {
		t.Fatalf("verified %s %s by %s", v.ModuleID, v.Version, v.Signer.KeyID)
	}
	if _, ok := files[modulekit.PendingFile]; ok {
		t.Error("the signed package ships pending.yaml")
	}
	if refs := modulekit.CheckAll(files); len(refs) != 0 {
		t.Fatalf("the signed package's files are refused: %v", refs)
	}
	if _, ms, err := modulekit.Check(files); err != nil || len(ms) != 2 {
		t.Fatalf("Check on the signed files: %d migrations, %v", len(ms), err)
	}
}

// Without its pending list the full package is refused, and only for parts the list
// names: every refusal CheckAll reports is one some held part answers.
func TestFullPackageRefusedOnlyForPendingParts(t *testing.T) {
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	delete(files, modulekit.PendingFile)
	refs := modulekit.CheckAll(files)
	if len(refs) == 0 {
		t.Fatal("the pinned core accepts the full package: empty pending.yaml")
	}
	_, held, err := clientportal.Installable(nil)
	if err != nil {
		t.Fatalf("a refusal no pending entry covers: %v", err)
	}
	answered := map[string]bool{}
	for _, h := range held {
		for _, r := range h.Refusals {
			answered[r.File+" "+r.Path+" "+r.Code] = true
		}
	}
	for _, r := range refs {
		if !answered[r.File+" "+r.Path+" "+r.Code] {
			t.Errorf("refusal held by no pending part: %v", r)
		}
	}
}

// A refusal pending.yaml doesn't name stops the build.
func TestBuildRefusesWhatPendingDoesNotName(t *testing.T) {
	_, key := testKey(t)
	_, _, err := clientportal.Build(key, func(f map[string][]byte) {
		f[clientportal.ManifestFile] = append(f[clientportal.ManifestFile], []byte("unheard_of: true\n")...)
	})
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefuseUnknownField || refs[0].Path != "unheard_of" {
		t.Fatalf("Build with an unlisted field = %v, want one unknown_field refusal at unheard_of", err)
	}
}

func verify(t *testing.T, signed []byte, pub ed25519.PublicKey) (modulekit.Verified, []modulekit.Migration, error) {
	t.Helper()
	return modulekit.VerifyFile(signed, []modulekit.TrustedKey{modulekit.Trust(pub)})
}

func TestBuildSignsAPackageTheCoreVerifies(t *testing.T) {
	pub, key := testKey(t)
	signed, _, err := clientportal.Build(key, nil)
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
	raw, err := clientportal.Archive(nil)
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

func TestSignedPackageRefusesTamperAndStrangers(t *testing.T) {
	pub, key := testKey(t)
	signed, _, err := clientportal.Build(key, nil)
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
	const mark = "\n-- changed\n"
	if _, _, err := clientportal.Build(key, func(f map[string][]byte) {
		f["migrations/0001_messages.sql"] = append(f["migrations/0001_messages.sql"], mark...)
	}); err != nil {
		t.Fatal(err)
	}
	files, err := clientportal.Files()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(string(files["migrations/0001_messages.sql"]), mark) {
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

// TestSourcePassesCoreSettingsCheck runs the core's settings source check (AC-030) over
// the module's Go source: no environment reads and no numeric configuration literals
// outside settings. Test files are not checked.
func TestSourcePassesCoreSettingsCheck(t *testing.T) {
	if err := modulekit.CheckSource("."); err != nil {
		t.Fatal(err)
	}
}
