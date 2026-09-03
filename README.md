# Mirops CLI

`mirops-cli` reads a Mirops upgrade-analysis report and turns it into a command-line decision for humans and CI/CD pipelines. It can print a table, emit JSON, and optionally exit with a non-zero status when the report blocks an upgrade.

The CLI is designed to consume reports produced by the `mirops` Kubernetes operator.

## Features

- Reads reports from local files and `file://` sources
- Supports `http://` and `https://` report URLs
- Supports `s3://` and `azure://` report sources through provider implementations
- Renders the operator's upgrade decision (`SAFE` / `WARNING` / `CRITICAL`)
- Prints table or JSON output
- Can enforce the decision by exiting with code `1` when the upgrade is not allowed
- Supports environment-variable fallbacks for automation

## Requirements

- Go 1.26.2 or newer

## Build

```sh
go build -o bin/mirops .
```

Run from source:

```sh
go run . scan --source ./report.mirops
```

## Usage

Read a local report:

```sh
mirops scan --source ./mirops-report.mirops
```

Read a file URL:

```sh
mirops scan --source file:///tmp/mirops-report.mirops
```

Read an HTTP report:

```sh
mirops scan --source https://example.com/mirops-report.mirops
```

Emit JSON:

```sh
mirops scan --source ./mirops-report.mirops --output json
```

Fail the pipeline when the upgrade is not allowed (CRITICAL):

```sh
mirops scan --source ./mirops-report.mirops --enforce
```

Fail also on WARNING (stricter policy):

```sh
mirops scan --source ./mirops-report.mirops --enforce --enforce-level warning
```

## Command Reference

```text
mirops scan [flags]
```

| Flag | Environment variable | Description |
| ---- | -------------------- | ----------- |
| `--source` | `MIROPS_SOURCE` | Report source: path, `file://`, `s3://`, `azure://`, `http://`, or `https://` |
| `--enforce` | `MIROPS_ENFORCE` | Exit with code `1` based on the report's decision |
| `--enforce-level` | `MIROPS_ENFORCE_LEVEL` | Minimum level that fails `--enforce`: `critical` (default) or `warning` |
| `--target-version` | `MIROPS_TARGET_VERSION` | Expected target version |
| `--output` | `MIROPS_OUTPUT` | Output format: `table` or `json` |
| `--timeout` | `MIROPS_TIMEOUT` | Request timeout duration |
| `--retry` | `MIROPS_RETRY` | Number of retries on failure |

SaaS-related flags are present but not implemented yet:

| Flag | Environment variable |
| ---- | -------------------- |
| `--api-url` | `MIROPS_API_URL` |
| `--api-token` | `MIROPS_API_TOKEN` |
| `--cluster` | `MIROPS_CLUSTER` |

## Decision Logic

The decision is computed by the `mirops` operator from deterministic facts (incompatible add-ons, lost PVCs, PDBs, CPU/memory pressure, pods not ready) and reported in `decision`. The CLI renders it verbatim — it does **not** recompute the gate from the score (`scores.total` is a readiness gauge only).

| Result | Meaning |
| ------ | ------- |
| `CRITICAL` | `decision.allow == false` — do not upgrade; reasons listed as blockers |
| `WARNING` | Upgrade possible but issues were detected |
| `SAFE` | Cluster ready, upgrade recommended |

With `--enforce`, the CLI exits `1` when the upgrade is not allowed (CRITICAL). `--enforce-level warning` is stricter and also fails on WARNING.

## Development

Run tests:

```sh
go test ./...
```

Format the code:

```sh
go fmt ./...
```

## License

See `LICENSE`.
