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
	"fmt"
	"sort"
	"strings"

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

// renderConfig parses the user-supplied configuration, injects the OIDC clients
// under identity_providers.oidc.clients, and returns the resulting YAML.
func renderConfig(base string, clients []oidcClient) (string, error) {
	root := map[string]any{}
	if strings.TrimSpace(base) != "" {
		if err := yaml.Unmarshal([]byte(base), &root); err != nil {
			return "", fmt.Errorf("parsing config: %w", err)
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
