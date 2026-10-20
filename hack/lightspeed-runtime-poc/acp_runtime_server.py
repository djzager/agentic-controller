#!/usr/bin/env python3
"""Isolated passive ACP proof over Lightspeed's pinned DeepAgents graph.

The Go parent owns Hub/Git lifecycle and the active ACP connection. This process
prepares the actual Lightspeed graph construction prefix, exposes first-party ACP
on loopback:4001, and shares an in-memory saver across private viewer connections.
Only the first session/new connection can issue the one workload prompt. Viewers
can initialize and load that session, never start or mutate work. This is a
research adapter, not a production provider/session API or durable recovery.

stdin: one existing driverInput JSON object. stdout: exactly one driverResult
JSON when the owning prompt finishes (or preparation fails). The server remains
alive after that record until the parent terminates it, preserving viewer load.
"""

from __future__ import annotations

import argparse
import ast
import asyncio
import hashlib
import importlib.metadata
import inspect
import json
import logging
import os
import re
import sys
import textwrap
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

SOURCE_REVISION = "325121ba39db810b1e98f38e5a1ca8dc02fecdef"
SOURCE_SHA256 = "71fbbfcb57ff696e34861eabfd81d566d226b7498aa54b062e24ce56a7f248fb"
ADAPTER_VERSION = "0.0.13"
SDK_VERSION = "0.12.1"
RUNTIME_ROOT = Path("/workspace")
RUN_ID_RE = re.compile(r"^[A-Za-z0-9_.-]{1,80}$")
DEFAULT_MODEL = "rits/zai-org/glm-5-3"
SERVER_NAME = "nonce-fixture"
TOOL_NAME = "issue_nonce"
SKILL_MARKER = "skill=probe-skill-v1"


def _safe_error(exc: BaseException) -> str:
    """Class names, never SDK exception text/URLs/headers, cross the log boundary."""
    return re.sub(r"[^A-Za-z0-9_]", "", type(exc).__name__)[:80] or "unknown"


@dataclass
class Proof:
    metadata: dict[str, Any]
    owner: Any = None
    session_id: str | None = None
    prompt_started: bool = False
    emitted: bool = False
    graphs: dict[str, Any] = field(default_factory=dict)

    def emit(self, success: bool, reason: str, usage: dict[str, int] | None = None) -> None:
        if self.emitted:
            return
        self.emitted = True
        print(json.dumps({"success": success, "reason": reason, "usage": usage,
                          "metadata": self.metadata}, separators=(",", ":")), flush=True)


def _builder_ast(query_source: str) -> ast.Module:
    """Extract only the pinned provider's graph-construction prefix, without edits.

    The new return/cleanup boundary is the experimental seam. Model, backend,
    skill, MCP tool and inspection construction statements remain upstream AST.
    """
    module = ast.parse(textwrap.dedent(query_source))
    if len(module.body) != 1 or not isinstance(module.body[0], ast.AsyncFunctionDef):
        raise RuntimeError("unexpected_query_shape")
    query = module.body[0]
    boundaries = [i for i, node in enumerate(query.body)
                  if isinstance(node, ast.Assign)
                  and isinstance(node.value, ast.Call)
                  and isinstance(node.value.func, ast.Name)
                  and node.value.func.id == "create_deep_agent"]
    if len(boundaries) != 1:
        raise RuntimeError("unexpected_graph_boundary")
    prefix = query.body[:boundaries[0]]
    if any(isinstance(node, (ast.Yield, ast.YieldFrom))
           for statement in prefix for node in ast.walk(statement)):
        raise RuntimeError("unexpected_prefix_yield")
    returned = ast.parse("""
return dict(agent_kwargs=agent_kwargs, chat_model=chat_model,
            classifier_model=classifier_model, generation_callback=generation_callback,
            inspection_middleware=inspection_middleware)
""").body
    # Prefix failure must not leak model clients before the bundle is returned.
    handler = ast.parse("""
try:
    pass
except BaseException:
    for model_name in ("classifier_model", "chat_model"):
        model = locals().get(model_name)
        if model is not None:
            try:
                await _close_model_clients(model)
            except Exception:
                pass
    raise
""").body[0]
    assert isinstance(handler, ast.Try)
    handler.body = prefix + returned
    query.name = "_prepare_acp_graph"
    query.body = [handler]
    query.decorator_list = []
    return ast.fix_missing_locations(module)


def _prepare_function(provider_module: Any) -> Any:
    source_path = Path(provider_module.__file__)
    if hashlib.sha256(source_path.read_bytes()).hexdigest() != SOURCE_SHA256:
        raise RuntimeError("lightspeed_source_pin_mismatch")
    namespace = dict(vars(provider_module))
    extracted = _builder_ast(inspect.getsource(provider_module.DeepAgentsProvider.query))
    exec(compile(extracted, "<pinned-lightspeed-acp-construction>", "exec"), namespace)
    return namespace["_prepare_acp_graph"]


def _validate_inputs(inputs: Any, root_override: str | None,
                     repo_override: str | None) -> tuple[dict[str, Any], Path]:
    if not isinstance(inputs, dict):
        raise ValueError("input_object_required")
    run_id = inputs.get("run_id")
    if not isinstance(run_id, str) or not RUN_ID_RE.fullmatch(run_id):
        raise ValueError("invalid_run_id")
    runtime_root = Path(root_override or inputs.get("runtime_root", "/workspace"))
    if runtime_root != RUNTIME_ROOT or runtime_root.resolve(strict=True) != RUNTIME_ROOT:
        raise ValueError("fixed_workspace_required")
    relative = repo_override or inputs.get("repo_relative_path")
    if not isinstance(relative, str) or not relative or Path(relative).is_absolute():
        raise ValueError("relative_repo_required")
    repo = (runtime_root / relative).resolve(strict=True)
    repo.relative_to(runtime_root)
    if repo == runtime_root or not repo.is_dir():
        raise ValueError("repo_child_required")
    inputs["runtime_root"] = str(runtime_root)
    inputs["repo_relative_path"] = relative
    for key, default, maximum in (("max_turns", 32, 256), ("timeout_seconds", 540, 3540)):
        value = inputs.get(key, default)
        if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= maximum:
            raise ValueError("invalid_execution_bound")
        inputs[key] = value
    return inputs, repo


def _skill_visible() -> bool:
    path = RUNTIME_ROOT / "probe" / "SKILL.md"
    if path.is_symlink() or path.parent.is_symlink():
        return False
    try:
        path.resolve(strict=True).relative_to(RUNTIME_ROOT)
        expected = Path(__file__).resolve().parent / "skills" / "probe" / "SKILL.md"
        return path.read_bytes() == expected.read_bytes()
    except (OSError, ValueError):
        return False


def _skill_call(arguments: Any) -> bool:
    if not isinstance(arguments, dict):
        return False
    path = arguments.get("file_path")
    if not isinstance(path, str):
        return False
    normalized = path.replace("\\", "/").rstrip("/")
    return normalized.endswith("/probe/SKILL.md") or normalized == "probe/SKILL.md"


def _graph_proof(messages: list[Any], proof: Proof) -> dict[str, int] | None:
    calls: list[str] = []
    skill_call_ids: set[str] = set()
    input_tokens = output_tokens = observed_usage = 0
    for message in messages:
        if message.type != "ai":
            continue
        for call in getattr(message, "tool_calls", []) or []:
            name = call.get("name", "")
            if isinstance(name, str) and name and len(calls) < 100:
                calls.append(name)
            if name == "read_file" and _skill_call(call.get("args")):
                skill_call_ids.add(call.get("id", ""))
        usage = getattr(message, "usage_metadata", None)
        if isinstance(usage, dict):
            in_count, out_count = usage.get("input_tokens"), usage.get("output_tokens")
            if (isinstance(in_count, int) and not isinstance(in_count, bool) and in_count >= 0
                    and isinstance(out_count, int) and not isinstance(out_count, bool)
                    and out_count >= 0):
                input_tokens += in_count
                output_tokens += out_count
                observed_usage += 1
    proof.metadata["tool_calls"] = calls
    proof.metadata["skill_file_read"] = any(
        message.type == "tool"
        and getattr(message, "tool_call_id", "") in skill_call_ids
        and getattr(message, "status", "success") != "error"
        and SKILL_MARKER in str(getattr(message, "content", ""))
        for message in messages
    )
    proof.metadata["event_types"] = (["text_delta"] if any(m.type == "ai" for m in messages) else [])
    if calls:
        proof.metadata["event_types"].append("tool_call")
    if any(m.type == "tool" for m in messages):
        proof.metadata["event_types"].append("tool_result")
    proof.metadata["usage_observable"] = observed_usage > 0
    proof.metadata["usage_complete"] = False
    proof.metadata["usage_scope"] = "checkpoint_main_ai_messages_excludes_inspection_and_subagents"
    return ({"input_tokens": input_tokens, "output_tokens": output_tokens}
            if observed_usage else None)


def _composed_prompt(inputs: dict[str, Any]) -> str:
    base = inputs.get("prompt")
    if not isinstance(base, str) or not base.strip():
        base = "Complete the isolated repository proof."
    return (
        f"{base.rstrip()}\n\n"
        "For this passive observation experiment, your first tool call must be "
        "execute with command exactly `pwd`. Wait for its successful result. "
        "Your second tool call must be a separate execute call with command exactly "
        "`sleep 12`. Do not combine these commands and do not call other tools before "
        "both have completed. Then use the probe skill, reading its SKILL.md, in "
        f"repository `{inputs['repo_relative_path']}` for run ID `{inputs['run_id']}`. "
        "Use fixture scenario benign. Commit locally as the skill requests. Do not push."
    )


async def _serve(inputs: dict[str, Any], repo: Path, proof: Proof) -> None:
    from acp.exceptions import RequestError
    from acp.http.asgi import create_asgi_app
    from acp.schema import TextContentBlock
    from deepagents import create_deep_agent
    from deepagents_acp.server import AgentServerACP
    from langgraph.checkpoint.memory import InMemorySaver
    from lightspeed_agentic.config import (
        parse_reasoning_config, resolve_router_model, resolve_sdk, resolve_startup_model,
    )
    from lightspeed_agentic.mcp import (
        discover_and_admit_mcp_servers, parse_mcp_servers, split_admitted_mcp_servers,
    )
    from lightspeed_agentic.providers import deepagents as provider_module
    from lightspeed_agentic.tls import configure_tls
    from lightspeed_agentic.types import ProviderQueryOptions
    import uvicorn

    for package, expected in (("deepagents-acp", ADAPTER_VERSION),
                              ("agent-client-protocol", SDK_VERSION), ("deepagents", "0.7.22")):
        if importlib.metadata.version(package) != expected:
            raise RuntimeError("dependency_pin_mismatch")
    os.environ["LIGHTSPEED_PROVIDER"] = "anthropic"
    model = inputs.get("model") or os.environ.get("LIGHTSPEED_MODEL", DEFAULT_MODEL)
    if not isinstance(model, str) or not model.strip():
        raise ValueError("model_required")
    os.environ["LIGHTSPEED_MODEL"] = model.strip()
    os.environ["LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED"] = "true"
    configure_tls()
    if resolve_sdk().name != "deepagents":
        raise RuntimeError("unexpected_provider")
    mcp_url = inputs.get("mcp_url")
    if not isinstance(mcp_url, str) or not mcp_url.startswith(("http://", "https://")):
        raise ValueError("required_mcp_url")
    os.environ["LIGHTSPEED_MCP_SERVERS"] = json.dumps([
        {"name": SERVER_NAME, "url": mcp_url, "timeout": 20, "headers": []}
    ])
    admitted = await asyncio.wait_for(
        discover_and_admit_mcp_servers(parse_mcp_servers()), timeout=60)
    fixture = next((s for s in admitted if s.name == SERVER_NAME), None)
    if fixture is None or not any(tool.name == TOOL_NAME for tool in fixture.tools):
        proof.emit(False, "mcp_unavailable")
        return
    proof.metadata["mcp_tools_admitted"] = [tool.name for tool in fixture.tools]
    servers, _policies = split_admitted_mcp_servers(admitted)
    system_prompt = inputs.get("system_prompt")
    if not isinstance(system_prompt, str) or not system_prompt.strip():
        system_prompt = "Follow the task and available skills. Work only in the named repository."
    options = ProviderQueryOptions(
        prompt=_composed_prompt(inputs), system_prompt=system_prompt,
        model=resolve_router_model("deepagents", resolve_startup_model("deepagents")),
        max_turns=inputs["max_turns"], allowed_tools=[], cwd=str(RUNTIME_ROOT),
        output_schema=None, mcp_servers=servers, reasoning_config=parse_reasoning_config(),
        tool_output_inspection_enabled=True,
        deadline=time.monotonic() + inputs["timeout_seconds"],
    )
    prepared = await asyncio.wait_for(
        _prepare_function(provider_module)(provider_module.DeepAgentsProvider(), options),
        timeout=min(120, inputs["timeout_seconds"]),
    )
    saver = InMemorySaver()

    def build_graph(context: Any) -> Any:
        if context.cwd != str(RUNTIME_ROOT) or context.mode != "auto" or context.model is not None:
            raise RequestError.invalid_params({"context": "fixed workload context required"})
        return create_deep_agent(**prepared["agent_kwargs"], checkpointer=saver).with_config({
            "callbacks": [prepared["generation_callback"]],
            "recursion_limit": options.max_turns,
        })

    class PassiveAgent(AgentServerACP):
        async def initialize(self, protocol_version: int, **kwargs: Any) -> Any:
            if protocol_version != 1:
                raise RequestError.invalid_params({"protocolVersion": "requires 1"})
            return await super().initialize(protocol_version=protocol_version, **kwargs)

        async def new_session(self, cwd: str, additional_directories: Any = None,
                              mcp_servers: Any = None, **kwargs: Any) -> Any:
            if cwd != str(RUNTIME_ROOT) or additional_directories or mcp_servers:
                raise RequestError.invalid_params({"context": "fixed workload context required"})
            if proof.owner is not None:
                raise RequestError(-32600, "one workload session is already owned")
            proof.owner = self
            try:
                response = await super().new_session(cwd=cwd, mcp_servers=[], **kwargs)
                proof.session_id = response.session_id
                return response
            except BaseException as exc:
                proof.metadata["error_type"] = _safe_error(exc)
                proof.emit(False, "runtime_error")
                raise

        async def load_session(self, cwd: str, session_id: str,
                               additional_directories: Any = None,
                               mcp_servers: Any = None, **kwargs: Any) -> Any:
            if (cwd != str(RUNTIME_ROOT) or additional_directories or mcp_servers
                    or session_id != proof.session_id):
                raise RequestError.invalid_params({"context": "existing workload session required"})
            return await super().load_session(cwd=cwd, session_id=session_id,
                                              mcp_servers=[], **kwargs)

        async def prompt(self, prompt: Any, session_id: str, **kwargs: Any) -> Any:
            if proof.owner is not self or session_id != proof.session_id or proof.prompt_started:
                raise RequestError(-32600, "passive connection cannot prompt or repeat work")
            proof.prompt_started = True
            started = time.monotonic()
            try:
                response = await asyncio.wait_for(super().prompt(
                    prompt=[TextContentBlock(type="text", text=options.prompt)],
                    session_id=session_id, **kwargs), timeout=inputs["timeout_seconds"])
                snapshot = await self._agent.aget_state(self._session_config(session_id))
                usage = _graph_proof(snapshot.values.get("messages", []), proof)
                proof.metadata["stop_reason"] = response.stop_reason
                proof.metadata["prompt_duration_seconds"] = round(time.monotonic() - started, 3)
                success = (response.stop_reason == "end_turn"
                           and proof.metadata["skill_file_read"]
                           and TOOL_NAME in proof.metadata["tool_calls"] and repo.is_dir())
                reason = ("ok" if success else "skill_not_read"
                          if not proof.metadata["skill_file_read"] else "agent_failure")
                proof.emit(success, reason, usage)
                return response
            except BaseException as exc:
                proof.metadata["error_type"] = _safe_error(exc)
                reason = ("timeout" if isinstance(exc, TimeoutError) else
                          "tool_output_inspection_failed"
                          if _safe_error(exc) == "ToolResultSafetyInspectionFailed" else "runtime_error")
                proof.emit(False, reason)
                raise

        async def cancel(self, **kwargs: Any) -> None:
            raise RequestError.method_not_found("session/cancel")

        async def set_config_option(self, **kwargs: Any) -> Any:
            raise RequestError.method_not_found("session/set_config_option")

        async def set_session_mode(self, **kwargs: Any) -> Any:
            raise RequestError.method_not_found("session/set_mode")

        async def close_session(self, **kwargs: Any) -> Any:
            raise RequestError.method_not_found("session/close")

        async def fork_session(self, **kwargs: Any) -> Any:
            raise RequestError.method_not_found("session/fork")

        async def resume_session(self, **kwargs: Any) -> Any:
            raise RequestError.method_not_found("session/resume")

        async def ext_method(self, name: str, payload: Any) -> Any:
            raise RequestError.method_not_found("_" + name)

        async def ext_notification(self, name: str, payload: Any) -> None:
            raise RequestError.method_not_found("_" + name)

    app = create_asgi_app(lambda conn: PassiveAgent(build_graph, load_sessions=True))
    server = uvicorn.Server(uvicorn.Config(
        app, host="127.0.0.1", port=4001, ws="websockets", lifespan="on",
        log_config=None, access_log=False,
    ))
    try:
        await server.serve()
    finally:
        prepared["generation_callback"].close_open("acp_server_shutdown")
        for name in ("classifier_model", "chat_model"):
            model_instance = prepared.get(name)
            if model_instance is not None:
                try:
                    await provider_module._close_model_clients(model_instance)
                except Exception:
                    logging.getLogger("acp-runtime").warning("model cleanup failed")


def main() -> int:
    # Ignore log messages/exception strings from SDKs: only logger names and
    # severity reach stderr. Model/tool content is observable through ACP only.
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(logging.Formatter("%(levelname)s %(name)s"))
    logging.basicConfig(level=logging.WARNING, handlers=[handler], force=True)
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime-root")
    parser.add_argument("--repo-relative-path")
    args = parser.parse_args()
    proof = Proof({
        "runtime": "lightspeed-deepagents", "skill_file_present": False,
        "skill_file_read": False, "mcp_tools_admitted": [], "tool_calls": [], "event_types": [],
        "lightspeed_source_revision": SOURCE_REVISION, "lightspeed_source_sha256": SOURCE_SHA256,
        "acp_adapter_version": ADAPTER_VERSION, "acp_sdk_version": SDK_VERSION,
        "history_persistence": "process_memory_only", "interaction": "passive",
        "query_envelope_reused": False, "model_boundary_inspection": True,
        "tool_result_display_inspection_gate": False,
    })
    try:
        inputs, repo = _validate_inputs(json.load(sys.stdin), args.runtime_root,
                                        args.repo_relative_path)
        proof.metadata["skill_file_present"] = _skill_visible()
        if not proof.metadata["skill_file_present"]:
            proof.emit(False, "runtime_error")
            return 2
        asyncio.run(_serve(inputs, repo, proof))
        if not proof.emitted:
            proof.emit(False, "runtime_error")
        return 0
    except BaseException as exc:
        proof.metadata["error_type"] = _safe_error(exc)
        proof.emit(False, "runtime_error")
        logging.getLogger("acp-runtime").error("runtime failed")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
