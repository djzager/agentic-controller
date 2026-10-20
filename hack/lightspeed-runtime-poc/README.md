# Shared-runtime prototype reference

Preserved experimental source for [the findings summary](../../docs/research/lightspeed-runtime-2026-10.md). Baseline Konveyor commit: `41afab2c868d37a48b19e62641234e4c55b86d51`. Upstream sandbox source: `325121ba39db810b1e98f38e5a1ca8dc02fecdef`. This code is outside production entrypoints and is not a supported integration.

- `harness/experiments/lightspeed-runtime`: Go parent reusing existing Hub, Git, params, prompt, ACP and tee packages. It prepares the workload, supplies a restricted child environment, validates typed outcomes and performs final push/token cleanup.
- `driver.py`: one-shot adapter over the actual Lightspeed configuration, skill/MCP discovery, DeepAgents provider and query/cleanup paths.
- `acp_runtime_server.py`: passive first-party ACP bridge over extracted internal graph construction, with shared in-memory checkpoint state. One owner prompt only. Viewer mutation is rejected; display/inspection parity is incomplete.
- `continuation_probe.py`: bounded retained-context and fresh-process committed-handoff probes. The exported copy requires `KONVEYOR_LLM_ENDPOINT` instead of the original deployment-specific endpoint literal. Other source bytes are preserved; the manifest records both hashes.
- `acp_adapter_probe.py`: synthetic multi-prompt/history and blocked-node cancellation checks.
- `observe_runtime_acp.py` and `hack/lightspeed-contract-poc/acp_reconnect/main.go`: passive observer and completed-tool replay probe.
- `fixture_mcp.py` and `skills/probe/SKILL.md`: benign nonce/marker fixtures. The MCP server is an unauthenticated internal test fixture, not a service to expose publicly.

## Local checks

```sh
cd harness
go test ./experiments/lightspeed-runtime ./internal/acp/... ./internal/tee/...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/lightspeed-runtime ./experiments/lightspeed-runtime
go build -o /tmp/acp-reconnect ../hack/lightspeed-contract-poc/acp_reconnect/main.go
```

The ACP/tee tests require local loopback listeners. Python sources can be syntax-checked without invoking a model. The in-package operator probe is preserved separately in the pinned fork linked from the findings summary.

## Runtime/image reproduction

Build `Containerfile.runtime` using the complete pinned Lightspeed sandbox checkout as build context, copying that file as its Dockerfile. It uses the upstream lockfile and deepagents extra. Compile the Go command from this Konveyor baseline. For `Containerfile`, provide that recorded dependency image through `RUNTIME_IMAGE` and copy the Go binary, Python files and `skills/` into the build context. `Containerfile.acp` additionally requires the exact DeepAgents ACP 0.0.13 and ACP SDK 0.12.1 wheels.

The Go entrypoint and Python drivers take runtime settings/credentials at execution. Use fresh credentials, your own Hub application/Git fixture and explicitly configured model endpoint. The live workload requires a suitable Agent Sandbox installation and a reachable benign MCP fixture; this source reference is not a generic installer. No old private fixture state, raw observations, internal registry images, kubeconfig, logs, model keys or Hub/Git credentials are included.

The continuity script can run within the pinned dependency environment with `--mode continuity --root <new-private-directory> --samples 3`, or `--mode handoff` for fresh-process Git handoff. It uses the model identity selected by the original experiment and requires your own endpoint and fresh model credential. Capture output privately. The synthetic ACP probe needs the exact adapter/SDK dependencies but does not call a model. Reproduction must preserve the documented limits: neither local tests nor source publication repeats the live evidence.
