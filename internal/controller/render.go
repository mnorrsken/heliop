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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// oidcClient pairs an OAuth client resource with the environment variable name
// used to expose its generated secret to the Authelia container.
type oidcClient struct {
	spec    autheliav1alpha1.AutheliaOAuthClientSpec
	envName string
}

// envVarName derives a stable, shell-safe environment variable name for a
// client ID, e.g. "argocd-cli" -> "OIDCC_ARGOCD_CLI".
func envVarName(clientID string) string {
	var b strings.Builder
	b.WriteString("OIDCC_")
	for _, r := range strings.ToUpper(clientID) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// clientToMap converts a client spec into the map structure expected under
// identity_providers.oidc.clients in the Authelia configuration. Secret clients
// reference their plaintext secret through the envhash shell function rendered
// by the Deployment's init container.
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
		m["client_secret"] = fmt.Sprintf("$(envhash %s)", c.envName)
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
func renderConfig(base string, clients []oidcClient, backend *autheliav1alpha1.AuthenticationBackendSpec, session *autheliav1alpha1.SessionSpec) (string, error) {
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

	if session != nil {
		if err := applySession(root, session); err != nil {
			return "", err
		}
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

// applySession merges the session configuration into the config, preserving
// sibling keys (e.g. session.redis). When no explicit cookies list is given but
// a domain is set, a sensible default cookie is generated from domain and
// hostname (defaulting to auth.<domain>).
func applySession(root map[string]any, s *autheliav1alpha1.SessionSpec) error {
	session := childMap(root, "session")

	setIf(session, "name", s.Name)
	setIf(session, "same_site", s.SameSite)
	setIf(session, "inactivity", s.Inactivity)
	setIf(session, "expiration", s.Expiration)
	setIf(session, "remember_me", s.RememberMe)

	switch {
	case s.Cookies != nil:
		var v any
		if err := json.Unmarshal(s.Cookies.Raw, &v); err != nil {
			return fmt.Errorf("decoding session.cookies: %w", err)
		}
		session["cookies"] = v
	case s.Domain != "":
		hostname := s.Hostname
		if hostname == "" {
			hostname = "auth." + s.Domain
		}
		cookie := map[string]any{
			"domain":       s.Domain,
			"authelia_url": "https://" + hostname,
		}
		setIf(cookie, "default_redirection_url", s.DefaultRedirectionURL)
		session["cookies"] = []any{cookie}
	}

	if err := setRaw(session, "redis", s.Redis); err != nil {
		return err
	}
	return nil
}

func fileBackendMap(f *autheliav1alpha1.FileAuthenticationBackend) (map[string]any, error) {
	m := map[string]any{
		"path": fileUsersPath(f.UsersSecret),
	}
	if f.Watch != nil {
		m["watch"] = *f.Watch
	}
	if f.Search != nil {
		search := map[string]any{}
		if f.Search.Email != nil {
			search["email"] = *f.Search.Email
		}
		if f.Search.CaseInsensitive != nil {
			search["case_insensitive"] = *f.Search.CaseInsensitive
		}
		if len(search) > 0 {
			m["search"] = search
		}
	}
	if err := setRaw(m, "password", f.Password); err != nil {
		return nil, err
	}
	return m, nil
}

func ldapBackendMap(l *autheliav1alpha1.LDAPAuthenticationBackend) (map[string]any, error) {
	m := map[string]any{
		"address": l.Address,
		"base_dn": l.BaseDN,
		"user":    l.User,
	}
	setIf(m, "implementation", l.Implementation)
	setIf(m, "additional_users_dn", l.AdditionalUsersDN)
	setIf(m, "users_filter", l.UsersFilter)
	setIf(m, "additional_groups_dn", l.AdditionalGroupsDN)
	setIf(m, "groups_filter", l.GroupsFilter)
	setIf(m, "group_search_mode", l.GroupSearchMode)
	setIf(m, "timeout", l.Timeout)
	if l.StartTLS != nil {
		m["start_tls"] = *l.StartTLS
	}
	if err := setRaw(m, "attributes", l.Attributes); err != nil {
		return nil, err
	}
	if err := setRaw(m, "tls", l.TLS); err != nil {
		return nil, err
	}
	return m, nil
}

func setIf(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// setRaw decodes a RawExtension passthrough field into the target map under key.
func setRaw(m map[string]any, key string, raw *runtime.RawExtension) error {
	if raw == nil || len(raw.Raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw.Raw, &v); err != nil {
		return fmt.Errorf("decoding %s: %w", key, err)
	}
	m[key] = v
	return nil
}
