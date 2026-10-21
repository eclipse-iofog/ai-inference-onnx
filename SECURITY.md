# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| The current `develop` branch | Yes |

## Reporting a vulnerability

If you believe you have found a security issue in this repository:

1. **Do not** open a public GitHub issue for an exploitable vulnerability.
2. Report it through the Eclipse security process at [https://www.eclipse.org/security/](https://www.eclipse.org/security/) (`security@eclipse.org`). Include a description of the issue and its impact, steps to reproduce, and the affected commit.
3. Maintainers listed in [MAINTAINERS.md](MAINTAINERS.md) triage the report.

For bugs that are not security issues, use the public issue tracker or [CONTRIBUTING.md](CONTRIBUTING.md).

## Security gates

Before a tag, run these from the repository root:

```bash
make security-code
make vulncheck
```

`make security-code` runs gosec on the engine module and the dashboard module. `make vulncheck` runs govulncheck on both modules, then `go mod verify`. The goal is zero vulnerabilities affecting call paths. The same vulnerability scan runs in CI.

## Known vulnerability exceptions

The exception table is empty. There are no accepted exceptions.

| GO ID | CVE | Component | Rationale | Fix timeline |
| --- | --- | --- | --- | --- |

## Exception policy

A new exception needs:

1. A row in the table above: the GO id, the CVE if there is one, the component, the rationale, and the fix timeline.
2. The matching GO id in `ALLOWED_VULNS` in [`scripts/vulncheck.sh`](scripts/vulncheck.sh).
3. A changelog note at the next release.

A finding that is not listed here fails `make vulncheck` and CI.
