# Contributing to mirops-cli

Thanks for your interest in mirops-cli — the command-line tool that runs and enforces a mirops
upgrade-readiness analysis, typically in CI pipelines. Contributions of all kinds are welcome: bug
reports, features, and docs.

By participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## The ecosystem

mirops is a few repositories that work together:

| Repo | What it is |
| --- | --- |
| **mirops** | The operator (Go, controller-runtime) — collector, mirror engine, scoring, decision. |
| **mirops-headlamp-plugin** | The Headlamp plugin (React/TS) that visualizes the report. |
| **mirops-cli** (this repo) | CLI to run/enforce an analysis in CI. |
| **helm-charts** | The `mirops-operator` Helm chart. |
| **mirops-compat** | The community add-on ↔ Kubernetes compatibility matrix. |

The scoring and decision model lives in the **operator**; the CLI consumes and enforces its report.

## Getting started

Prerequisites: Go (see `go.mod`) and a cluster (kind/minikube/AKS/EKS) to run against.

```bash
go build ./...
go test ./...
go vet ./...
```

## Making a change

1. **Fork** and branch from **`beta`** (`feat/…`, `fix/…`, `docs/…`):
   ```bash
   git fetch origin && git checkout -b feat/my-change origin/beta
   ```
2. Keep the change focused. Add/adjust tests for behavior changes.
3. Run `go vet ./... && go test ./... && go build ./...` before pushing.
4. Open a PR **against `beta`** — never against `main`. Give it a [Conventional Commits](#commit--pr-conventions)
   title: the PR is squash-merged, so its title becomes the commit that decides the next version.

### Commit & PR conventions

This project uses **[Conventional Commits](https://www.conventionalcommits.org/)** for PR titles:

```
feat: …      # a new feature                → minor release
fix: …       # a bug fix                    → patch release
docs: …      # documentation only           → no release
ci: …        # the release/CI workflows     → no release
refactor: …  # no behavior change           → no release
test: …      # tests only                   → no release
chore: …     # tooling, dependencies        → no release
```

A breaking change adds a `!` (`feat!: …`) or a `BREAKING CHANGE:` footer (major).

## How releases work

Every change goes **branch → `beta` → `main`**, and both hops are squash-merged.

```text
your branch ──PR (squash)──▶ beta ──PR (squash)──▶ main
                              │                     │
                        vX.Y.Z-beta.N        vX.Y.Z (Latest)

after each stable release: beta is reset to main
```

1. **Merging into `beta`** publishes a pre-release, `vX.Y.Z-beta.N`, when the PR title is a `feat:` or
   `fix:` — try it in a real pipeline before it's stable. Pre-releases never become *Latest*.
2. **A maintainer opens a PR `beta` → `main`** when the betas are ready, titled with the highest change it
   carries (`feat: …` if any feature, else `fix: …`). Merging it publishes the stable `vX.Y.Z`.
3. **The release workflow then resets `beta` to `main`.** The squash on `main` already holds everything on
   `beta`, so `beta`'s own commits are dropped — otherwise the next beta would count them again. It does
   so only when both hold the same code; if `beta` took a PR after the release one, the job fails and a
   maintainer resets it by hand.

`beta` and `main` accept changes only through pull requests. The one exception is the
`mirops-cli-release-bot` GitHub App, which performs the reset in step 3.

## Reporting bugs & requesting features

Open an issue with what you expected, what happened, the target vs current Kubernetes version, and (if
relevant) a redacted `report.json`. For security issues, **do not** open a public issue — see
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache License 2.0](LICENSE).
