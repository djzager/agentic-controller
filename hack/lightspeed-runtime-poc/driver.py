#!/usr/bin/env python3
"""Small Go-harness entrypoint over Lightspeed's shared DeepAgents runtime."""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import re
import sys
from pathlib import Path
from typing import Any


SERVER_NAME = "nonce-fixture"
TOOL_NAME = "issue_nonce"
SKILL_NAME = "probe"
DEFAULT_MODEL = "rits/zai-org/glm-5-3"
RUN_ID_RE = re.compile(r"^[A-Za-z0-9_.-]{1,80}$")


def _emit(
    success: bool,
    reason: str,
    *,
    usage: dict[str, int] | None = None,
    metadata: dict[str, Any] | None = None,
    diagnostic: dict[str, str] | None = None,
) -> None:
    """Write one content-free result record for the Go parent."""
    print(
        json.dumps(
            {
                "success": success,
                "reason": reason,
                "usage": usage or {"input_tokens": 0, "output_tokens": 0},
                "metadata": metadata or {},
                "diagnostic": diagnostic or {},
            },
            separators=(",", ":"),
        ),
        flush=True,
    )


def _skill_file_is_visible(runtime_root: Path) -> bool:
    """Require the harness-provided skill at the root Lightspeed scans."""
    skill_path = runtime_root / "probe" / "SKILL.md"
    if skill_path.is_symlink() or skill_path.parent.is_symlink():
        return False
    skill_file = skill_path.resolve(strict=True)
    skill_file.relative_to(runtime_root)
    expected = (Path(__file__).resolve().parent / "skills" / "probe" / "SKILL.md").resolve()
    return skill_file.read_bytes() == expected.read_bytes()


def _skill_path_was_read(arguments: str) -> bool:
    try:
        value = json.loads(arguments)
    except (TypeError, json.JSONDecodeError):
        return False

    pending = [value]
    while pending:
        current = pending.pop()
        if isinstance(current, dict):
            for key, item in current.items():
                if isinstance(item, str) and ("path" in key.lower() or "file" in key.lower()):
                    normalized = item.replace("\\", "/").rstrip("/")
                    if normalized.endswith("/probe/SKILL.md") or normalized == "probe/SKILL.md":
                        return True
                pending.append(item)
        elif isinstance(current, list):
            pending.extend(current)
    return False


class RecordingProvider:
    """Record tool names and the skill-read fact, never event contents."""

    def __init__(self, wrapped: Any) -> None:
        self.wrapped = wrapped
        self.tool_calls: list[str] = []
        self.event_types: list[str] = []
        self.skill_file_read = False
        self.skill_read_calls: set[str] = set()

    @property
    def name(self) -> str:
        return self.wrapped.name

    async def query(self, options: Any):
        async for event in self.wrapped.query(options):
            event_type = getattr(event, "type", "unknown")
            if str(event_type) not in self.event_types:
                self.event_types.append(str(event_type))
            if event_type == "tool_call":
                name = str(getattr(event, "name", ""))
                if name and len(self.tool_calls) < 100:
                    self.tool_calls.append(name)
                if name == "read_file" and _skill_path_was_read(str(getattr(event, "input", ""))):
                    self.skill_read_calls.add(str(getattr(event, "call_id", "")))
            elif event_type == "tool_result":
                if (
                    getattr(event, "call_id", "") in self.skill_read_calls
                    and not getattr(event, "error_type", None)
                    and "skill=probe-skill-v1" in str(getattr(event, "output", ""))
                ):
                    self.skill_file_read = True
            yield event

    async def aclose(self) -> None:
        await self.wrapped.aclose()


async def _run(inputs: dict[str, Any], runtime_root: Path, repo_path: Path) -> None:
    from lightspeed_agentic.config import (
        parse_reasoning_config,
        resolve_router_model,
        resolve_sdk,
        resolve_startup_model,
    )
    from lightspeed_agentic.factory import create_provider
    from lightspeed_agentic.inspection.errors import ToolResultSafetyInspectionFailed
    from lightspeed_agentic.mcp import (
        discover_and_admit_mcp_servers,
        parse_mcp_servers,
        split_admitted_mcp_servers,
    )
    from lightspeed_agentic.run_agent import run_agent_query
    from lightspeed_agentic.tls import configure_tls

    os.environ["LIGHTSPEED_PROVIDER"] = "anthropic"
    model_override = inputs.get("model")
    if model_override is not None:
        if not isinstance(model_override, str) or not model_override.strip():
            _emit(False, "runtime_error", diagnostic={"error_type": "InvalidModelOverride"})
            return
        os.environ["LIGHTSPEED_MODEL"] = model_override.strip()
    else:
        configured_model = os.environ.get("LIGHTSPEED_MODEL", "").strip()
        if configured_model and configured_model != DEFAULT_MODEL:
            _emit(False, "runtime_error", diagnostic={"error_type": "UnpinnedModel"})
            return
        os.environ["LIGHTSPEED_MODEL"] = DEFAULT_MODEL
    os.environ.setdefault("LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED", "true")
    if os.environ["LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED"].strip().lower() != "true":
        _emit(False, "runtime_error")
        return

    mcp_url = inputs.get("mcp_url")
    if not isinstance(mcp_url, str) or not mcp_url.startswith("http"):
        _emit(False, "mcp_unavailable")
        return

    os.environ["LIGHTSPEED_MCP_SERVERS"] = json.dumps(
        [{"name": SERVER_NAME, "url": mcp_url, "timeout": 20, "headers": []}]
    )

    metadata: dict[str, Any] = {
        "runtime": "lightspeed-deepagents",
        "skill_file_present": _skill_file_is_visible(runtime_root),
        "mcp_tools_admitted": [],
        "tool_calls": [],
        "event_types": [],
        "skill_file_read": False,
    }
    if not metadata["skill_file_present"]:
        _emit(False, "runtime_error", metadata=metadata)
        return

    provider: RecordingProvider | None = None
    try:
        configure_tls()
        sdk = resolve_sdk()
        if sdk.name != "deepagents":
            _emit(False, "runtime_error", metadata=metadata)
            return

        configured_servers = parse_mcp_servers()
        admitted = await discover_and_admit_mcp_servers(configured_servers)
        admitted_fixture = next((s for s in admitted if s.name == SERVER_NAME), None)
        if admitted_fixture is None or not any(
            tool.name == TOOL_NAME for tool in admitted_fixture.tools
        ):
            _emit(False, "mcp_unavailable", metadata=metadata)
            return

        metadata["mcp_tools_admitted"] = [tool.name for tool in admitted_fixture.tools]
        provider_servers, _policies = split_admitted_mcp_servers(admitted)
        wrapped = create_provider(sdk.name)
        provider = RecordingProvider(wrapped)
        model = resolve_router_model(sdk.name, resolve_startup_model(sdk.name))
        prompt_prefix = inputs.get("prompt")
        if not isinstance(prompt_prefix, str):
            prompt_prefix = "Complete the requested repository proof."
        prompt = (
            f"{prompt_prefix.rstrip()}\n\n"
            f"Use the {SKILL_NAME} skill in repository `{inputs['repo_relative_path']}` "
            f"for run ID `{inputs['run_id']}`."
        )
        scenario = inputs.get("scenario")
        if scenario is not None:
            if scenario not in ("benign", "malicious"):
                _emit(False, "runtime_error", diagnostic={"error_type": "InvalidScenario"})
                return
            prompt += f" Use fixture scenario `{scenario}`."
        system_prompt = inputs.get("system_prompt")
        if not isinstance(system_prompt, str) or not system_prompt.strip():
            system_prompt = "Follow the available skills and work only in the named repository."

        result = await run_agent_query(
            provider,
            prompt=prompt,
            system_prompt=system_prompt,
            output_schema=None,
            context=None,
            skills_dir=str(runtime_root),
            model=model,
            max_turns=int(inputs.get("max_turns", 8)),
            timeout_seconds=int(inputs.get("timeout_seconds", 540)),
            mcp_servers=provider_servers,
            reasoning_config=parse_reasoning_config(),
            tool_output_inspection_enabled=True,
            audit_enabled=False,
            capture_content=False,
        )
        usage = {
            "input_tokens": int(result.input_tokens),
            "output_tokens": int(result.output_tokens),
        }
        metadata["tool_calls"] = provider.tool_calls
        metadata["event_types"] = provider.event_types
        metadata["skill_file_read"] = provider.skill_file_read
        agent_success = result.output.get("success") is True
        mcp_called = TOOL_NAME in provider.tool_calls
        repository_exists = repo_path.is_dir()
        timed_out = bool(getattr(result, "timed_out", False))
        success = (
            agent_success and mcp_called and repository_exists and provider.skill_file_read
        )
        reason = (
            "ok"
            if success
            else "timeout"
            if timed_out
            else "skill_not_read"
            if not provider.skill_file_read
            else "agent_failure"
        )
        _emit(success, reason, usage=usage, metadata=metadata)
    except ToolResultSafetyInspectionFailed:
        if provider is not None:
            metadata["tool_calls"] = provider.tool_calls
            metadata["event_types"] = provider.event_types
            metadata["skill_file_read"] = provider.skill_file_read
        metadata["error_type"] = "ToolResultSafetyInspectionFailed"
        _emit(False, "tool_output_inspection_failed", metadata=metadata)
    except Exception:
        # Do not echo exception text: SDK/network exceptions can include URLs or headers.
        exception_class = sys.exc_info()[0]
        safe_class = exception_class.__name__ if exception_class else "unknown"
        logging.getLogger("runtime-poc").error("runtime invocation failed (%s)", safe_class)
        if provider is not None:
            metadata["tool_calls"] = provider.tool_calls
            metadata["event_types"] = provider.event_types
            metadata["skill_file_read"] = provider.skill_file_read
        metadata["error_type"] = safe_class
        _emit(
            False,
            "runtime_error",
            metadata=metadata,
            diagnostic={"error_type": safe_class},
        )
    finally:
        if provider is not None:
            try:
                await provider.aclose()
            except Exception:
                logging.getLogger("runtime-poc").warning("runtime cleanup failed")


def main() -> int:
    # Upstream EventLogger emits model content at INFO even without content
    # capture. Keep private diagnostics, but do not log the conversation.
    logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime-root", required=True)
    parser.add_argument("--repo-relative-path", required=True)
    args = parser.parse_args()

    try:
        inputs = json.load(sys.stdin)
        if not isinstance(inputs, dict):
            raise ValueError("input must be a JSON object")
        run_id = inputs.get("run_id")
        if not isinstance(run_id, str) or not RUN_ID_RE.fullmatch(run_id):
            _emit(False, "runtime_error")
            return 2

        runtime_root = Path(args.runtime_root).resolve(strict=True)
        repo_path = (runtime_root / args.repo_relative_path).resolve(strict=True)
        repo_path.relative_to(runtime_root)
        if not repo_path.is_dir():
            _emit(False, "runtime_error")
            return 2
        # The parent supplies root child `probe/` from the image. The model only
        # receives the run ID; the skill holds its proof marker/instructions.
        inputs["repo_relative_path"] = args.repo_relative_path
        inputs["run_id"] = run_id
        asyncio.run(_run(inputs, runtime_root, repo_path))
        return 0
    except Exception:
        exception_class = sys.exc_info()[0]
        safe_class = exception_class.__name__ if exception_class else "unknown"
        logging.getLogger("runtime-poc").error("invalid runtime invocation (%s)", safe_class)
        _emit(False, "runtime_error", diagnostic={"error_type": safe_class})
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
