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
	"maps"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
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

// renderConfig parses the user-supplied configuration, merges the first factor
// authentication backend (when set), injects the OIDC clients under
// identity_providers.oidc.clients, and returns the resulting YAML.
func renderConfig(base string, clients []oidcClient, backend *autheliav1alpha1.AuthenticationBackendSpec, session *runtime.RawExtension, hostname string) (string, error) {
	root := map[string]any{}
	if strings.TrimSpace(base) != "" {
		if err := yaml.Unmarshal([]byte(base), &root); err != nil {
			return "", fmt.Errorf("parsing config: %w", err)
		}
	}

	if backend != nil {
		if err := applyAuthenticationBackend(root, backend); err != nil {
			return "", err
		}
	}

	if err := applySession(root, session, hostname); err != nil {
		return "", err
	}

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

// applyAuthenticationBackend merges the configured file or ldap backend into
// authentication_backend, preserving sibling keys (e.g. password_reset) already
// present in the user config. The LDAP bind password is intentionally omitted;
// it is supplied at runtime through the *_PASSWORD_FILE environment variable.
func applyAuthenticationBackend(root map[string]any, backend *autheliav1alpha1.AuthenticationBackendSpec) error {
	ab := childMap(root, "authentication_backend")

	switch {
	case backend.File != nil:
		delete(ab, "ldap")
		file, err := fileBackendMap(backend.File)
		if err != nil {
			return err
		}
		ab["file"] = file
	case backend.LDAP != nil:
		delete(ab, "file")
		ldap, err := ldapBackendMap(backend.LDAP)
		if err != nil {
			return err
		}
		ab["ldap"] = ldap
	}
	return nil
}

// applySession merges the user-provided session dict (verbatim) into the config
// session section, then ensures a cookie exists: if none is configured (here or
// in config) and a hostname is given, a default cookie is generated with
// authelia_url https://<hostname> and the parent domain of hostname.
func applySession(root map[string]any, sessionRaw *runtime.RawExtension, hostname string) error {
	if (sessionRaw == nil || len(sessionRaw.Raw) == 0) && hostname == "" {
		return nil
	}

	session := childMap(root, "session")

	if sessionRaw != nil && len(sessionRaw.Raw) > 0 {
		var user map[string]any
		if err := json.Unmarshal(sessionRaw.Raw, &user); err != nil {
			return fmt.Errorf("decoding session: %w", err)
		}
		maps.Copy(session, user)
	}

	if hostname != "" {
		if _, ok := session["cookies"]; !ok {
			session["cookies"] = []any{map[string]any{
				"domain":       parentDomain(hostname),
				"authelia_url": "https://" + hostname,
			}}
		}
	}
	return nil
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

// fileBackendMap merges the verbatim file config and forces path to the mounted
// users Secret location (operator-controlled).
func fileBackendMap(f *autheliav1alpha1.FileAuthenticationBackend) (map[string]any, error) {
	m := map[string]any{}
	if err := mergeRaw(m, f.Config); err != nil {
		return nil, err
	}
	m["path"] = fileUsersPath(f.UsersSecret)
	return m, nil
}

// ldapBackendMap merges the verbatim ldap config. The bind password is supplied
// through the *_PASSWORD_FILE environment variable, never the config.
func ldapBackendMap(l *autheliav1alpha1.LDAPAuthenticationBackend) (map[string]any, error) {
	m := map[string]any{}
	if err := mergeRaw(m, l.Config); err != nil {
		return nil, err
	}
	delete(m, "password")
	return m, nil
}

// mergeRaw decodes a RawExtension object and copies its keys into dst.
func mergeRaw(dst map[string]any, raw *runtime.RawExtension) error {
	if raw == nil || len(raw.Raw) == 0 {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw.Raw, &v); err != nil {
		return fmt.Errorf("decoding config: %w", err)
	}
	maps.Copy(dst, v)
	return nil
}
