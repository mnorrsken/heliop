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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

const traefikAPIVersion = "traefik.io/v1alpha1"

func forwardAuthMiddlewareName(a *autheliav1alpha1.Authelia) string {
	return a.Name + "-forwardauth"
}

func traefikEntryPoints(t *autheliav1alpha1.TraefikSpec) []any {
	eps := t.EntryPoints
	if len(eps) == 0 {
		eps = []string{"websecure"}
	}
	out := make([]any, len(eps))
	for i, e := range eps {
		out[i] = e
	}
	return out
}

// buildForwardAuthMiddleware builds the Traefik forwardAuth Middleware that
// other IngressRoutes reference to require Authelia authentication.
func buildForwardAuthMiddleware(a *autheliav1alpha1.Authelia) *unstructured.Unstructured {
	m := &unstructured.Unstructured{}
	m.SetAPIVersion(traefikAPIVersion)
	m.SetKind("Middleware")
	m.SetName(forwardAuthMiddlewareName(a))
	m.SetNamespace(a.Namespace)
	m.SetLabels(labelsFor(a))
	m.Object["spec"] = map[string]any{
		"forwardAuth": map[string]any{
			"address":            fmt.Sprintf("http://%s.%s.svc.cluster.local/api/authz/forward-auth", a.Name, a.Namespace),
			"trustForwardHeader": true,
			"authResponseHeaders": []any{
				"Remote-User",
				"Remote-Groups",
				"Remote-Email",
				"Remote-Name",
			},
		},
	}
	return m
}

// buildIngressRoute builds the Traefik IngressRoute serving the Authelia portal
// at the configured hostname. The portal itself is not protected by the
// forwardAuth middleware.
func buildIngressRoute(a *autheliav1alpha1.Authelia) *unstructured.Unstructured {
	ir := &unstructured.Unstructured{}
	ir.SetAPIVersion(traefikAPIVersion)
	ir.SetKind("IngressRoute")
	ir.SetName(a.Name)
	ir.SetNamespace(a.Namespace)
	ir.SetLabels(labelsFor(a))
	ir.Object["spec"] = map[string]any{
		"entryPoints": traefikEntryPoints(a.Spec.Traefik),
		"routes": []any{
			map[string]any{
				"kind":  "Rule",
				"match": fmt.Sprintf("Host(`%s`)", a.Spec.Hostname),
				"services": []any{
					map[string]any{
						"kind":      "Service",
						"name":      a.Name,
						"namespace": a.Namespace,
						"port":      int64(80),
					},
				},
			},
		},
	}
	return ir
}
