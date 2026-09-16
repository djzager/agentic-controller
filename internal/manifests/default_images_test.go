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
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestDefaultRelatedImages(t *testing.T) {
	// Exact operator CSV environment variable names. Add new supported images
	// here in the same PR that introduces defaults using them.
	supported := map[string]bool{
		"RELATED_IMAGE_AGENT_JAVA":   true,
		"RELATED_IMAGE_AGENT_SKILLS": true,
	}

	// Match the default manifests copied by hack/sync-operator.sh.
	paths, err := filepath.Glob("../../config/defaults/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no default manifests found")
	}
	for _, path := range paths {
		if filepath.Base(path) == "kustomization.yaml" {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
			for {
				var obj unstructured.Unstructured
				if err := decoder.Decode(&obj); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					t.Fatalf("parse manifest: %v", err)
				}
				_, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "image")
				if err != nil {
					t.Fatalf("read spec.image: %v", err)
				}
				if !found {
					continue
				}
				binding := obj.GetAnnotations()["konveyor.io/related-image"]
				if !supported[binding] {
					t.Errorf("%s/%s has spec.image but konveyor.io/related-image is %q; expected a supported CSV environment variable name (add new supported images to this test's allowlist)", obj.GetKind(), obj.GetName(), binding)
				}
			}
		})
	}
}
