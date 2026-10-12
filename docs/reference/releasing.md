# Releasing upkeep

How a release of upkeep itself is cut: what to set up once, how to validate it
without publishing, and what signs the macOS binaries.

This is about releasing *this tool*. For drafting release notes for a Drupal
module you maintain, see `upkeep notes`.

A tag a human pushed, and nothing else. `upkeep` itself drafts release notes
and leaves the tagging to a person; its own releases work the same way.

```bash
git tag v1.0.0 && git push --tags
```

`.github/workflows/go-release.yml` then runs GoReleaser:
it re-runs the gates against the tag (CI ran them against the commit, and a
tag can be pushed at a commit CI never saw), cross-compiles for darwin and
linux on amd64 and arm64, publishes a GitHub release with archives and
checksums, and pushes a Homebrew cask to the tap.

```bash
brew install owenbush/tap/upkeep
```

Built for macOS and Linux, on amd64 and arm64. **Not Windows**, and not by
oversight: all three of ddev's supported Windows configurations require WSL2,
and the one it recommends for most users is Docker CE *inside* WSL2 — so the
Linux binary is the Windows answer, on the same side of the filesystem
boundary as the projects it drives. A native build would also need work it has
not had: `killWholeGroup` is `//go:build unix`, and three `$HOME` lookups are
not where Windows keeps a home directory. Shipping that untested would be
worse than not shipping it.

The binary is static — `CGO_ENABLED=0`, nothing linked — so it runs on any
glibc or musl host with no runtime to install. That is the whole practical
advantage over the PHP, and the release config is deliberate about it rather
than inheriting a default.

## What it needs set up once

- **`owenbush/homebrew-tap`**, a public repository. GoReleaser creates the
  `Casks/` directory in it; nothing else needs to be in it.
- **`HOMEBREW_TAP_GITHUB_TOKEN`**, a repository secret here: a fine-grained
  PAT with `contents: write` on the tap and nothing else. The default
  `GITHUB_TOKEN` cannot reach another repository. Without it the release still
  publishes working archives and only the brew route is missed, which is not
  worth failing a release over.
- **The five signing secrets** below, from the Apple Developer account.

Until the signing secrets are set, a tag still produces a release with working
archives and an unsigned cask: everything here degrades rather than failing.
A *malformed* secret does fail the release, before anything is published.

## Validating it without releasing

The workflow runs itself in dry-run on any pull request that touches the
release config — builds everything, publishes none of it, and keeps the
artifacts for a week. A release description is otherwise only ever exercised
at the moment you least want to debug it.

Locally:

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish
tar tzvf dist/upkeep_*_darwin_arm64.tar.gz
```

Both were worth doing. `check` refused the first two configs — `brews` and
then `homebrew_casks.binary` are deprecated, neither of which the JSON schema
flags — and extracting a snapshot archive showed the LICENSE going in as a
dangling `../LICENSE` symlink, which would have shipped every copy without the
notice MIT requires travel with it.

## Signed and notarized

macOS binaries are signed with a Developer ID certificate and notarized by
Apple, so Gatekeeper runs them without argument. The alternative — shipping
unsigned and having the cask strip the quarantine attribute — asks every user
to take on trust the thing Gatekeeper exists to check, and gives anyone
downloading the archive by hand a wall with no way past it but a terminal
incantation.

Both happen on the Linux runner. GoReleaser signs with `quill`, so no macOS
runner is involved.

It needs five more secrets, all scoped and revocable. They go on **this**
repository (Settings → Secrets and variables → Actions → repository
secrets); the tap repository needs none of its own, being the target of the
PAT rather than the thing that holds it:

| secret | what |
| --- | --- |
| `MACOS_SIGN_P12` | the Developer ID Application certificate, exported as .p12 and base64'd |
| `MACOS_SIGN_PASSWORD` | that export's password |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect API issuer id |
| `MACOS_NOTARY_KEY_ID` | that key's id |
| `MACOS_NOTARY_KEY` | the .p8 private key, base64'd |

Both base64 values must be **one line**. GoReleaser takes either a path or
base64'd contents for these two, and Go's base64 decoder rejects embedded
newlines — so `base64 -i x.p12 | tr -d '\n'`, not a wrapped encode.

An App Store Connect API key rather than an app-specific password: it is
scoped to notarization, revocable on its own, and does not carry the Apple ID.

The release **waits for Apple's verdict** rather than publishing and hoping. A
release page that says "signed" while the submission was rejected is worse
than one that failed loudly.

Signing is enabled only when the certificate is actually in the environment,
so a fork's pull request still builds. What that produces is unsigned, and the
cask it generates strips the quarantine attribute instead — the two paths are
one template, checked both ways.
