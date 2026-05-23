/*
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
*/

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// configChecksum returns a short hex digest of the rendered configuration, used
// as a pod-template annotation so config changes trigger a Deployment rollout.
func configChecksum(rendered string) string {
	sum := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(sum[:])
}

// oidcClient pairs an OAuth client resource with the PBKDF2 digest of its
// generated secret (empty for public clients).
type oidcClient struct {
	spec         autheliav1alpha1.AutheliaOAuthClientSpec
	secretDigest string
}

// clientToMap converts a client spec into the map structure expected under
// identity_providers.oidc.clients in the Authelia configuration. Confidential
// clients embed the pre-hashed PBKDF2 digest of their secret.
func clientToMap(c oidcClient) map[string]any {
	m := map[string]any{
		"client_id": c.spec.ClientID,
	}
	if c.spec.ClientName != "" {
		m["client_name"] = c.spec.ClientName
	}
	if c.spec.Public {
		m["public"] = true
	} else {
		m["client_secret"] = c.secretDigest
	}
	if c.spec.AuthorizationPolicy != "" {
		m["authorization_policy"] = c.spec.AuthorizationPolicy
	}
	if c.spec.ConsentMode != "" {
		m["consent_mode"] = c.spec.ConsentMode
	}
	if c.spec.ClaimsPolicy != "" {
		m["claims_policy"] = c.spec.ClaimsPolicy
	}
	if c.spec.Lifespan != "" {
		m["lifespan"] = c.spec.Lifespan
	}
	if len(c.spec.RedirectURIs) > 0 {
		m["redirect_uris"] = toIfaceSlice(c.spec.RedirectURIs)
	}
	if len(c.spec.Scopes) > 0 {
		m["scopes"] = toIfaceSlice(c.spec.Scopes)
	}
	if len(c.spec.GrantTypes) > 0 {
		m["grant_types"] = toIfaceSlice(c.spec.GrantTypes)
	}
	if len(c.spec.ResponseTypes) > 0 {
		m["response_types"] = toIfaceSlice(c.spec.ResponseTypes)
	}
	if c.spec.TokenEndpointAuthMethod != "" {
		m["token_endpoint_auth_method"] = c.spec.TokenEndpointAuthMethod
	}
	if c.spec.UserinfoSignedResponseAlg != "" {
		m["userinfo_signed_response_alg"] = c.spec.UserinfoSignedResponseAlg
	}
	return m
}

func toIfaceSlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// renderConfig builds the Authelia configuration from settings.additionalConfig,
// wires the backend Secret references, generates a default session cookie from
// hostname, injects the OIDC clients, and returns the resulting YAML.
func renderConfig(settings autheliav1alpha1.AutheliaSettings, clients []oidcClient, hostname string) (string, error) {
	root := map[string]any{}
	if raw := settings.AdditionalConfig; raw != nil && len(raw.Raw) > 0 {
		if err := json.Unmarshal(raw.Raw, &root); err != nil {
			return "", fmt.Errorf("parsing additionalConfig: %w", err)
		}
	}

	applyBackendSecrets(root, settings)
	applySession(root, hostname)

	if len(clients) > 0 {
		// Sort by client ID so output is deterministic regardless of list order.
		sorted := make([]oidcClient, len(clients))
		copy(sorted, clients)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].spec.ClientID < sorted[j].spec.ClientID
		})

		clientMaps := make([]any, 0, len(sorted))
		for _, c := range sorted {
			clientMaps = append(clientMaps, clientToMap(c))
		}

		idp := childMap(root, "identity_providers")
		oidc := childMap(idp, "oidc")
		oidc["clients"] = clientMaps
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshalling config: %w", err)
	}
	return string(out), nil
}

// childMap returns the nested map at key, creating it if absent or if the
// existing value is not a map.
func childMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// applyBackendSecrets wires the operator-managed backend Secret references into
// the configuration: it sets authentication_backend.file.path to the mounted
// users database when fileUsersSecret is set, and strips any
// authentication_backend.ldap.password (supplied via the environment) when
// ldapPasswordSecret is set.
func applyBackendSecrets(root map[string]any, settings autheliav1alpha1.AutheliaSettings) {
	if settings.FileUsersSecret != nil {
		ab := childMap(root, "authentication_backend")
		file := childMap(ab, "file")
		file["path"] = fileUsersPath(*settings.FileUsersSecret)
	}
	if settings.Secrets != nil && settings.Secrets.LDAPPassword != nil {
		ab := childMap(root, "authentication_backend")
		ldap := childMap(ab, "ldap")
		delete(ldap, "password")
	}
}

// applySession ensures a session cookie exists: when none is configured and a
// hostname is given, a default cookie is generated with authelia_url
// https://<hostname> and the parent domain of hostname.
func applySession(root map[string]any, hostname string) {
	if hostname == "" {
		return
	}
	session := childMap(root, "session")
	if _, ok := session["cookies"]; !ok {
		session["cookies"] = []any{map[string]any{
			"domain":       parentDomain(hostname),
			"authelia_url": "https://" + hostname,
		}}
	}
}

// parentDomain returns the cookie domain for a portal hostname: the hostname
// with its first label stripped (auth.example.com -> example.com), or the
// hostname unchanged when it has no subdomain.
func parentDomain(hostname string) string {
	if i := strings.IndexByte(hostname, '.'); i >= 0 && i < len(hostname)-1 {
		return hostname[i+1:]
	}
	return hostname
}
