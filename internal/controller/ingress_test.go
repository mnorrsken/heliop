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

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ingress(name string, annotations map[string]string, hosts ...string) *networkingv1.Ingress {
	rules := make([]networkingv1.IngressRule, 0, len(hosts))
	for _, h := range hosts {
		rules = append(rules, networkingv1.IngressRule{Host: h})
	}
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, Annotations: annotations},
		Spec:       networkingv1.IngressSpec{Rules: rules},
	}
}

func TestIngressAccessRulesOptIn(t *testing.T) {
	// No rule annotation -> nothing.
	if r := ingressAccessRules(ingress("x", nil, "app.example.com")); len(r) != 0 {
		t.Error("ingress without rule annotation should produce no rules")
	}
	// Invalid JSON -> skipped.
	if r := ingressAccessRules(ingress("x", map[string]string{ruleAnnotation: "{not json"}, "app.example.com")); len(r) != 0 {
		t.Error("invalid JSON should be skipped")
	}
	// Invalid policy -> skipped.
	if r := ingressAccessRules(ingress("x", map[string]string{ruleAnnotation: `{"policy":"nope"}`}, "app.example.com")); len(r) != 0 {
		t.Error("invalid policy should be skipped")
	}
	// Rule annotation but no host -> skipped (no domain).
	if r := ingressAccessRules(ingress("x", map[string]string{ruleAnnotation: `{"policy":"two_factor"}`})); len(r) != 0 {
		t.Error("ingress with no host should be skipped")
	}
}

func TestIngressAccessRuleDomainForced(t *testing.T) {
	// A domain in the JSON is overridden by the Ingress hosts.
	ing := ingress("grafana", map[string]string{
		ruleAnnotation: `{"policy":"two_factor","domain":["evil.example.com"],"subject":["group:admins"]}`,
	}, "grafana.example.com")

	rules := ingressAccessRules(ing)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	got := rules[0].rule["domain"].([]any)
	if len(got) != 1 || got[0] != "grafana.example.com" {
		t.Errorf("domain = %v, want forced from hosts", got)
	}
	if rules[0].rule["policy"] != "two_factor" {
		t.Errorf("policy = %v", rules[0].rule["policy"])
	}
	if s := rules[0].rule["subject"].([]any); len(s) != 1 || s[0] != "group:admins" {
		t.Errorf("subject passthrough = %v", rules[0].rule["subject"])
	}
}

func TestIngressMultipleRules(t *testing.T) {
	ing := ingress("app", map[string]string{
		ruleAnnotation + "-0": `{"policy":"bypass","resources":["^/health$"]}`,
		ruleAnnotation + "-1": `{"policy":"two_factor"}`,
	}, "app.example.com")

	rules := ingressAccessRules(ing)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestIngressDomainsTLSDedup(t *testing.T) {
	ing := ingress("y", map[string]string{ruleAnnotation: `{"policy":"one_factor"}`}, "app.example.com")
	ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{"app.example.com", "alt.example.com"}}}
	rules := ingressAccessRules(ing)
	if got := rules[0].rule["domain"].([]any); len(got) != 2 {
		t.Errorf("expected 2 deduped domains, got %v", got)
	}
}

func TestSortAccessRulesResourceLength(t *testing.T) {
	// Longer resource patterns first, then exact before wildcard, then key.
	rules := []accessRule{
		{resourceLen: 0, wildcard: false, key: "a.example.com/default/a/rule"},
		{resourceLen: 12, wildcard: false, key: "a.example.com/default/a/rule-1"},
		{resourceLen: 5, wildcard: false, key: "a.example.com/default/a/rule-2"},
		{resourceLen: 0, wildcard: true, key: "z.example.com/default/z/rule"},
	}
	sortAccessRules(rules)

	want := []int{12, 5, 0, 0}
	for i, w := range want {
		if rules[i].resourceLen != w {
			t.Errorf("rules[%d].resourceLen = %d, want %d", i, rules[i].resourceLen, w)
		}
	}
	// Among the two resourceLen==0 rules, exact host sorts before wildcard.
	if rules[2].wildcard || !rules[3].wildcard {
		t.Errorf("exact host should precede wildcard: %#v", rules)
	}
}

func TestResourceLen(t *testing.T) {
	if got := resourceLen(map[string]any{"resources": []any{"^/a$", "^/admin/.*$"}}); got != 11 {
		t.Errorf("resourceLen = %d, want 11 (longest pattern)", got)
	}
	if got := resourceLen(map[string]any{}); got != 0 {
		t.Errorf("resourceLen with no resources = %d, want 0", got)
	}
}
