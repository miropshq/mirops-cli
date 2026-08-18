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

1. **Fork** and branch from `main` (`feat/…`, `fix/…`).
2. Keep the change focused. Add/adjust tests for behavior changes.
3. Run `go vet ./... && go test ./... && go build ./...` before pushing.
4. Open a PR.

### Commit & PR conventions

This project uses **[Conventional Commits](https://www.conventionalcommits.org/)**:

```
feat: …      # a new feature (minor)
fix: …       # a bug fix (patch)
docs: …      # documentation only
refactor: …  # no behavior change
test: …      # tests only
chore: …     # tooling/CI
```

A breaking change adds a `!` (`feat!: …`) or a `BREAKING CHANGE:` footer (major).

## Reporting bugs & requesting features

Open an issue with what you expected, what happened, the target vs current Kubernetes version, and (if
relevant) a redacted `report.json`. For security issues, **do not** open a public issue — see
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache License 2.0](LICENSE).
