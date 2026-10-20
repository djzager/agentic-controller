// Command lightspeed-runtime is an isolated one-shot runtime integration
// spike. It deliberately does not alter the production migration-harness.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/konveyor/agentic-controller/api/skill"
	"github.com/konveyor/migration-harness/internal/git"
	"github.com/konveyor/migration-harness/internal/hub"
	"github.com/konveyor/migration-harness/internal/params"
	"github.com/konveyor/migration-harness/internal/prompt"
)

const (
	defaultRuntimeRoot = "/workspace"
	defaultSkillsDir   = "/opt/runtime-spike/skills"
	defaultDriver      = "/opt/runtime-spike/driver.py"
	defaultModel       = "rits/zai-org/glm-5-3"
	defaultMaxTurns    = 24
	childTimeout       = 10 * time.Minute
	pushTimeout        = 25 * time.Second
	maxDriverOutput    = 1 << 20
)

type driverInput struct {
	RunID             string `json:"run_id"`
	RuntimeRoot       string `json:"runtime_root"`
	RepoRelativePath  string `json:"repo_relative_path"`
	Prompt            string `json:"prompt"`
	SystemPrompt      string `json:"system_prompt"`
	MaxTurns          int    `json:"max_turns"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	RequiredMCPServer string `json:"required_mcp_server"`
	RequiredMCPTool   string `json:"required_mcp_tool"`
	MCPURL            string `json:"mcp_url"`
}

type driverUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type driverResult struct {
	Success  *bool           `json:"success"`
	Reason   string          `json:"reason"`
	Usage    *driverUsage    `json:"usage"`
	Metadata json.RawMessage `json:"metadata"`
}

type driverMetadata struct {
	Runtime            string   `json:"runtime,omitempty"`
	ErrorType          string   `json:"error_type,omitempty"`
	ExceptionClass     string   `json:"exception_class,omitempty"`
	SkillFilePresent   bool     `json:"skill_file_present"`
	MCPToolsAdmitted   []string `json:"mcp_tools_admitted,omitempty"`
	ToolCalls          []string `json:"tool_calls,omitempty"`
	EventTypes         []string `json:"event_types,omitempty"`
	SkillFileRead      bool     `json:"skill_file_read"`
	ACPAdapterVersion  string   `json:"acp_adapter_version,omitempty"`
	ACPSDKVersion      string   `json:"acp_sdk_version,omitempty"`
	HistoryPersistence string   `json:"history_persistence,omitempty"`
	Interaction        string   `json:"interaction,omitempty"`
	UsageObservable    *bool    `json:"usage_observable,omitempty"`
	UsageComplete      *bool    `json:"usage_complete,omitempty"`
	UsageScope         string   `json:"usage_scope,omitempty"`
}

type terminationData struct {
	ExitCode       int             `json:"exitCode"`
	Outcome        string          `json:"outcome"`
	StopReason     string          `json:"stopReason,omitempty"`
	RuntimeSuccess *bool           `json:"runtimeSuccess,omitempty"`
	Usage          *driverUsage    `json:"usage,omitempty"`
	Metadata       *driverMetadata `json:"metadata,omitempty"`
	Pushed         bool            `json:"pushed,omitempty"`
	BaseSHA        string          `json:"baseSHA,omitempty"`
	HeadSHA        string          `json:"headSHA,omitempty"`
}

type settings struct {
	HubBaseURL     string
	HubToken       string
	HubTokenID     uint
	AppID          uint
	TargetBranch   string
	APIKey         string
	Endpoint       string
	Model          string
	MCPURL         string
	RunID          string
	RepoDir        string
	RuntimeRoot    string
	SkillsDir      string
	DriverPath     string
	Params         params.File
	cleanupRuntime func()
	runtimeNotice  func(string)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	term := execute(ctx)
	writeTerminationLog(term)
	if term.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "lightspeed runtime spike failed: %s\n", term.StopReason)
	}
	os.Exit(term.ExitCode)
}

func execute(parent context.Context) (term terminationData) {
	term = terminationData{ExitCode: 1, Outcome: "failed", StopReason: "configuration"}
	hubBaseURL, hubToken, hubTokenID, err := loadHubCredentials()
	if err != nil {
		return term
	}
	hubClient := hub.NewClient(hubBaseURL, hubToken)
	defer func() {
		if err := hubClient.RevokeToken(hubTokenID); err != nil {
			fmt.Fprintln(os.Stderr, "Hub token revocation failed")
		}
	}()
	cfg, err := loadSettings(hubBaseURL, hubToken, hubTokenID)
	if err != nil {
		return term
	}
	defer func() {
		if cfg.cleanupRuntime != nil {
			cfg.cleanupRuntime()
		}
	}()
	term.StopReason = "hub_resolution"
	creds, err := resolveCredentials(cfg, hubClient)
	if err != nil || creds.Branch == cfg.TargetBranch {
		return term
	}
	creds.Branch = cfg.TargetBranch
	term.StopReason = "workspace_setup"

	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	repo, err := git.Clone(ctx, creds, cfg.RepoDir)
	if err != nil {
		return term
	}
	if err := git.ConfigureAuthor(repo, envOr("KONVEYOR_GIT_AUTHOR_NAME", "migration-agent"), envOr("KONVEYOR_GIT_AUTHOR_EMAIL", "migration-agent@konveyor.io")); err != nil {
		return term
	}
	if err := git.StripCredentials(repo); err != nil {
		return term
	}
	if err := git.CheckoutBranch(repo, creds.Branch); err != nil {
		return term
	}
	baseSHA, err := git.HeadSHA(repo)
	if err != nil || baseSHA == "" {
		return term
	}
	term.BaseSHA = baseSHA
	if err := prepareSkillRoot(cfg.SkillsDir, cfg.RuntimeRoot); err != nil {
		return term
	}

	// The child is deliberately given only runtime credentials. Hub credentials
	// have been removed from the parent environment and Git credentials remain
	// only in the Go object used for clone/push.
	hub.ClearEnv()
	term.StopReason = "runtime"
	input := buildDriverInput(cfg)
	runtimeResult, runtimeErr := runDriver(ctx, cfg, input)
	if runtimeErr != nil {
		runtimeResult = nil
	}
	if runtimeResult != nil && runtimeResult.Usage != nil {
		term.Usage = runtimeResult.Usage
	}
	if runtimeResult != nil {
		term.RuntimeSuccess = runtimeResult.Success
		term.Metadata = safeMetadata(runtimeResult.Metadata)
	}

	term.StopReason = "runtime_result"
	if runtimeErr != nil {
		term.StopReason = "runtime_error"
	} else if runtimeResult == nil || runtimeResult.Success == nil {
		term.StopReason = "invalid_runtime_result"
	} else if !*runtimeResult.Success {
		term.StopReason = safeFailureReason(runtimeResult.Reason)
	} else if !validProofMetadata(term.Metadata) {
		term.StopReason = "runtime_proof_incomplete"
	} else {
		term.StopReason = "no_commit"
	}

	worktree, wtErr := repo.Worktree()
	if wtErr != nil {
		term.StopReason = "worktree_status"
		return term
	}
	status, statusErr := worktree.Status()
	if statusErr != nil {
		term.StopReason = "worktree_status"
		return term
	}
	headSHA, headErr := git.HeadSHA(repo)
	if headErr != nil {
		term.StopReason = "head_status"
		return term
	}
	term.HeadSHA = headSHA
	if statusErr == nil && !status.IsClean() && runtimeResult != nil && runtimeResult.Success != nil && *runtimeResult.Success {
		term.StopReason = "uncommitted_changes"
		return term
	}

	pushCtx, pushCancel := context.WithTimeout(context.Background(), pushTimeout)
	defer pushCancel()
	if cfg.runtimeNotice != nil {
		cfg.runtimeNotice("Runtime finished; harness is pushing committed output")
	}
	pushed, pushErr := git.Push(pushCtx, creds, repo, creds.Branch, baseSHA)
	term.Pushed = pushed
	if pushErr != nil {
		term.StopReason = "final_push"
		return term
	}
	if runtimeErr != nil || runtimeResult == nil || runtimeResult.Success == nil || !*runtimeResult.Success || !validProofMetadata(term.Metadata) {
		return term
	}
	if headSHA == baseSHA || !pushed {
		term.StopReason = "no_commit"
		return term
	}

	term.ExitCode = 0
	term.Outcome = "succeeded"
	term.StopReason = "succeeded"
	if cfg.runtimeNotice != nil {
		cfg.runtimeNotice("Harness final push succeeded; invocation complete")
	}
	return term
}

func loadHubCredentials() (string, string, uint, error) {
	baseURL, token, tokenID := os.Getenv("HUB_BASE_URL"), os.Getenv("HUB_TOKEN"), os.Getenv("HUB_TOKEN_ID")
	if baseURL == "" || token == "" || tokenID == "" {
		return "", "", 0, errors.New("Hub credentials are required")
	}
	id64, err := strconv.ParseUint(tokenID, 10, 64)
	if err != nil || uint64(uint(id64)) != id64 {
		return "", "", 0, errors.New("HUB_TOKEN_ID must be a numeric Hub token ID")
	}
	return baseURL, token, uint(id64), nil
}

func loadSettings(hubBaseURL, hubToken string, hubTokenID uint) (*settings, error) {
	for _, pair := range [][2]string{
		{"APP_ID", os.Getenv("APP_ID")},
		{"TARGET_BRANCH", os.Getenv("TARGET_BRANCH")},
		{"ANTHROPIC_API_KEY", os.Getenv("ANTHROPIC_API_KEY")},
		{"SPIKE_MCP_URL", os.Getenv("SPIKE_MCP_URL")},
		{"SPIKE_RUN_ID", os.Getenv("SPIKE_RUN_ID")},
	} {
		if strings.TrimSpace(pair[1]) == "" {
			return nil, fmt.Errorf("%s is required", pair[0])
		}
	}
	if os.Getenv("KONVEYOR_WORKFLOW_STAGE") != "" || os.Getenv("KONVEYOR_WORKFLOW_STAGE_COUNT") != "" || os.Getenv("KONVEYOR_WORKFLOW_GUIDE") != "" {
		return nil, errors.New("workflow runs are unsupported by this spike")
	}
	endpoint := firstNonEmpty(os.Getenv("SPIKE_LLM_ENDPOINT"), os.Getenv("KONVEYOR_LLM_ENDPOINT"), os.Getenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT"))
	if endpoint == "" {
		return nil, errors.New("SPIKE_LLM_ENDPOINT is required")
	}
	if _, err := validateHTTPURL(os.Getenv("SPIKE_MCP_URL")); err != nil {
		return nil, errors.New("SPIKE_MCP_URL must be an HTTP(S) URL")
	}
	appID, err := hub.ParseAppID(os.Getenv("APP_ID"))
	if err != nil {
		return nil, errors.New("APP_ID must be numeric")
	}
	if err := rejectUnsupportedExecution(); err != nil {
		return nil, err
	}
	paramsFile, err := params.Load(paramsFilePath())
	if err != nil {
		return nil, errors.New("params file is invalid")
	}
	if strings.EqualFold(paramsFile.Execution.Mode, "approve") || paramsFile.Execution.AskUser || paramsFile.Execution.MaxCost != "" {
		return nil, errors.New("approval, askUser, and cost limits are unsupported by this spike")
	}
	model := envOr("SPIKE_MODEL", defaultModel)
	if model != defaultModel {
		return nil, errors.New("this spike is pinned to its configured test model")
	}
	runtimeRoot := envOr("SPIKE_RUNTIME_ROOT", defaultRuntimeRoot)
	repoDir := filepath.Join(runtimeRoot, "repo")
	return &settings{
		HubBaseURL: hubBaseURL, HubToken: hubToken, HubTokenID: hubTokenID,
		AppID: appID, TargetBranch: os.Getenv("TARGET_BRANCH"), APIKey: os.Getenv("ANTHROPIC_API_KEY"),
		Endpoint: endpoint, Model: model, MCPURL: os.Getenv("SPIKE_MCP_URL"),
		RunID: os.Getenv("SPIKE_RUN_ID"), RepoDir: repoDir, RuntimeRoot: runtimeRoot,
		SkillsDir: envOr("SPIKE_SKILLS_DIR", defaultSkillsDir), DriverPath: envOr("SPIKE_DRIVER", defaultDriver), Params: paramsFile,
	}, nil
}

func rejectUnsupportedExecution() error {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("KONVEYOR_EXECUTION_MODE")), "approve") {
		return errors.New("approval mode is unsupported by this spike")
	}
	return nil
}

func paramsFilePath() string {
	if p := os.Getenv("HARNESS_PARAMS_FILE"); p != "" {
		return p
	}
	return params.Path
}

func resolveCredentials(cfg *settings, client *hub.Client) (*git.Credentials, error) {
	app, err := client.FetchApp(cfg.AppID)
	if err != nil || app == nil || app.Repository == nil || hub.ValidateSourceRepository(app.Repository) != nil {
		return nil, errors.New("application source is unavailable")
	}
	identity, err := client.FetchGitCreds(cfg.AppID)
	if err != nil {
		return nil, errors.New("source credentials are unavailable")
	}
	creds := &git.Credentials{RepoURL: app.Repository.URL, Branch: app.Repository.Branch}
	if identity != nil {
		creds.Username = identity.User
		creds.Token = identity.Password
		if creds.Username == "" {
			creds.Username = "x-access-token"
		}
	}
	return creds, nil
}

func buildDriverInput(cfg *settings) driverInput {
	manifest, _ := skill.ReadManifest(cfg.SkillsDir)
	rules, _ := skill.RuleContent(cfg.SkillsDir, manifest)
	task := "Use the probe skill to complete the requested repository task. Work only in repo/, use the required MCP tool, make the requested change, and commit it."
	fullPrompt := prompt.Build(prompt.Layers{
		AgentPrompt:   "The Git checkout is in repo/ under the runtime root. Run project commands from repo/ and modify only files under repo/. The skills directory is outside the checkout and must not be changed.",
		Rules:         rules,
		WorkflowGuide: os.Getenv("KONVEYOR_WORKFLOW_GUIDE"),
		Parameters:    params.RenderSection(cfg.Params),
		StageTask:     task + "\n\n" + os.Getenv("KONVEYOR_INSTRUCTIONS"),
	})
	return driverInput{
		RunID: cfg.RunID, RuntimeRoot: cfg.RuntimeRoot, RepoRelativePath: "repo",
		Prompt:       "Use the probe skill to make the proof commit in repo for run ID " + cfg.RunID + ".",
		SystemPrompt: fullPrompt, MaxTurns: maxTurns(cfg.Params), TimeoutSeconds: int(childTimeout.Seconds()),
		RequiredMCPServer: "nonce-fixture", RequiredMCPTool: "issue_nonce", MCPURL: cfg.MCPURL,
	}
}

func runDriver(parent context.Context, cfg *settings, input driverInput) (*driverResult, error) {
	if os.Getenv("SPIKE_ACP") == "on" {
		return runACPDriver(parent, cfg, input)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("cannot encode runtime request")
	}
	ctx, cancel := context.WithTimeout(parent, childTimeout)
	defer cancel()
	cmd := execCommandContext(ctx, "python3", cfg.DriverPath, "--runtime-root", cfg.RuntimeRoot, "--repo-relative-path", "repo")
	cmd.Stdin = bytes.NewReader(inputJSON)
	output := &limitedBuffer{limit: maxDriverOutput}
	cmd.Stdout = output
	cmd.Stderr = os.Stderr
	cmd.Env = runtimeEnvironment(cfg)
	if err := cmd.Run(); err != nil || output.truncated {
		return nil, errors.New("runtime process failed")
	}
	return decodeDriverResult(output.Bytes())
}

func runtimeEnvironment(cfg *settings) []string {
	allow := map[string]string{
		"PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME"), "TMPDIR": os.Getenv("TMPDIR"),
		"SSL_CERT_FILE": os.Getenv("SSL_CERT_FILE"), "SSL_CERT_DIR": os.Getenv("SSL_CERT_DIR"),
		"REQUESTS_CA_BUNDLE": os.Getenv("REQUESTS_CA_BUNDLE"), "CURL_CA_BUNDLE": os.Getenv("CURL_CA_BUNDLE"),
		"LANG": os.Getenv("LANG"), "LC_ALL": os.Getenv("LC_ALL"),
		"ANTHROPIC_API_KEY": cfg.APIKey, "LIGHTSPEED_PROVIDER": "anthropic",
		"LIGHTSPEED_MODEL": cfg.Model, "LIGHTSPEED_PROVIDER_URL": cfg.Endpoint,
		"PYTHONUNBUFFERED": "1",
	}
	keys := []string{"PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "LANG", "LC_ALL", "ANTHROPIC_API_KEY", "LIGHTSPEED_PROVIDER", "LIGHTSPEED_MODEL", "LIGHTSPEED_PROVIDER_URL", "PYTHONUNBUFFERED"}
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		if allow[key] != "" {
			env = append(env, key+"="+allow[key])
		}
	}
	return env
}

func decodeDriverResult(raw []byte) (*driverResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var result driverResult
	if err := decoder.Decode(&result); err != nil || result.Success == nil {
		return nil, errors.New("invalid runtime result")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("runtime returned trailing output")
	}
	if result.Reason != "ok" && result.Reason != "mcp_unavailable" && result.Reason != "agent_failure" && result.Reason != "runtime_error" && result.Reason != "tool_output_inspection_failed" && result.Reason != "skill_not_read" && result.Reason != "timeout" {
		return nil, errors.New("runtime returned unknown result reason")
	}
	if *result.Success && result.Reason != "ok" {
		return nil, errors.New("runtime result contradicts its success flag")
	}
	return &result, nil
}

func safeFailureReason(reason string) string {
	switch reason {
	case "mcp_unavailable", "agent_failure", "runtime_error", "tool_output_inspection_failed", "skill_not_read", "timeout":
		return reason
	default:
		return "agent_failure"
	}
}

func safeMetadata(raw json.RawMessage) *driverMetadata {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var metadata driverMetadata
	if json.Unmarshal(raw, &metadata) != nil {
		return nil
	}
	// Only retain typed metadata fields, never agent/model text.
	metadata.Runtime = safeLabel(metadata.Runtime)
	metadata.ErrorType = safeLabel(metadata.ErrorType)
	metadata.ExceptionClass = safeLabel(metadata.ExceptionClass)
	metadata.ACPAdapterVersion = safeLabel(metadata.ACPAdapterVersion)
	metadata.ACPSDKVersion = safeLabel(metadata.ACPSDKVersion)
	metadata.HistoryPersistence = safeLabel(metadata.HistoryPersistence)
	metadata.Interaction = safeLabel(metadata.Interaction)
	metadata.UsageScope = safeLabel(metadata.UsageScope)
	metadata.MCPToolsAdmitted = safeLabels(metadata.MCPToolsAdmitted)
	metadata.ToolCalls = safeLabels(metadata.ToolCalls)
	metadata.EventTypes = safeLabels(metadata.EventTypes)
	return &metadata
}

func validProofMetadata(metadata *driverMetadata) bool {
	if metadata == nil || !metadata.SkillFilePresent || !metadata.SkillFileRead {
		return false
	}
	return contains(metadata.MCPToolsAdmitted, "issue_nonce") && contains(metadata.ToolCalls, "issue_nonce")
}

func maxTurns(f params.File) int {
	if f.Execution.MaxTurns > 0 {
		return f.Execution.MaxTurns
	}
	return defaultMaxTurns
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func safeLabels(values []string) []string {
	if len(values) > 50 {
		values = values[:50]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if label := safeLabel(value); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func safeLabel(value string) string {
	if len(value) > 128 {
		value = value[:128]
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-/", r)) {
			return ""
		}
	}
	return value
}

func prepareSkillRoot(skillsDir, runtimeRoot string) error {
	source := filepath.Join(skillsDir, "probe")
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return errors.New("skills directory is unavailable")
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
		return errors.New("probe skill source must be a regular directory")
	}
	manifestInfo, err := os.Lstat(filepath.Join(source, "SKILL.md"))
	if err != nil || manifestInfo.Mode()&os.ModeSymlink != 0 || !manifestInfo.Mode().IsRegular() {
		return errors.New("probe skill must contain a regular SKILL.md")
	}
	runtimeRoot, err = filepath.Abs(runtimeRoot)
	if err != nil {
		return errors.New("runtime workspace is unavailable")
	}
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return errors.New("runtime workspace is unavailable")
	}
	resolvedRoot, err := filepath.EvalSymlinks(runtimeRoot)
	if err != nil {
		return errors.New("runtime workspace is unavailable")
	}
	return copySkillTree(source, filepath.Join(resolvedRoot, "probe"), resolvedRoot)
}

func copySkillTree(source, destination, runtimeRoot string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return errors.New("probe skill source is unavailable")
	}
	destination, err = filepath.Abs(destination)
	if err != nil || !isWithinRoot(runtimeRoot, destination) || destination == runtimeRoot {
		return errors.New("probe skill destination is outside runtime workspace")
	}
	return filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("probe skill source cannot be read")
		}
		info, err := os.Lstat(sourcePath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("probe skill source contains a symlink")
		}
		relative, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return errors.New("probe skill source path is invalid")
		}
		destinationPath := filepath.Join(destination, relative)
		if !isWithinRoot(runtimeRoot, destinationPath) {
			return errors.New("probe skill path escapes runtime workspace")
		}
		if info.IsDir() {
			if existing, err := os.Lstat(destinationPath); err == nil {
				if existing.Mode()&os.ModeSymlink != 0 || !existing.IsDir() {
					return errors.New("probe skill destination contains a non-directory")
				}
			} else if !os.IsNotExist(err) {
				return errors.New("probe skill destination cannot be inspected")
			} else if err := os.Mkdir(destinationPath, 0o755); err != nil {
				return errors.New("probe skill destination cannot be created")
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("probe skill source contains a non-regular file")
		}
		if existing, err := os.Lstat(destinationPath); err == nil {
			if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
				return errors.New("probe skill destination contains a non-regular file")
			}
		} else if !os.IsNotExist(err) {
			return errors.New("probe skill destination cannot be inspected")
		}
		if err := copyRegularFile(sourcePath, destinationPath, info.Mode().Perm()&0o755); err != nil {
			return errors.New("probe skill file cannot be copied")
		}
		return nil
	})
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func isWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateHTTPURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid HTTP URL")
	}
	return u, nil
}

func writeTerminationLog(term terminationData) {
	path := envOr("HARNESS_TERMINATION_LOG_PATH", "/dev/termination-log")
	data, err := json.Marshal(term)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		remaining := b.limit - b.Len()
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:remaining])
		}
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// execCommandContext is a variable so unit tests can substitute a local
// fixture process without invoking the LightSpeed runtime.
var execCommandContext = exec.CommandContext
