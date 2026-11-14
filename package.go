// Package clientportal is the Togavsu Client Portal module: the client's own portal
// screens, the staff Messages inbox and the Portal access panel. This file holds the
// package contents (manifest, migrations and help) and the seam where they become a
// signed package. The server is in internal/portal.
//
// Packaging and signing belong to the core: the package archive format, the manifest
// check and the Ed25519 signed package file are what the core verifies at install, and
// they live in the core's internal packages, which another Go module can't import. A
// copy here would drift from what the core checks, so the module doesn't carry one.
// Build takes a Packager; until the core publishes a public packaging and signing
// package or CLI, the only Packager here is Unavailable, which refuses.
package clientportal

import (
	"crypto/ed25519"
	"embed"
	"errors"
	"io/fs"
	"strings"
)

// ID is the module id from the manifest.
const ID = "client-portal"

// ManifestFile is the manifest's name inside a package.
const ManifestFile = "togavsu-module.yaml"

//go:embed package
var packageFS embed.FS

// Files returns the package files (manifest and migrations) by package path.
func Files() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(packageFS, "package", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := packageFS.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "package/")] = b
		return nil
	})
	return out, err
}

// Packager turns package files into the core's signed package file, signed with key.
// The core's public packaging and signing tool will implement it.
type Packager interface {
	Package(files map[string][]byte, key ed25519.PrivateKey) ([]byte, error)
}

// ErrNoPackager means no core packaging and signing tool is wired in yet.
var ErrNoPackager = errors.New("client-portal: packaging and signing wait on the core's public signing and packaging tool")

// Unavailable is the stub Packager used until the core's tool exists. It refuses.
type Unavailable struct{}

// Package refuses with ErrNoPackager.
func (Unavailable) Package(map[string][]byte, ed25519.PrivateKey) ([]byte, error) {
	return nil, ErrNoPackager
}

// Build hands the package files to p to package and sign with key. edit, when set,
// changes the files first (tests use it to make tampered and changed variants).
func Build(p Packager, key ed25519.PrivateKey, edit func(map[string][]byte)) ([]byte, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	if _, ok := files[ManifestFile]; !ok {
		return nil, errors.New("client-portal: the package has no " + ManifestFile)
	}
	if edit != nil {
		edit(files)
	}
	if p == nil {
		p = Unavailable{}
	}
	return p.Package(files, key)
}
