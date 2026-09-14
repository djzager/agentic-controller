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

package manifests

import (
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"
)

// The operator copies these source files verbatim, without Kustomize transforms.
// Hub filters its Agent and AgentWorkflow catalogs by konveyor.io/managed=true.
func TestDefaultCatalogLabels(t *testing.T) {
	paths, err := filepath.Glob("../../config/defaults/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalogResources := 0
	selector := labels.SelectorFromSet(labels.Set{"konveyor.io/managed": "true"})
	for _, path := range paths {
		if filepath.Base(path) == "kustomization.yaml" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var resource struct {
			metav1.TypeMeta `json:",inline"`
			Metadata        metav1.ObjectMeta `json:"metadata"`
		}
		if err := yaml.Unmarshal(raw, &resource); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if resource.Kind != "Agent" && resource.Kind != "AgentWorkflow" {
			continue
		}
		catalogResources++
		t.Run(resource.Kind+"/"+resource.Metadata.Name, func(t *testing.T) {
			if !selector.Matches(labels.Set(resource.Metadata.Labels)) {
				t.Errorf("%s is hidden from the Hub catalog: requires konveyor.io/managed=\"true\"", path)
			}
			if resource.Metadata.Labels["app.kubernetes.io/managed-by"] != "agentic-controller-defaults" {
				t.Errorf("%s must preserve the operator's pruning label", path)
			}
		})
	}
	if catalogResources == 0 {
		t.Fatal("no default Agents or AgentWorkflows checked")
	}
}
