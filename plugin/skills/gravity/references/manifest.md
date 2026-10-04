# `.gravity.yaml` (manifest v2)

Code facts live in the repository; editorial intent (instructions, review) lives in the app. Only
`version: 2` is required. `gravity validate` checks the file against the embedded JSON Schema plus
semantic rules; fix every `manifest_invalid` issue at the `path` it names.

Never put a token in this file (`token:` anywhere is a manifest error).

## Top level

```yaml
version: 2                       # required
product: polaris                 # product slug; omitted = the product the repo is registered under
apiUrl: https://api.gravitydocs.io   # rarely needed; GRAVITY_API_URL / --api-url override it
appPasses: allow                 # allow: app passes run too | ignore: only passes declared here
code:
  openapi: [api/openapi.yaml]    # OpenAPI 3 / Swagger 2 documents
  entrypoints: [src/routes]      # where user-visible surface starts
  include: [src/**]              # default scope for passes without scope.paths
  exclude: ["**/*.test.ts"]
  units: { kind: auto, role: implements }   # role: implements | declares | documents (or a list)
docs:
  include: [docs/**]             # human docs passes may READ as context (reading is not importing)
structure: { ... }               # declared docs structure, see structure.md
passes: [ ... ]                  # passes-as-code, max 32, unique names
```

Globs are repository-relative doublestar patterns; absolute paths and `..` are rejected.

## A pass

```yaml
- name: admin-docs               # slug, unique
  kind: verbatim                 # guides | reference | verbatim | changelog | nucleus | check | capture
  title: Admin docs              # optional display name
  template: verbatim-docs        # optional app template; kind must match it
  target: polaris/developers/admin   # <site>/<space>[/<collection>...]; required for guides, reference,
                                     # verbatim, changelog, capture; check/nucleus accept a bare <site>
  triggers: [push]               # pr | push | release | schedule | manual
  branches: [main]               # push/schedule filter; empty = default branch
  scope: { paths: [packages/admin/**], exclude: [], units: [api] }
  audiences: [developers]        # public | users | developers | org audience keys (max 8)
  instructions: |                # Markdown layered after org voice and site/space/collection briefs
    Write for operators. Never describe internal endpoints.
  publish: propose               # propose | auto-if-trusted
  enabled: true
  options: { ... }               # per kind, below
```

## Options per kind

- **verbatim** (`options` required):
  ```yaml
  options:
    files:                       # 1-32 entries
      - include: packages/*/README.md
        exclude: [packages/legacy/**]
        collection: packages     # collection path under the target
        stripPrefix: packages    # folder root removed before nesting collections
      - include: docs/install.md # literal include may pin slug/title
        slug: installation
        title: Installation
    adopt: false                 # take over an existing unlocked page with the same slug
    allowEmpty: false            # import empty files (otherwise: empty_source warning, skipped)
    indexFiles: [index.md, README.md, _index.md]   # become their folder's overview page
    languages: [fr, es]          # translation languages requested for imported pages
  ```
  Folder structure below the include root nests collections (max depth 5). Front matter keys `title`,
  `slug`, `description`, `position`, `hidden`, `lang`, `audiences` are honoured. A leading H1 equal to the
  page title is dropped (no duplicate title). Package-name titles (`@scope/polaris-admin`) become
  `Polaris Admin`; slugs never come from package names.
- **reference**: `sources: [{path, page, title, collection}]`, `pageStrategy: single | per-tag |
  per-operation` (default per-tag), `prose: false` (true = AI writes intro/usage prose), `languages`.
- **guides**: `maxPages` (25), `surveyCommits` (50), `createPages` (true), `deprecate` (true),
  `prPreview: off | impact | full`, `languages`.
- **changelog**: `audience: customer | internal`, `unreleased` (true), `unreleasedSlug`, `slugPattern`
  (`{tag}`; also `{major}` `{minor}` `{patch}` `{date}`), `source: commits | changelog-file`,
  `changelogFile`, `tagPattern` (`v*`), `releaseQueued`.
- **nucleus**: `namespace` (default `product:<slug>`), `kinds`, `maxAtoms` (40).
- **check**: `failOn: [drift, coverage, claims, verbatim]`, `coverageMin`, `require`, `claims` (true),
  `annotate` (true).
- **capture**: `connectionId`, `audience`, `maxPages` (10).

## Where passes run from

A real run (`gravity run`) uses the local `.gravity.yaml` from any branch; its changes become a change
request. Watermarks (what a pass has already processed) only advance on the repository's authoritative
(default) branch, so branch runs never skip work for CI. The file becomes the stored configuration when it
reaches the default branch.
