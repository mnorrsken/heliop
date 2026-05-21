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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

func TestEnvVarName(t *testing.T) {
	cases := map[string]string{
		"argocd":     "OIDCC_ARGOCD",
		"argocd-cli": "OIDCC_ARGOCD_CLI",
		"my.app/1":   "OIDCC_MY_APP_1",
	}
	for in, want := range cases {
		if got := envVarName(in); got != want {
			t.Errorf("envVarName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderConfigInjectsClients(t *testing.T) {
	base := `log:
  level: debug
identity_providers:
  oidc:
    cors:
      allowed_origins_from_client_redirect_uris: true
`
	clients := []oidcClient{
		{
			spec: autheliav1alpha1.AutheliaOAuthClientSpec{
				ClientID:            "argocd",
				ClientName:          "Argo CD",
				AuthorizationPolicy: "one_factor",
				RedirectURIs:        []string{"https://argocd.snosr.se/auth/callback"},
			},
			envName: "OIDCC_ARGOCD",
		},
		{
			spec: autheliav1alpha1.AutheliaOAuthClientSpec{
				ClientID:     "cli",
				Public:       true,
				RedirectURIs: []string{"http://localhost:8085/auth/callback"},
			},
			envName: "OIDCC_CLI",
		},
	}

	out, err := renderConfig(base, clients, nil, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("output is not valid yaml: %v", err)
	}

	idp := root["identity_providers"].(map[string]any)
	oidc := idp["oidc"].(map[string]any)
	if oidc["cors"] == nil {
		t.Error("existing oidc.cors config was dropped")
	}
	list, ok := oidc["clients"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 clients, got %#v", oidc["clients"])
	}

	// Sorted by client ID: argocd first.
	first := list[0].(map[string]any)
	if first["client_id"] != "argocd" {
		t.Errorf("expected argocd first, got %v", first["client_id"])
	}
	if first["client_secret"] != "$(envhash OIDCC_ARGOCD)" {
		t.Errorf("unexpected client_secret: %v", first["client_secret"])
	}

	second := list[1].(map[string]any)
	if second["public"] != true {
		t.Errorf("expected public client, got %#v", second)
	}
	if _, has := second["client_secret"]; has {
		t.Error("public client must not have a client_secret")
	}
}

func TestRenderConfigLDAPBackend(t *testing.T) {
	base := "authentication_backend:\n  password_reset:\n    disable: true\n  ldap:\n    address: 'ldap://old'\n"
	startTLS := true
	backend := &autheliav1alpha1.AuthenticationBackendSpec{
		LDAP: &autheliav1alpha1.LDAPAuthenticationBackend{
			Address:        "ldap://lldap:3890",
			BaseDN:         "DC=example,DC=com",
			User:           "UID=bind,OU=people,DC=example,DC=com",
			Implementation: "lldap",
			StartTLS:       &startTLS,
			PasswordSecret: autheliav1alpha1.SecretKeyRef{Name: "ldap-creds", Key: "password"},
		},
	}

	out, err := renderConfig(base, nil, backend, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	ab := root["authentication_backend"].(map[string]any)
	if ab["password_reset"] == nil {
		t.Error("sibling password_reset was dropped")
	}
	ldap := ab["ldap"].(map[string]any)
	if ldap["address"] != "ldap://lldap:3890" {
		t.Errorf("address not overridden: %v", ldap["address"])
	}
	if ldap["base_dn"] != "DC=example,DC=com" {
		t.Errorf("base_dn = %v", ldap["base_dn"])
	}
	if ldap["start_tls"] != true {
		t.Errorf("start_tls = %v", ldap["start_tls"])
	}
	if _, has := ldap["password"]; has {
		t.Error("ldap password must not be rendered into config (provided via env)")
	}
}

func TestRenderConfigFileBackend(t *testing.T) {
	backend := &autheliav1alpha1.AuthenticationBackendSpec{
		File: &autheliav1alpha1.FileAuthenticationBackend{
			UsersSecret: autheliav1alpha1.SecretKeyRef{Name: "users", Key: "users_database.yml"},
		},
	}

	out, err := renderConfig("", nil, backend, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	file := root["authentication_backend"].(map[string]any)["file"].(map[string]any)
	want := fileBackendMountPath + "/users_database.yml"
	if file["path"] != want {
		t.Errorf("file.path = %v, want %v", file["path"], want)
	}
}

func TestRenderConfigSessionGeneratesCookie(t *testing.T) {
	base := "session:\n  redis:\n    host: redis\n"
	session := &autheliav1alpha1.SessionSpec{
		Domain:     "example.com",
		Expiration: "2 hours",
	}

	out, err := renderConfig(base, nil, nil, session)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	s := root["session"].(map[string]any)
	if s["redis"] == nil {
		t.Error("sibling session.redis was dropped")
	}
	if s["expiration"] != "2 hours" {
		t.Errorf("expiration = %v", s["expiration"])
	}
	cookies := s["cookies"].([]any)
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	cookie := cookies[0].(map[string]any)
	if cookie["domain"] != "example.com" {
		t.Errorf("domain = %v", cookie["domain"])
	}
	if cookie["authelia_url"] != "https://auth.example.com" {
		t.Errorf("authelia_url = %v (want default auth.<domain>)", cookie["authelia_url"])
	}
}

func TestRenderConfigSessionHostnameOverride(t *testing.T) {
	session := &autheliav1alpha1.SessionSpec{Domain: "example.com", Hostname: "sso.example.com"}
	out, err := renderConfig("", nil, nil, session)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	cookie := root["session"].(map[string]any)["cookies"].([]any)[0].(map[string]any)
	if cookie["authelia_url"] != "https://sso.example.com" {
		t.Errorf("authelia_url = %v", cookie["authelia_url"])
	}
}

func TestRenderConfigSessionRedisVerbatim(t *testing.T) {
	session := &autheliav1alpha1.SessionSpec{
		Domain: "example.com",
		Redis:  &runtime.RawExtension{Raw: []byte(`{"host":"redis","port":6379,"database_index":2}`)},
	}
	out, err := renderConfig("", nil, nil, session)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	redis := root["session"].(map[string]any)["redis"].(map[string]any)
	if redis["host"] != "redis" {
		t.Errorf("host = %v", redis["host"])
	}
	if redis["database_index"] != float64(2) {
		t.Errorf("database_index = %v", redis["database_index"])
	}
}

func TestRenderConfigEmptyBase(t *testing.T) {
	out, err := renderConfig("", nil, nil, nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected empty map, got %q", out)
	}
}
