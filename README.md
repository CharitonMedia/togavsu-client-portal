# Togavsu Client Portal

The Client Portal module gives a firm's clients their own screens in Togavsu, and gives
staff a Messages inbox and a Portal access panel. Author Chariton Media, listed free,
license "Same as Togavsu" (see LICENSE). The full specification is in
[docs/SPEC.md](docs/SPEC.md), copied from the core repository.

## What it does

- **Client pages** (tabs with `placement: client`, home tab `home`): Dashboard (new
  documents, unread messages, balance due, trust balance, an active replenishment
  request, the next appointment), Documents (shared files, view, download, Download all
  as a zip, uploads), Invoices (sent, viewed and paid only), Messages and Account
  (contact details with history kept by the core, trust activity, a Change password
  link). A client with several matters picks one before any upload, new message, Pay or
  deposit, and the choice is remembered.
- **Staff**: the Messages inbox and sidebar badge, the matter screen's Messages tab,
  the dashboard widget, a quick-create New message, message search, and the Portal
  access matter item (send portal access, temporary password, turn off and on).
- **Messages**: threads scoped to one matter and one client contact; attachments that
  share the file with the client and write a "sent to client" timeline row; read
  receipts; archive and close; staff can retract their own messages. A staff message
  emails the client a portal link (never the body); a client message notifies the
  matter team.
- **Uploads**: content-sniffed types (PDF, Word, Excel, text, images, HEIC), at most
  `upload_max_file_mb` per file, 100 files or 2 GB per matter in 24 hours, 90 uploads
  per client in 5 minutes. A refused upload leaves no file record.
- **Payments and signing** come from the lawpay-docuseal module's published views. When
  that module is missing or its scopes are declined, no Pay, deposit or Sign control
  shows and every page still works.
- **Agents**: every page action has a JSON endpoint under `/api/`, listed at
  `/api/openapi.json`.

The module keeps no user, password or session. Every request carries the core's
context token (the frame's `?context=`, a `context` form field, or the
`X-Togavsu-Context-Token` header), which the module verifies with the core; every core
call goes out with the module's token plus that context token, so the core's walls,
roles and attorney-only rules decide what each viewer sees. Its own tables live in
`mod_client_portal` and reach core records only through `ON DELETE RESTRICT` keys.

## Layout

```
cmd/client-portal/          serve and package subcommands
package.go                  embedded package files; Pending, Installable, Archive, Build, SignFile (pkg/modulekit)
package/togavsu-module.yaml the manifest
package/migrations/         0001_messages.sql, 0002_portal_state.sql
package/pending.yaml        the parts that wait on a core change, each with its CORE item; never ships
internal/portal/            the server: pages, JSON endpoints, templates, static files
internal/coreapi/           the core API client
internal/acceptance/        the acceptance suite on the real core (pkg/modulekit/coretest)
internal/fakecore/          an in-memory fake core for the fast suite
internal/dbtest/            test database setup for the fake-core suite
```

The module requires the core module (`github.com/CharitonMedia/togavsu`) for the public
module kit, and no dependency the core's go.mod doesn't pin.

## Build, test and package

```sh
gofmt -l .
go vet ./...
TOGAVSU_TEST_DATABASE_URL=postgres://postgres:pg@127.0.0.1:5432/postgres?sslmode=disable go test -race ./...
docker build -t togavsu-client-portal .
```

`TOGAVSU_TEST_DATABASE_URL` is a Postgres superuser URL. Without it the database tests
skip, except under CI, where they fail. Two suites use it:

- `internal/acceptance` installs the signed package on the real composed core
  (`pkg/modulekit/coretest`: trusted key, install, approval of every permission), serves
  the module in process with the token and database login the core handed over, and
  drives MOD-POR-01 to 35 through the core's API, frames, access rules and database.
  coretest makes and removes its own database and roles per test. A criterion that needs
  a module-core item the core main lacks is written in full and skips naming the item
  (`needs CORE-NN`), so the suite turns green criterion by criterion as module-core
  lands. The suite installs the package the package command builds: the pending parts
  the pinned core refuses held back (see Packaging and signing). Its lawpay-docuseal
  fixture has its own `pending.yaml` for its two client tabs (CORE-10).
- `internal/portal` runs every criterion against `internal/fakecore`, which serves the
  core API operations the module calls, including the module-core items, with the
  core's access rules. It creates a database named `togavsu_client_portal` and a role
  `togavsu_client_portal_mod`, and drops both when each test ends.

The root package's tests run the core's checks on the package and the source; see
Packaging and signing for the package tests. `TestSourcePassesCoreSettingsCheck` runs
`modulekit.CheckSource`, the core's settings source check (no environment reads, no
numeric configuration literals outside settings). Fixed numbers in the source are
spelled as arithmetic, as the core's matter-clock does, so the check can tell them from
tunables.

## Packaging and signing

The package directory is the full package: the manifest and migrations as the SPEC
defines them, including the fields and scopes that wait on core items, plus
`package/pending.yaml`. Each pending entry names one part (a manifest field path or a
scope), the CORE item it waits on and one line on what it does. Today it holds back:

| Part | CORE item |
| --- | --- |
| `client_home` | CORE-10 |
| the five client tabs (`tabs[2]`, Dashboard to Account) | CORE-10 |
| `tabs[*].params` | CORE-10 |
| `sidebar[*].section`, `icon`, `order`, `badge` | CORE-08 |
| `notifications` | CORE-03 |
| scope `users:directory` | CORE-19 |
| scope `firm:read` | CORE-18 |
| scope `mail:send` | CORE-04 |
| scopes `client_access:read`, `client_access:write` | CORE-14 |

No migration waits on a core item. The client tabs are the last five in `tabs`, so the
one entry `tabs[2]` holds back each in turn: the kit names list items by index, and
each removal moves the next client tab to index 2. Keep them last.

```sh
go run ./cmd/client-portal package -key signing.key -out client-portal.signed.json
# or, with the core's command:
go run github.com/CharitonMedia/togavsu/cmd/togavsu-module package -installable -dir package -key signing.key -out client-portal.signed.json
```

The key file holds a base64 Ed25519 seed; keep the release key out of the repository.
`package` builds with `modulekit.BuildInstallable`: it holds back each pending part the
pinned core refuses, keeps each one the core accepts, prints one stderr line per held
part with its CORE item (and one per entry that held nothing back), refuses to sign
when the core refuses anything `pending.yaml` doesn't cover, writes the signed package
file and prints the public key for the admin to add as a trusted signing key. When a
CORE item lands its parts ship without any edit; delete its entries then.
`go run github.com/CharitonMedia/togavsu/cmd/togavsu-module check -installable package`
runs the same check without signing, as CI does. The manifest's image digest is a
placeholder that the release workflow pins.

`TestBuildSignsWhatThePinnedCoreInstalls` builds and signs with a key made at test time,
verifies the signed file and runs `modulekit.CheckAll` and `modulekit.Check` on the
files it carries, which must show no refusals and no `pending.yaml`. It fails when Build
errors with that key, when an entry holds nothing back (the core accepts that part now:
delete the entry) and, through Build, when the core refuses a part no entry covers.
`TestFullPackageRefusedOnlyForPendingParts` checks every refusal of the full package is
answered by a held part, and `TestBuildRefusesWhatPendingDoesNotName` that an unlisted
refusal stops the build.

## Core requirements

The manifest and the server use items of the core's module API update
(docs/modules/CORE-CHANGES.md in the core repository): CORE-01 service calls, CORE-03
module notifications, CORE-04 module mail, CORE-05 system mail routing, CORE-08 sidebar
badges, CORE-09 the frame bridge, CORE-10 client tabs and `client_home`, CORE-11 to
CORE-16 client reads and writes, CORE-18 firm profile and CORE-19 user directory. The
module installs on a core that has them.
