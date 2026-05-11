# Mirops CLI

`mirops-cli` reads a Mirops upgrade-analysis report and turns it into a command-line decision for humans and CI/CD pipelines. It can print a table, emit JSON, and optionally exit with a non-zero status when the report blocks an upgrade.

The CLI is designed to consume reports produced by the `mirops` Kubernetes operator.

## Features

- Reads reports from local files and `file://` sources
- Supports `http://` and `https://` report URLs
- Supports `s3://` and `azure://` report sources through provider implementations
- Evaluates report score against a threshold
- Prints table or JSON output
- Can enforce the decision by exiting with code `1` when blocked
- Supports environment-variable fallbacks for automation

## Requirements

- Go 1.26.2 or newer

## Build

```sh
go build -o bin/mirops .
```

Run from source:

```sh
go run . scan --source ./report.json
```

## Usage

Read a local report:

```sh
mirops scan --source ./mirops-report.json
```

Read a file URL:

```sh
mirops scan --source file:///tmp/mirops-report.json
```

Read an HTTP report:

```sh
mirops scan --source https://example.com/mirops-report.json
```

Emit JSON:

```sh
mirops scan --source ./mirops-report.json --output json
```

Use a custom threshold:

```sh
mirops scan --source ./mirops-report.json --threshold 80
```

Fail the pipeline when the decision is blocked:

```sh
mirops scan --source ./mirops-report.json --enforce
```

## Command Reference

```text
mirops scan [flags]
```

| Flag | Environment variable | Description |
| ---- | -------------------- | ----------- |
| `--source` | `MIROPS_SOURCE` | Report source: path, `file://`, `s3://`, `azure://`, `http://`, or `https://` |
| `--threshold` | `MIROPS_THRESHOLD` | Minimum score required before the result is blocked |
| `--enforce` | `MIROPS_ENFORCE` | Exit with code `1` when the result is blocked |
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

The CLI uses the report score and threshold to produce one of three results:

| Result | Meaning |
| ------ | ------- |
| `BLOCK` | Score is below the effective threshold |
| `WARNING` | Score is at or above the threshold, but below 90 |
| `SAFE` | Score is 90 or above |

If `--threshold` is not provided, the CLI uses the threshold embedded in the report. If the report does not include a threshold, the default flag value is used.

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
