// Package clientportal is the Togavsu Client Portal module: the client's own portal
// screens, the staff Messages inbox and the Portal access panel. This file holds the
// package contents (manifest, migrations, help and the pending list) and turns them
// into the core's signed package file through the public module kit (pkg/modulekit),
// which runs the core's own packaging, signing and install checks and holds back the
// parts that wait on a core change. The server is in internal/portal.
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

// Files returns the package files (manifest, migrations and pending.yaml) by package
// path, read the way the kit reads a package directory on disk. They are the full
// package; Installable is what the pinned core installs.
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

// Pending reads the package's pending list (package/pending.yaml): the parts that wait
// on a core change, each with its CORE item.
func Pending() ([]modulekit.Pending, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	return modulekit.ParsePending(files[modulekit.PendingFile])
}

// Installable returns what the pinned core accepts (modulekit.Installable): the files
// without pending.yaml and without each pending part the core refuses, the parts it
// held back, the pending list and the entries that held nothing back. A refusal no
// pending entry covers is an error. edit, when set, changes a copy of the full files
// first.
func Installable(edit func(map[string][]byte)) (modulekit.InstallablePackage, error) {
	files, err := edited(edit)
	if err != nil {
		return modulekit.InstallablePackage{}, err
	}
	return modulekit.Installable(files)
}

// Archive returns the installable package (the gzip tar the core installs) built by
// the kit from Installable's files. edit, when set, changes a copy of the full files
// first.
func Archive(edit func(map[string][]byte)) ([]byte, error) {
	ip, err := Installable(edit)
	if err != nil {
		return nil, err
	}
	return modulekit.Archive(ip.Files)
}

// Build checks the package, holds back the pending parts the pinned core refuses and
// signs the rest with key (modulekit.BuildInstallable), returning the signed package
// file the admin installs and Installable's result (the parts it held back and the
// unused entries). edit, when set, changes a copy of the full files first.
func Build(key ed25519.PrivateKey, edit func(map[string][]byte)) ([]byte, modulekit.InstallablePackage, error) {
	files, err := edited(edit)
	if err != nil {
		return nil, modulekit.InstallablePackage{}, err
	}
	return modulekit.BuildInstallable(key, files)
}

// SignFile signs package bytes without checking them (modulekit.SignFile). Tests use
// it to sign a package the core must refuse.
func SignFile(key ed25519.PrivateKey, pkg []byte) ([]byte, error) {
	return modulekit.SignFile(key, pkg)
}
