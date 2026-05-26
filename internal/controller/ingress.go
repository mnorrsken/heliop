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
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

// ruleAnnotation marks an Ingress access_control rule. A single rule uses
// "heliop/rule"; multiple rules add a numeric (or any) suffix:
// "heliop/rule-0", "heliop/rule-1", ... Each value is a JSON object describing
// the rule (policy, subject, networks, resources, methods, ...). The domain is
// always derived from the Ingress hosts and cannot be set in the annotation.
const ruleAnnotation = "heliop/rule"

// validPolicies is the set of Authelia access_control policies.
var validPolicies = map[string]bool{
	"bypass":     true,
	"one_factor": true,
	"two_factor": true,
	"deny":       true,
}

// accessRule is a generated Authelia access_control rule plus sort metadata.
type accessRule struct {
	resourceLen int    // longest resource pattern; higher = more specific
	wildcard    bool   // any domain begins with "*"
	key         string // domain + namespace/name + annotation key, for stability
	rule        map[string]any
}

// isRuleAnnotation reports whether an annotation key declares an access rule.
func isRuleAnnotation(key string) bool {
	return key == ruleAnnotation || strings.HasPrefix(key, ruleAnnotation+"-")
}

// hasRuleAnnotations reports whether an Ingress opts into rule generation.
func hasRuleAnnotations(ing *networkingv1.Ingress) bool {
	for k := range ing.Annotations {
		if isRuleAnnotation(k) {
			return true
		}
	}
	return false
}

// ingressAccessRules builds the access_control rules declared on an Ingress.
// Invalid JSON or invalid/missing policy entries are skipped. The domain is
// forced to the Ingress hosts; any "domain" in the JSON is ignored.
func ingressAccessRules(ing *networkingv1.Ingress) []accessRule {
	domains := ingressDomains(ing)
	if len(domains) == 0 {
		return nil
	}

	keys := make([]string, 0)
	for k := range ing.Annotations {
		if isRuleAnnotation(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	rules := make([]accessRule, 0, len(keys))
	for _, k := range keys {
		var m map[string]any
		if err := json.Unmarshal([]byte(ing.Annotations[k]), &m); err != nil {
			continue
		}
		policy, _ := m["policy"].(string)
		if !validPolicies[policy] {
			continue
		}
		// Domain is operator-controlled; never taken from the annotation.
		m["domain"] = toIfaceSlice(domains)

		wildcard := false
		for _, d := range domains {
			if strings.HasPrefix(d, "*") {
				wildcard = true
				break
			}
		}
		rules = append(rules, accessRule{
			resourceLen: resourceLen(m),
			wildcard:    wildcard,
			key:         domains[0] + "/" + ing.Namespace + "/" + ing.Name + "/" + k,
			rule:        m,
		})
	}
	return rules
}

// ingressDomains returns the Ingress hosts (rule + TLS), de-duplicated and sorted.
func ingressDomains(ing *networkingv1.Ingress) []string {
	seen := map[string]bool{}
	var domains []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			domains = append(domains, h)
		}
	}
	for _, r := range ing.Spec.Rules {
		add(r.Host)
	}
	for _, t := range ing.Spec.TLS {
		for _, h := range t.Hosts {
			add(h)
		}
	}
	sort.Strings(domains)
	return domains
}

// resourceLen returns the length of the longest resource pattern in the rule,
// or 0 when none. Longer patterns are treated as more specific.
func resourceLen(rule map[string]any) int {
	max := 0
	switch v := rule["resources"].(type) {
	case []any:
		for _, r := range v {
			if s, ok := r.(string); ok && len(s) > max {
				max = len(s)
			}
		}
	case string:
		max = len(v)
	}
	return max
}

// sortAccessRules orders rules so the most specific match first (Authelia is
// first-match-wins): longer resource patterns, then exact hosts before
// wildcards, then a stable key.
func sortAccessRules(rules []accessRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if a.resourceLen != b.resourceLen {
			return a.resourceLen > b.resourceLen
		}
		if a.wildcard != b.wildcard {
			return !a.wildcard // exact (false) before wildcard (true)
		}
		return a.key < b.key
	})
}
