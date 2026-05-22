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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

func testAuthelia() *autheliav1alpha1.Authelia {
	return &autheliav1alpha1.Authelia{
		ObjectMeta: metav1.ObjectMeta{Name: "authelia", Namespace: "auth"},
		Spec: autheliav1alpha1.AutheliaSpec{
			Hostname: "sso.example.com",
			Traefik:  &autheliav1alpha1.TraefikSpec{},
		},
	}
}

func TestBuildForwardAuthMiddleware(t *testing.T) {
	m := buildForwardAuthMiddleware(testAuthelia())

	if m.GetName() != "authelia-forwardauth" || m.GetNamespace() != "auth" {
		t.Fatalf("unexpected metadata: %s/%s", m.GetNamespace(), m.GetName())
	}
	fa := m.Object["spec"].(map[string]any)["forwardAuth"].(map[string]any)
	want := "http://authelia.auth.svc.cluster.local/api/authz/forward-auth"
	if fa["address"] != want {
		t.Errorf("address = %v, want %v", fa["address"], want)
	}
	if fa["trustForwardHeader"] != true {
		t.Errorf("trustForwardHeader = %v", fa["trustForwardHeader"])
	}
}

func TestBuildIngressRoute(t *testing.T) {
	ir := buildIngressRoute(testAuthelia())

	spec := ir.Object["spec"].(map[string]any)
	eps := spec["entryPoints"].([]any)
	if len(eps) != 1 || eps[0] != "websecure" {
		t.Errorf("entryPoints = %v, want [websecure]", eps)
	}
	route := spec["routes"].([]any)[0].(map[string]any)
	if route["match"] != "Host(`sso.example.com`)" {
		t.Errorf("match = %v", route["match"])
	}
	svc := route["services"].([]any)[0].(map[string]any)
	if svc["name"] != "authelia" || svc["port"] != int64(80) {
		t.Errorf("service = %v", svc)
	}
}

func TestBuildIngressRouteCustomEntryPoints(t *testing.T) {
	a := testAuthelia()
	a.Spec.Traefik.EntryPoints = []string{"web", "websecure"}
	spec := buildIngressRoute(a).Object["spec"].(map[string]any)
	if len(spec["entryPoints"].([]any)) != 2 {
		t.Errorf("entryPoints = %v", spec["entryPoints"])
	}
}
