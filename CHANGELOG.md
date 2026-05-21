# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-05-21

### Added
- Core Authelia secrets (`SESSION_ENCRYPTION_KEY`, `STORAGE_ENCRYPTION_KEY`,
  `OIDC_HMAC_SECRET`, `OIDC_PRIVATE_KEY`) are generated automatically into a
  managed Secret `<name>-secrets` when `deployment.existingSecret` is not set.
  Values are generated once and never rotated. The OIDC HMAC secret is a random
  alphanumeric string and the issuer key is an RSA-4096 PKCS#1 key.
- `deployment.volumeClaimTemplate` lets the operator create and manage a
  PersistentVolumeClaim `<name>-data` mounted at `/data` for persistent state
  (e.g. SQLite). Permitted only when `replicas` is 1; the PVC is retained when
  the Authelia resource is deleted. Without it, `/data` is an `emptyDir`.

### Changed
- **Breaking:** `deployment.secretName` renamed to `deployment.existingSecret`.
  When set it is used as-is; when unset the core secrets are generated.
- **Breaking:** PostgreSQL, Redis and SMTP password mapping are now opt-in.
  `deployment.postgresSecretName` / `deployment.redisSecretName` no longer
  default to `authelia-db-app` / `redis-ha`, and the SMTP password is only
  exposed when `deployment.smtpPassword` is `true`. Their volumes and
  `*_FILE` environment variables are omitted unless configured.
- The OIDC HMAC and issuer-key `*_FILE` environment variables are only set when
  at least one `AutheliaOAuthClient` targets the instance, so an Authelia with
  no OIDC clients starts cleanly.

## [0.2.0] - 2026-05-21

### Added
- First-class `spec.authenticationBackend` with mutually exclusive `file` and
  `ldap` backends. The file users database and the LDAP bind password are
  sourced from Secret key references, mounted into the container, and referenced
  via `authentication_backend.file.path` or the
  `AUTHELIA_AUTHENTICATION_BACKEND_LDAP_PASSWORD_FILE` secret env variable.

### Fixed
- Correct the Authelia RBAC rules to target the `authelias` resource (the
  manager could not list/watch the CRD due to a stale `authelia` resource name).

## [0.1.1] - 2026-05-20

### Fixed
- Replace `interface{}` with `any` to satisfy the `modernize` linter.

## [0.1.0] - 2026-05-20

### Added
- Initial release. `Authelia` and `AutheliaOAuthClient` CRDs in the
  `authelia.snosr.se/v1alpha1` API group.
- Authelia controller renders the configuration into a ConfigMap and reconciles
  a Deployment and Service, injecting OIDC clients under
  `identity_providers.oidc.clients`.
- AutheliaOAuthClient controller generates a client secret and stores
  `client_id` / `client_secret` in a `<name>-oauth-secret` Secret in the
  resource's namespace.
- GitHub Actions workflows for linting, testing, building the controller image,
  and releasing multi-arch images to GHCR on `v*` tags.

[0.3.0]: https://github.com/mnorrsken/heliop/releases/tag/v0.3.0
[0.2.0]: https://github.com/mnorrsken/heliop/releases/tag/v0.2.0
[0.1.1]: https://github.com/mnorrsken/heliop/releases/tag/v0.1.1
[0.1.0]: https://github.com/mnorrsken/heliop/releases/tag/v0.1.0
