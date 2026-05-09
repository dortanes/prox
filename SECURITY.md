# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in prox, please report it responsibly.

**Do NOT open a public GitHub issue.**

Instead, [open a private security advisory](https://github.com/dortanes/prox/security/advisories/new) on GitHub.

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact

## Scope

Security issues in the following areas are in scope:

- Request routing bypass (reaching unintended upstreams)
- Path traversal in file serving (`serve` action)
- TLS configuration weaknesses
- Config injection or parsing vulnerabilities
- Denial of service via crafted requests or configs
