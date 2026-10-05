# Docs structure (`structure:`)

Declare the docs skeleton in `.gravity.yaml` and let the CLI create it deterministically. This replaces
building sites, spaces, collections and pages one MCP call at a time (slow, random slug suffixes, stubs
that later block imports).

## Spec

```yaml
structure:
  site: { slug: polaris, name: Polaris }        # existing or created
  spaces:
    - slug: developers
      name: Developers
      type: api-reference        # a space type: api-reference, product-docs, release-notes, handbook, knowledge-base, ...
      visibility: public         # public | private
      parent: null               # parent space slug for a subspace
      collections:
        - slug: admin
          title: Admin
          description: Running the admin service
          collections: []        # nested, max depth 5
          pages:
            - slug: installation
              title: Installation
              description: Install and configure the admin service
              source: packages/admin/README.md   # owned by a verbatim pass: never created as a stub
```

Rules:

- Everything is addressed by slug path (`site/space/collection/.../page`). Slugs are lowercase
  `a-z0-9-`. Renaming a slug means a new item; the old one shows up as `extra`.
- A page with `source:` belongs to a verbatim pass. That pass must map the file (same slug and
  collection), else validate reports `structure_source_unmapped`. Apply does not create it
  (`deferred_to_import`); the import creates it, already locked to the repository.
- Pages without `source:` are created empty by apply and attributed to the repository, so a later
  verbatim import from this repository takes them over without `adopt`.
- Pass targets should point inside the declared structure (`polaris/developers/admin`).

## Commands

```bash
gravity structure plan [--repo <path>]... [--site <slug>] [--write]
```

Deterministic draft from the repository layout (monorepo packages with READMEs, `docs/` folders, OpenAPI
documents, changelog, runbooks) merged with the current site tree. Prints the YAML and a diff:
`+` create, `=` exists, `!` extra (on the server, not declared). `--repo` adds sibling repositories of the
same product. `--write` merges the draft into `.gravity.yaml` (other keys untouched).

```bash
gravity structure apply --dry-run --json
gravity structure apply
```

Idempotent: re-applying changes nothing. Never deletes: server items not declared are reported as `extra`
for the user to handle in the app. Result items have `kind`, `path`, `title`, `reason`, grouped as
`created`, `updated`, `unchanged`, `extra`, `conflicts`, `deferred`.

- `conflicts`: something at that path is incompatible (wrong kind, locked by another repository, no
  permission). Read `reason`; fix the yaml or ask an admin.
- Permissions: a user needs docs.spaces.manage (and site create rights for a new site). A repository
  token may only apply inside spaces the repository holds a grant for (setup grants them).

```bash
gravity structure show [--site <slug>] --json
```

The live tree including pages (`slug`, `title`, `status`, `lockedByRepo`, `sourceRepo`). Use it to check
what exists before editing the yaml.

## Workflow with the user

1. `gravity structure plan` and show the tree.
2. Ask: which spaces are public vs private, who reads each (audiences), which repo files are imported
   verbatim (add `source:`), what should be AI-written (target a collection with a guides/changelog pass).
3. Edit the yaml; `gravity validate`.
4. `gravity structure apply --dry-run`, confirm, `gravity structure apply`.
