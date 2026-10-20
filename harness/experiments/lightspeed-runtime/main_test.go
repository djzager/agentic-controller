package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeDriverResultRequiresOneConsistentTypedResult(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "success", input: `{"success":true,"reason":"ok","usage":{"input_tokens":4,"output_tokens":2},"metadata":{"skill_file_present":true,"skill_file_read":true,"mcp_tools_admitted":["issue_nonce"],"tool_calls":["issue_nonce"]}}`},
		{name: "agent failure is a valid result", input: `{"success":false,"reason":"agent_failure"}`},
		{name: "tool inspection failure is a valid result", input: `{"success":false,"reason":"tool_output_inspection_failed"}`},
		{name: "skill not read is a valid result", input: `{"success":false,"reason":"skill_not_read"}`},
		{name: "timeout is a valid result", input: `{"success":false,"reason":"timeout"}`},
		{name: "missing success", input: `{"reason":"ok"}`, wantErr: true},
		{name: "contradiction", input: `{"success":true,"reason":"mcp_unavailable"}`, wantErr: true},
		{name: "unknown reason", input: `{"success":false,"reason":"secret-bearing-error"}`, wantErr: true},
		{name: "trailing output", input: `{"success":true,"reason":"ok"}\nextra`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeDriverResult([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeDriverResult() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestSafeMetadataKeepsOnlyBoundedIdentifiersAndEvidence(t *testing.T) {
	metadata := safeMetadata([]byte(`{"runtime":"deepagents","error_type":"provider_timeout","exception_class":"TimeoutError","skill_file_present":true,"skill_file_read":true,"mcp_tools_admitted":["issue_nonce"],"tool_calls":["issue_nonce","call included secret"],"event_types":["tool_call"],"assistant_text":"must not be retained"}`))
	if metadata == nil {
		t.Fatal("expected typed runtime metadata")
	}
	if metadata.Runtime != "deepagents" || metadata.ErrorType != "provider_timeout" || metadata.ExceptionClass != "TimeoutError" {
		t.Fatalf("safe identifiers were not retained: %+v", metadata)
	}
	if !validProofMetadata(metadata) {
		t.Fatalf("skill and MCP evidence was not retained: %+v", metadata)
	}
	if len(metadata.ToolCalls) != 1 || metadata.ToolCalls[0] != "issue_nonce" {
		t.Fatalf("unsafe tool-call labels were not filtered: %+v", metadata.ToolCalls)
	}
}

func TestSuccessfulRuntimeRequiresSkillAndMCPProof(t *testing.T) {
	metadata := &driverMetadata{
		SkillFilePresent: true,
		SkillFileRead:    true,
		MCPToolsAdmitted: []string{"issue_nonce"},
		ToolCalls:        []string{"issue_nonce"},
	}
	if !validProofMetadata(metadata) {
		t.Fatal("complete skill and MCP evidence should satisfy the prototype proof")
	}
	metadata.ToolCalls = nil
	if validProofMetadata(metadata) {
		t.Fatal("runtime success must not pass without evidence the required MCP tool ran")
	}
}

func TestRuntimeEnvironmentExcludesHubGitAndServiceAccountCredentials(t *testing.T) {
	t.Setenv("HUB_TOKEN", "hub-secret")
	t.Setenv("GIT_ASKPASS", "git-secret-helper")
	t.Setenv("KUBERNETES_SERVICE_HOST", "cluster-secret")
	t.Setenv("AWS_ACCESS_KEY_ID", "cloud-secret")
	cfg := &settings{APIKey: "runtime-key", Endpoint: "https://model.invalid", Model: defaultModel}
	env := strings.Join(runtimeEnvironment(cfg), "\n")
	for _, forbidden := range []string{"hub-secret", "GIT_ASKPASS", "git-secret-helper", "KUBERNETES_SERVICE_HOST", "cluster-secret", "AWS_ACCESS_KEY_ID", "cloud-secret"} {
		if strings.Contains(env, forbidden) {
			t.Errorf("child environment contains forbidden value %q", forbidden)
		}
	}
	if !strings.Contains(env, "ANTHROPIC_API_KEY=runtime-key") {
		t.Fatal("runtime API key was not included in child environment")
	}
	if strings.Contains(env, "AWS_ACCESS_KEY_ID") || strings.Contains(env, "KUBERNETES_SERVICE_HOST") {
		t.Fatal("cloud or Kubernetes credentials were included in child environment")
	}
}

func TestPrepareSkillRootCopiesMountedSkillIntoRuntimeRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(source, "probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("name: probe\n\nUse this skill.\n")
	if err := os.WriteFile(filepath.Join(source, "probe", "SKILL.md"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareSkillRoot(source, root); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "probe", "SKILL.md")
	info, err := os.Lstat(destination)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("skill was not copied as a regular file: info=%v err=%v", info, err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("copied skill bytes = %q, want %q", got, want)
	}
}

func TestPrepareSkillRootRejectsSourceSymlinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "SKILL.md"), []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(source, "probe")); err != nil {
		t.Fatal(err)
	}
	if err := prepareSkillRoot(source, root); err == nil {
		t.Fatal("expected symlinked source skill to be rejected")
	}
}
