# heliop

A Kubernetes operator for [Authelia](https://www.authelia.com/). Heliop runs and
configures Authelia declaratively, and manages its OpenID Connect clients as
first-class Kubernetes resources.

## Description

Heliop (built with [kubebuilder](https://book.kubebuilder.io/)) provides two
custom resources in the `authelia.snosr.se/v1alpha1` API group:

- **`Authelia`** — deploys and configures an Authelia instance. The operator
  renders the configuration into a ConfigMap and reconciles a Deployment and
  Service. The CR carries the raw Authelia `config` plus first-class fields for
  the authentication backend, persistent storage, and secrets.
- **`AutheliaOAuthClient`** — declares an OIDC client. The operator generates a
  client secret, stores `client_id` / `client_secret` in a
  `<name>-oauth-secret` Secret, and injects the client into the Authelia
  configuration under `identity_providers.oidc.clients`.

### Features

- **Generated core secrets** — the session, storage encryption and OIDC secrets
  are generated automatically (and never rotated) unless you supply an existing
  Secret via `deployment.existingSecret`.
- **First-class authentication backend** — `spec.authenticationBackend.file` or
  `.ldap`, with the users database / bind password sourced from Secret key
  references and mounted/wired automatically.
- **OIDC clients as resources** — each `AutheliaOAuthClient` generates its own
  secret; OIDC is only enabled in Authelia when at least one client exists.
- **Opt-in integrations** — PostgreSQL, Redis and SMTP password mapping are only
  wired when configured, so a minimal (e.g. SQLite, no Redis) instance just works.
- **Persistent storage** — `deployment.volumeClaimTemplate` has the operator
  create a retained PVC mounted at `/data` (requires `replicas: 1`); otherwise
  `/data` is an `emptyDir`.
- **Traefik integration** — `spec.traefik` generates the portal IngressRoute (at
  `spec.hostname`) and a `<name>-forwardauth` Middleware for protecting other
  routes.
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
  deployment:
    replicas: 1
    # existingSecret omitted -> core secrets generated into "authelia-secrets"
    volumeClaimTemplate:
      accessModes: [ReadWriteOnce]
      resources:
        requests:
          storage: 1Gi
  authenticationBackend:
    file:
      usersSecret:
        name: authelia-users
        key: users_database.yml
  config: |
    server:
      address: 'tcp://0.0.0.0:9091/'
    storage:
      local:
        path: '/data/db.sqlite3'
    session:
      cookies:
        - domain: 'example.com'
          authelia_url: 'https://sso.example.com'
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
