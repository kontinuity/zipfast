# Build & release pipeline

zipfast ships through four GitHub Actions workflows under `.github/workflows/`.

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `ci.yml` | PR to `main`, push to `main`, manual | gofmt + `go vet` + build + `go test`, web SPA build, Docker image sanity build (no push). The merge gate. |
| `docker.yml` | push to `main`, manual | Builds **amd64 + arm64** natively and pushes `main`, `main-<sha>`, `edge` multi-arch tags. Every merge = a fresh image (rapid releases). |
| `release.yml` | push tag `v*.*.*`, manual | Builds versioned multi-arch images (`<version>` + `latest`) on GHCR and creates a GitHub Release. |
| `zipline-sync.yml` | weekly schedule, manual | Opens a tracking issue when upstream Zipline ships a release newer than `ZIPLINE_PARITY`. |

## 1. Rapid releases

Merging to `main` automatically publishes:

- `ghcr.io/kontinuity/zipfast:main` and `:edge` (rolling)
- `ghcr.io/kontinuity/zipfast:main-<short-sha>` (immutable, pin-able)

No tag or manual step required. Use `:edge` for "always latest from main".

## 2. PR reviews & merge requests

`ci.yml` runs on every PR into `main`. Recommended branch-protection rule on `main`
(Settings → Branches → Add rule):

- Require a pull request before merging (1 approval; `CODEOWNERS` routes review to the owner)
- Require status checks to pass: **`Go — fmt / vet / build / test`**, **`golangci-lint`**, **`Web — SPA build`**, **`Docker — image builds (no push)`**
- Require branches to be up to date before merging

`golangci-lint` (pinned `v2.12.2`) runs against [`.golangci.yml`](../.golangci.yml) —
the standard linter set (errcheck, govet, ineffassign, staticcheck, unused), with
best-effort deferred `Close()` / `os.Remove` calls excluded.

## 3. Cutting a versioned release

zipfast uses its **own** version line (independent of Zipline's numbers).

```bash
# from a clean main:
git tag v0.1.0
git push origin v0.1.0
```

This builds `:0.1.0` + `:latest` (multi-arch) on GHCR and drafts a GitHub Release
with auto-generated notes. To rebuild without a new tag, run the **Release**
workflow manually and supply the version.

## 4. Zipline version alignment (good-to-have)

`ZIPLINE_PARITY` (repo root) records the upstream Zipline version zipfast has reached
feature parity with — currently **4.6.3**. It is:

- baked into release images as the `sh.zipfast.zipline-parity` OCI label, and
- printed in each GitHub Release's notes.

`zipline-sync.yml` checks weekly whether `diced/zipline` published something newer and,
if so, opens a `zipline-sync` issue with a compare link. When you finish porting an
upstream release, bump `ZIPLINE_PARITY` and cut a zipfast release.

## Required secrets

| Secret | Needed for | Notes |
|--------|-----------|-------|
| `GITHUB_TOKEN` | GHCR push, releases, issues | Built in — nothing to configure. |

Publishing is **GHCR-only** and uses the built-in `GITHUB_TOKEN`, so no additional
secrets need to be configured.

First GHCR push creates a **private** package — make it public (or grant pull access)
under the repo's *Packages* settings if you want anonymous `docker pull`.
