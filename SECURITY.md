# Security Policy

mirops-cli runs and enforces a mirops upgrade-readiness analysis, typically in CI. We take its
security seriously and appreciate responsible disclosure.

## Supported versions

Security fixes land on the latest released version. We recommend always running the most recent
release.

| Version | Supported |
| --- | --- |
| latest release | ✅ |
| older releases | ❌ (please upgrade) |

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately through either channel:

- **GitHub private vulnerability reporting** — *Security → Report a vulnerability* on the repository.
  This opens a private advisory visible only to the maintainers.
- **Email** — [security@mirops.com](mailto:security@mirops.com).

Please include a description of the issue and its impact, steps to reproduce, and the affected
version(s) and environment.

### What to expect

- **Acknowledgement** within a few business days.
- An assessment and, if confirmed, a fix timeline shared with you.
- Credit in the advisory once a fix is released, unless you prefer to remain anonymous.

## Scope notes

The CLI reads cluster state using the credentials you give it (a kubeconfig or in-cluster service
account). Run it with **least-privilege, read-only** access, and protect the `report.json` it emits —
the report enumerates workloads, namespaces, and versions. In CI, prefer short-lived, scoped
credentials over long-lived secrets.

Thank you for helping keep mirops and its users safe.
