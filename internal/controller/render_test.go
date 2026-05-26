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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// settings builds an AutheliaSettings whose additionalConfig is the given JSON.
func settings(additionalConfigJSON string) autheliav1alpha1.AutheliaSettings {
	s := autheliav1alpha1.AutheliaSettings{}
	if additionalConfigJSON != "" {
		s.AdditionalConfig = &runtime.RawExtension{Raw: []byte(additionalConfigJSON)}
	}
	return s
}

func mustRender(t *testing.T, s autheliav1alpha1.AutheliaSettings, clients []oidcClient, hostname string) map[string]any {
	t.Helper()
	out, err := renderConfig(s, clients, nil, hostname)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("output is not valid yaml: %v", err)
	}
	return root
}

func TestRenderConfigInjectsClients(t *testing.T) {
	s := settings(`{"log":{"level":"debug"},"identity_providers":{"oidc":{"cors":{"allowed_origins_from_client_redirect_uris":true}}}}`)
	clients := []oidcClient{
		{
			spec: autheliav1alpha1.AutheliaOAuthClientSpec{
				ClientID:            "argocd",
				ClientName:          "Argo CD",
				AuthorizationPolicy: "one_factor",
				RedirectURIs:        []string{"https://argocd.example.com/auth/callback"},
			},
			secretDigest: "$pbkdf2-sha512$310000$abc$def",
		},
		{
			spec: autheliav1alpha1.AutheliaOAuthClientSpec{
				ClientID:     "cli",
				Public:       true,
				RedirectURIs: []string{"http://localhost:8085/auth/callback"},
			},
		},
	}

	root := mustRender(t, s, clients, "")

	oidc := root["identity_providers"].(map[string]any)["oidc"].(map[string]any)
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
	if first["client_secret"] != "$pbkdf2-sha512$310000$abc$def" {
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

func TestRenderConfigFileBackend(t *testing.T) {
	s := settings(`{"authentication_backend":{"file":{"search":{"email":true},"path":"/ignored"}}}`)
	s.FileUsersSecret = &autheliav1alpha1.SecretKeyRef{Name: "users", Key: "users_database.yml"}

	root := mustRender(t, s, nil, "")

	file := root["authentication_backend"].(map[string]any)["file"].(map[string]any)
	want := fileBackendMountPath + "/users_database.yml"
	if file["path"] != want {
		t.Errorf("file.path = %v, want %v (operator-controlled)", file["path"], want)
	}
	if file["search"].(map[string]any)["email"] != true {
		t.Errorf("verbatim search config dropped: %#v", file["search"])
	}
}

func TestAutheliaFileEnvSecrets(t *testing.T) {
	s := autheliav1alpha1.AutheliaSettings{
		Secrets: []autheliav1alpha1.AutheliaSecret{
			{Name: "AUTHELIA_NOTIFIER_SMTP_PASSWORD_FILE", Secret: &autheliav1alpha1.SecretKeyRef{Name: "smtp", Key: "password"}},
			{Name: "AUTHELIA_SESSION_REDIS_PASSWORD", Secret: &autheliav1alpha1.SecretKeyRef{Name: "redis", Key: "redis-password"}},
		},
	}
	env := autheliaFileEnv(s, false)

	byName := map[string]corev1.EnvVar{}
	for _, e := range env {
		byName[e.Name] = e
	}

	file := byName["AUTHELIA_NOTIFIER_SMTP_PASSWORD_FILE"]
	if want := secretMountPath("AUTHELIA_NOTIFIER_SMTP_PASSWORD_FILE") + "/password"; file.Value != want {
		t.Errorf("_FILE var = %q, want path %q", file.Value, want)
	}

	direct := byName["AUTHELIA_SESSION_REDIS_PASSWORD"]
	if direct.ValueFrom == nil || direct.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("non-_FILE var should use valueFrom secretKeyRef: %#v", direct)
	}
	if direct.ValueFrom.SecretKeyRef.Name != "redis" || direct.ValueFrom.SecretKeyRef.Key != "redis-password" {
		t.Errorf("unexpected secretKeyRef: %#v", direct.ValueFrom.SecretKeyRef)
	}
}

func TestRenderConfigSessionDefaultCookie(t *testing.T) {
	s := settings(`{"session":{"expiration":"2 hours","redis":{"host":"redis","database_index":2}}}`)

	root := mustRender(t, s, nil, "sso.example.com")

	sess := root["session"].(map[string]any)
	if sess["expiration"] != "2 hours" {
		t.Errorf("expiration = %v", sess["expiration"])
	}
	redis := sess["redis"].(map[string]any)
	if redis["host"] != "redis" || redis["database_index"] != float64(2) {
		t.Errorf("redis not passed through verbatim: %#v", redis)
	}
	cookie := sess["cookies"].([]any)[0].(map[string]any)
	if cookie["domain"] != "example.com" {
		t.Errorf("domain = %v (want parent domain of hostname)", cookie["domain"])
	}
	if cookie["authelia_url"] != "https://sso.example.com" {
		t.Errorf("authelia_url = %v", cookie["authelia_url"])
	}
}

func TestRenderConfigSessionDefaultCookieWithoutConfig(t *testing.T) {
	// No additionalConfig at all, just a hostname -> default cookie generated.
	root := mustRender(t, autheliav1alpha1.AutheliaSettings{}, nil, "auth.example.com")
	cookie := root["session"].(map[string]any)["cookies"].([]any)[0].(map[string]any)
	if cookie["domain"] != "example.com" || cookie["authelia_url"] != "https://auth.example.com" {
		t.Errorf("unexpected default cookie: %#v", cookie)
	}
}

func TestRenderConfigSessionExplicitCookiesNotOverridden(t *testing.T) {
	s := settings(`{"session":{"cookies":[{"domain":"corp.internal","authelia_url":"https://login.corp.internal"}]}}`)
	root := mustRender(t, s, nil, "sso.example.com")
	cookies := root["session"].(map[string]any)["cookies"].([]any)
	if len(cookies) != 1 || cookies[0].(map[string]any)["domain"] != "corp.internal" {
		t.Errorf("explicit cookies were overridden: %#v", cookies)
	}
}

func TestRenderConfigPrependsIngressRules(t *testing.T) {
	s := settings(`{"access_control":{"default_policy":"deny","rules":[{"domain":["*.example.com"],"policy":"two_factor"}]}}`)
	ingressRules := []map[string]any{
		{"domain": []any{"grafana.example.com"}, "policy": "one_factor"},
	}

	out, err := renderConfig(s, nil, ingressRules, "")
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	ac := root["access_control"].(map[string]any)
	if ac["default_policy"] != "deny" {
		t.Errorf("default_policy lost: %v", ac["default_policy"])
	}
	rules := ac["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	first := rules[0].(map[string]any)
	if first["domain"].([]any)[0] != "grafana.example.com" {
		t.Errorf("generated rule not prepended: %#v", rules[0])
	}
	last := rules[1].(map[string]any)
	if last["domain"].([]any)[0] != "*.example.com" {
		t.Errorf("static rule not preserved after generated: %#v", rules[1])
	}
}

func TestRenderConfigEmpty(t *testing.T) {
	out, err := renderConfig(autheliav1alpha1.AutheliaSettings{}, nil, nil, "")
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected empty map, got %q", out)
	}
}
