// Package clientportal is the Togavsu Client Portal module: the client's own portal
// screens, the staff Messages inbox and the Portal access panel. This file holds the
// package contents (manifest, migrations and help) and turns them into the core's
// signed package file through the public module kit (pkg/modulekit), which runs the
// core's own packaging, signing and install checks. The server is in internal/portal.
package clientportal

import (
	"crypto/ed25519"
	"embed"
	"errors"
	"io/fs"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// ID is the module id from the manifest.
const ID = "client-portal"

// ManifestFile is the manifest's name inside a package.
const ManifestFile = modulekit.ManifestFile

//go:embed package
var packageFS embed.FS

// Files returns the package files (manifest and migrations) by package path, read
// the way the kit reads a package directory on disk.
func Files() (map[string][]byte, error) {
	sub, err := fs.Sub(packageFS, "package")
	if err != nil {
		return nil, err
	}
	files, err := modulekit.Files(sub)
	if err != nil {
		return nil, err
	}
	if _, ok := files[ManifestFile]; !ok {
		return nil, errors.New("client-portal: the package has no " + ManifestFile)
	}
	return files, nil
}

// edited returns the package files with edit applied to a copy. Tests use edit to
// make tampered and changed variants.
func edited(edit func(map[string][]byte)) (map[string][]byte, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	if edit != nil {
		edit(files)
	}
	return files, nil
}

// Archive returns the installable package (the gzip tar the core installs) built by
// the kit from the package files. edit, when set, changes a copy of the files first.
func Archive(edit func(map[string][]byte)) ([]byte, error) {
	files, err := edited(edit)
	if err != nil {
		return nil, err
	}
	return modulekit.Archive(files)
}

// Check runs the core's install check over the package files, as the kit's Check
// does. edit, when set, changes a copy of the files first.
func Check(edit func(map[string][]byte)) error {
	files, err := edited(edit)
	if err != nil {
		return err
	}
	_, _, err = modulekit.Check(files)
	return err
}

// Build archives the package files and signs the archive with key, returning the
// signed package file the admin installs (modulekit.Build). It does not run Check, so
// a test can sign a package the core must refuse; the package command checks first.
func Build(key ed25519.PrivateKey, edit func(map[string][]byte)) ([]byte, error) {
	files, err := edited(edit)
	if err != nil {
		return nil, err
	}
	return modulekit.Build(key, files)
}

// SignFile signs an installable package built by Archive (modulekit.SignFile).
func SignFile(key ed25519.PrivateKey, pkg []byte) ([]byte, error) {
	return modulekit.SignFile(key, pkg)
}
