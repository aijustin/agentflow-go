# Security Policy

## Supported Versions

Security fixes are applied to the latest release line. Until the first stable release, the `main` branch is the supported development line.

## Reporting a Vulnerability

Please report suspected vulnerabilities privately to the project maintainers. Do not open a public issue for exploitable behavior, leaked credentials, authentication bypasses, or remote execution risks.

Include:

- Affected version or commit
- Reproduction steps
- Expected and actual impact
- Any known workaround

We will acknowledge reports as soon as possible, assess severity, and publish fixes with release notes when appropriate.

## Security Expectations

- Never commit API keys, model credentials, tokens, or production secrets.
- Use a strong `AGENT_TOKEN_SECRET` in production.
- Do not expose the debug HTTP console to untrusted networks without an external authentication layer.
- Run `make security` or `govulncheck ./...` before releases.
