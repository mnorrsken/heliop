# heliop

A Kubernetes operator for [Authelia](https://www.authelia.com/). Heliop runs and
configures Authelia declaratively, and manages its OpenID Connect clients as
first-class Kubernetes resources.

## Description

Heliop (built with [kubebuilder](https://book.kubebuilder.io/)) provides two
custom resources in the `authelia.snosr.se/v1alpha1` API group:

- **`Authelia`** — deploys and configures an Authelia instance. The operator
  renders the configuration into a ConfigMap and reconciles a Deployment and
  Service. The CR carries the Authelia config verbatim under
  `spec.settings.additionalConfig`, plus the Secret references the operator wires
  into it.
- **`AutheliaOAuthClient`** — declares an OIDC client. The operator generates a
  client secret, stores `client_id` / `client_secret` in a
  `<name>-oauth-secret` Secret, and injects the client into the Authelia
  configuration under `identity_providers.oidc.clients`.

### Features

- **Generated core secrets** — the session, storage encryption and OIDC secrets
  are generated automatically (and never rotated) unless you supply an existing
  Secret via `deployment.existingSecret`.
- **Uniform secret wiring** — `spec.settings.secrets` is a list of
  `{name, secret}` mapping any Authelia env variable to a Secret value: `_FILE`
  variables are mounted and set to the file path, others are set directly via
  `valueFrom`. `fileUsersSecret` is mounted and set as
  `authentication_backend.file.path`. All opt-in, so a minimal (e.g. SQLite, no
  Redis) instance just works.
- **OIDC clients as resources** — each `AutheliaOAuthClient` generates its own
  secret; OIDC is only enabled in Authelia when at least one client exists.
- **Dynamic rules from Ingress** — annotate an `Ingress` with a `heliop/rule`
  JSON rule and the operator generates a matching `access_control` rule from its
  hosts (see [Ingress access control](#ingress-access-control)).
- **Persistent storage** — `deployment.volumeClaimTemplate` has the operator
  create a retained PVC mounted at `/data` (requires `replicas: 1`); otherwise
  `/data` is an `emptyDir`.
- **Self-contained rendering** — the operator renders the final config (hashing
  OIDC client secrets with PBKDF2 itself) and rolls the Deployment automatically
  when the config changes; there is no init container.

## Getting Started

### Prerequisites

- A Kubernetes v1.24+ cluster and `kubectl`.
- For local development: Go 1.24+, Docker, and `make`.

### Install

Install the CRDs and deploy the controller using a released image:

```sh
make install
make deploy IMG=ghcr.io/mnorrsken/heliop:0.3.0
```

The controller runs in the `heliop-system` namespace. Container images are
published to `ghcr.io/mnorrsken/heliop` on every `v*` tag (multi-arch
`linux/amd64` + `linux/arm64`).

### Uninstall

```sh
make undeploy   # remove the controller
make uninstall  # remove the CRDs
```

## Usage

A minimal Authelia using the file backend, generated core secrets, SQLite on a
managed PVC, and no OIDC clients:

```yaml
apiVersion: authelia.snosr.se/v1alpha1
kind: Authelia
metadata:
  name: authelia
  namespace: authelia
spec:
  # Portal FQDN; generates the default session cookie.
  hostname: sso.example.com
  deployment:
    replicas: 1
    # existingSecret omitted -> core secrets generated into "authelia-secrets"
    volumeClaimTemplate:
      accessModes: [ReadWriteOnce]
      resources:
        requests:
          storage: 1Gi
  settings:
    # Mounted by the operator; sets authentication_backend.file.path.
    fileUsersSecret:
      name: authelia-users
      key: users_database.yml
    # additionalConfig is verbatim Authelia config. The operator layers on OIDC
    # clients, the hostname-derived session cookie, and the file.path above.
    additionalConfig:
      server:
        address: tcp://0.0.0.0:9091/
      storage:
        local:
          path: /data/db.sqlite3
      access_control:
        default_policy: two_factor
```

Add an OIDC client (this also enables the identity provider):

```yaml
apiVersion: authelia.snosr.se/v1alpha1
kind: AutheliaOAuthClient
metadata:
  name: argocd
  namespace: authelia
spec:
  autheliaRef:
    name: authelia
  clientID: argocd
  clientName: Argo CD
  authorizationPolicy: one_factor
  redirectURIs:
    - https://argocd.example.com/auth/callback
```

The generated `client_id` / `client_secret` are available in the
`argocd-oauth-secret` Secret. More examples live in [config/samples](config/samples/).

## Ingress access control

Annotate an `Ingress` with a `heliop/rule` annotation whose value is a JSON
Authelia access_control rule. The operator forces the rule's `domain` to the
Ingress hosts (`spec.rules[].host` + `spec.tls[].hosts`) — it cannot be set in
the annotation — and prepends generated rules before the static rules in
`additionalConfig` (Authelia is first-match-wins). Set `default_policy` (e.g.
`deny`) in `additionalConfig`.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grafana
  annotations:
    # Single rule. policy is required and must be one of
    # bypass / one_factor / two_factor / deny. Any other Authelia rule fields
    # (subject, networks, resources, methods, ...) are passed through verbatim.
    heliop/rule: '{"policy":"two_factor","subject":["group:admins"]}'
    # Multiple rules: add a numeric suffix.
    heliop/rule-1: '{"policy":"bypass","resources":["^/health$"]}'
spec:
  rules:
    - host: grafana.example.com   # becomes the rule domain
      # ...
```

**Ordering** (most specific first): rules with longer `resources` patterns come
first, then exact hosts before wildcards, then a stable key. This lets a narrow
path rule (e.g. `^/admin/.*`) take precedence over a broader one on the same
host. An Ingress without any `heliop/rule[-*]` annotation is ignored.

## Development

```sh
make test    # run unit + envtest suites
make lint    # run golangci-lint
make run     # run the controller against your current kubecontext
```

Run `make help` for all available targets. See the [Kubebuilder
Documentation](https://book.kubebuilder.io/introduction.html) for background.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
