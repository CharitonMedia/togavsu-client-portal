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
package.go                  embedded package files and the packaging seam (Build)
package/togavsu-module.yaml the manifest
package/migrations/         0001_messages.sql, 0002_portal_state.sql
internal/portal/            the server: pages, JSON endpoints, templates, static files
internal/coreapi/           the core API client
internal/fakecore/          an in-memory fake core for tests
internal/dbtest/            test database setup, as the core provisions a module
```

## Build, test and package

```sh
go vet ./...
TOGAVSU_TEST_DATABASE_URL=postgres://postgres:pg@127.0.0.1:5432/postgres?sslmode=disable go test ./...
docker build -t togavsu-client-portal .
```

The database tests create and use only a database named `togavsu_client_portal` on the
server the URL names, plus a login role `togavsu_client_portal_mod`. Without the variable
they skip, except under CI, where they fail. They run the module against
`internal/fakecore`, which serves the core API operations the module calls with the
core's access rules, including the module API update items it depends on. Every
acceptance criterion MOD-POR-01 to 35 has a subtest named after it.

`client-portal package -key signing.key -out client-portal.signed.json` is in place but
stops with a message for now: the core's packaging and signing code is internal to the
core and can't be imported from another Go module, and a copy here would drift from
what the core verifies. `Build` takes a `Packager`; the core's public signing and
packaging tool will provide one. Once a package is signed, the admin adds the printed
public key as a trusted signing key, installs the package on the Modules tab and
approves its permissions. Set the manifest's image digest to the built image's digest
before signing.

## Core requirements

The manifest and the server use items of the core's module API update
(docs/modules/CORE-CHANGES.md in the core repository): CORE-01 service calls, CORE-03
module notifications, CORE-04 module mail, CORE-05 module settings, CORE-08 sidebar
badges, CORE-09 context refresh, CORE-10 client tabs and `client_home`, CORE-11 to
CORE-16 client reads and writes, CORE-18 firm profile, CORE-19 user directory and
CORE-21 matter includes. The module installs on a core that has them.
