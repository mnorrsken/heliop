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

	out, err := renderConfig(base, clients)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}

	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("output is not valid yaml: %v", err)
	}

	idp := root["identity_providers"].(map[string]interface{})
	oidc := idp["oidc"].(map[string]interface{})
	if oidc["cors"] == nil {
		t.Error("existing oidc.cors config was dropped")
	}
	list, ok := oidc["clients"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 clients, got %#v", oidc["clients"])
	}

	// Sorted by client ID: argocd first.
	first := list[0].(map[string]interface{})
	if first["client_id"] != "argocd" {
		t.Errorf("expected argocd first, got %v", first["client_id"])
	}
	if first["client_secret"] != "$(envhash OIDCC_ARGOCD)" {
		t.Errorf("unexpected client_secret: %v", first["client_secret"])
	}

	second := list[1].(map[string]interface{})
	if second["public"] != true {
		t.Errorf("expected public client, got %#v", second)
	}
	if _, has := second["client_secret"]; has {
		t.Error("public client must not have a client_secret")
	}
}

func TestRenderConfigEmptyBase(t *testing.T) {
	out, err := renderConfig("", nil)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected empty map, got %q", out)
	}
}
