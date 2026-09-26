# Mirops CLI

`mirops-cli` gates CI/CD pipelines on the live cluster mirror published by the `mirops` operator. One command, `mirops scan`, runs the checks its inputs ask for — an upgrade check, a namespace's current state and, from v0.3.0, a deploy check of the manifests you're about to apply. Pipelines are configured only through `MIROPS_*` environment variables, so they don't change when an upgrade window opens or closes.

The CLI is designed to consume reports produced by the `mirops` Kubernetes operator.

## Features

- Reads reports from local files and `file://` sources
- Supports `http://` and `https://` report URLs
- Supports `s3://` and `azure://` report sources through provider implementations
- One command, `mirops scan`: the inputs you give pick the checks
- Upgrade check switched on with `MIROPS_UPGRADE`, reading the UpgradeAnalysis report at `MIROPS_UPGRADE_SOURCE` — the target version comes from that report, and the check skips itself once the cluster runs it
- Namespaces' current state from the mirror: one, a list, or `all` (informational)
- Renders the operator's upgrade decision (`SAFE` / `WARNING` / `CRITICAL`)
- Prints a table, or JSON with a `schemaVersion` for other tools
- Every flag can be set as a `MIROPS_*` environment variable
- Fixed exit codes: `0` pass, `1` blocked (with `--enforce`), `2` couldn't evaluate — never a silent pass

## Install

Download the release binary for your platform, make it executable, and put it on your `PATH`. **No `sudo` required** — this works the same on a laptop or in a CI pipeline.

```sh
# 1. Download the binary for your platform (see the table below for the name)
curl -fsSL -O https://github.com/miropshq/mirops-cli/releases/latest/download/mirops-darwin-arm64

# 2. Install it on a user-writable dir on your PATH
mkdir -p "$HOME/.local/bin"
cp mirops-darwin-arm64 "$HOME/.local/bin/mirops"
chmod +x "$HOME/.local/bin/mirops"

# 3. Ensure the dir is on your PATH (add to your shell rc to persist)
export PATH="$HOME/.local/bin:$PATH"

# 4. Verify
mirops --version
```

### Platform binaries

| Platform | Binary |
| --- | --- |
| macOS (Apple Silicon) | `mirops-darwin-arm64` |
| macOS (Intel) | `mirops-darwin-amd64` |
| Linux (x86_64) | `mirops-linux-amd64` |
| Linux (arm64) | `mirops-linux-arm64` |
| Windows (x86_64) | `mirops-windows-amd64.exe` |

Swap the binary name in the `curl` URL for your platform.

### Latest vs. pinned version

```sh
# Always the newest release
curl -fsSL -O https://github.com/miropshq/mirops-cli/releases/latest/download/mirops-linux-amd64

# Pin a version (reproducible — recommended for CI)
curl -fsSL -O https://github.com/miropshq/mirops-cli/releases/download/v0.2.0/mirops-linux-amd64
```

### Verify the checksum (optional)

```sh
curl -fsSL -O https://github.com/miropshq/mirops-cli/releases/latest/download/checksums.txt
shasum -a 256 --check --ignore-missing checksums.txt   # macOS
sha256sum --check --ignore-missing checksums.txt        # Linux
```

## Requirements

Building from source (not needed to install a release binary):

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

Point `MIROPS_SOURCE` at the ClusterMirror report (`<name>.mirror`, e.g. `s3://mirops-reports/prod/default.mirror`), then give `mirops scan` the inputs for the checks you want:

| Input | Check | Gates? |
| ----- | ----- | ------ |
| `--upgrade` / `MIROPS_UPGRADE=true` | **Upgrade.** Off by default. Gates on the UpgradeAnalysis report you point `--upgrade-source` / `MIROPS_UPGRADE_SOURCE` at; the target version comes from that report. The cluster already runs it → nothing to check (exit `0`). No `MIROPS_UPGRADE_SOURCE`, an unreadable report, or upgrade analysis off in the cluster → exit `2`. | Yes |
| `-n` / `--namespace` / `MIROPS_NAMESPACE` | **Namespaces' state**: one namespace, a comma-separated list, or `all` (only the namespaces with something at risk, plus a count of the healthy ones). Reads the mirror report, so it adds no load on the cluster. | Never |
| `-f` / `--file` / `MIROPS_FILE` | **Deploy** check of the manifests you're about to apply — arrives in v0.3.0. | Yes |

Give several and they all run: the namespaces' state first, the upgrade verdict last — only the verdict sets the exit code. Give none and the CLI exits `2` ("nothing to scan").

### Cluster upgrade pipeline

```yaml
env:
  MIROPS_SOURCE: s3://mirops-reports/prod/default.mirror
  MIROPS_UPGRADE: ${{ vars.MIROPS_UPGRADE }}   # "true" during the upgrade window, e.g. as a CI variable
  MIROPS_UPGRADE_SOURCE: s3://mirops-reports/prod/to-1-35.mirops
  MIROPS_ENFORCE: "true"
steps:
  - run: mirops scan
  - run: terraform apply
```

Upgrade analysis itself is switched on in the mirops install (Helm `upgrade.enabled=true`), which is where the UpgradeAnalysis — and so the target version — comes from. `MIROPS_UPGRADE` only tells this pipeline to gate on it, and `MIROPS_UPGRADE_SOURCE` says where its report is. Each source is read with the pipeline's own credentials for its scheme (AWS for `s3://`, Azure for `azure://`), so the two reports can live in different places. Once the cluster runs the target version the check reports `skipped` and passes, so the variables can stay set between upgrades.

### Namespaces' state

```sh
mirops scan -n payments
mirops scan -n payments,checkout,orders
mirops scan -n all
```

### Pointing at an analysis directly

`--source` can also be an UpgradeAnalysis report (`<name>.mirops`), as with v0.1.0 — pointing at it is the request, so the CLI gates on it as is (then `--upgrade-source` and `--namespace` don't apply).

## Command Reference

```text
mirops scan [flags]
```

Every flag can be set through the environment as `MIROPS_<FLAG>` (dashes become underscores). Precedence: flag, then environment, then default. A variable that doesn't parse stops the CLI with exit `2`.

| Flag | Environment variable | Description |
| ---- | -------------------- | ----------- |
| `--source` | `MIROPS_SOURCE` | ClusterMirror report: path, `file://`, `s3://`, `azure://`, `http://`, or `https://` |
| `--upgrade` | `MIROPS_UPGRADE` | Run the upgrade check against the report at `--upgrade-source` |
| `--upgrade-source` | `MIROPS_UPGRADE_SOURCE` | UpgradeAnalysis report to gate on: path, `file://`, `s3://`, `azure://`, `http://`, or `https://` |
| `-n`, `--namespace` | `MIROPS_NAMESPACE` | Namespaces to show the state of: one, a comma-separated list, or `all` |
| `-f`, `--file` | `MIROPS_FILE` | Manifests to check before deploying (v0.3.0) |
| `--enforce` | `MIROPS_ENFORCE` | Exit `1` when a check blocks |
| `--enforce-level` | `MIROPS_ENFORCE_LEVEL` | Minimum upgrade level that blocks: `critical` (default) or `warning` |
| `--output` | `MIROPS_OUTPUT` | `table` or `json` |
| `--timeout` | `MIROPS_TIMEOUT` | Request timeout duration |
| `--retry` | `MIROPS_RETRY` | Number of retries on failure |

SaaS-related flags are present but not implemented yet:

| Flag | Environment variable |
| ---- | -------------------- |
| `--api-url` | `MIROPS_API_URL` |
| `--api-token` | `MIROPS_API_TOKEN` |
| `--cluster` | `MIROPS_CLUSTER` |

### Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Passed, or nothing to check on purpose (no upgrade pending) |
| `1` | A check blocked, and `--enforce` is set |
| `2` | Couldn't evaluate: missing or unreadable source, unknown report kind, bad flag or variable, nothing to scan, `MIROPS_UPGRADE=true` without `MIROPS_UPGRADE_SOURCE`, upgrade check requested while upgrade analysis is off. Never treated as a pass. |

### JSON output

`--output json` prints one document with a `schemaVersion` (currently `1`) and one block per check that ran — `checks.upgrade` (`status`: `evaluated` or `skipped`, `source` — where the upgrade report was read — plus `blocking`, `level`, `blockers`, …) and `checks.namespaces` (a list). New fields may be added without a version bump; breaking changes bump `schemaVersion`.

## Decision Logic

The upgrade decision is computed by the `mirops` operator from deterministic facts (incompatible add-ons, lost PVCs, PDBs, CPU/memory pressure, pods not ready) and reported in `decision`. The CLI renders it verbatim — it does **not** recompute the gate from the score (`scores.total` is a readiness gauge only).

| Result | Meaning |
| ------ | ------- |
| `CRITICAL` | `decision.allow == false` — do not upgrade; reasons listed as blockers |
| `WARNING` | Upgrade possible but issues were detected |
| `SAFE` | Cluster ready, upgrade recommended |

With `--enforce`, the upgrade check blocks when the upgrade is not allowed (CRITICAL). `--enforce-level warning` is stricter and also blocks on WARNING. A namespace's state never blocks.

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
