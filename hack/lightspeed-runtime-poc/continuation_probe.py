#!/usr/bin/env python3
"""Bounded runtime-only continuity/handoff research; no production entrypoint changes.

Runs inside the pinned dependency image. The checkpoint adapter is temporary
instrumentation around DeepAgents' factory, not a shipped Lightspeed capability.
Only aggregate facts are emitted; no prompts, responses, or credentials.
"""
from __future__ import annotations

import argparse
import asyncio
from collections import Counter
import hashlib
import importlib.metadata
import json
import logging
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time

MODEL = "rits/zai-org/glm-5-3"
FOLLOWUP = "What was the continuity value from our previous turn? Reply with that exact value only, or UNKNOWN if unavailable. Do not use tools."


def emit(record):
    print(json.dumps(record, separators=(",", ":")), flush=True)


def synthetic_answer(text):
    """Decode only this probe's answer, retaining observed stream artifacts.

    Pinned provider stringifies empty metadata chunks as '[]'. This is not a
    general response parser or an upstream fix; raw equality stays in evidence.
    """
    text = text.strip()
    while text.startswith("[]"):
        text = text[2:]
    while text.endswith("[]"):
        text = text[:-2]
    return text.strip()


def git(cwd, *args, check=True):
    result = subprocess.run(["git", "-C", str(cwd), *args], capture_output=True,
                            text=True, timeout=30)
    if check and result.returncode:
        raise RuntimeError("git_" + args[0])
    return result


def configure():
    from lightspeed_agentic.config import resolve_router_model, resolve_startup_model, resolve_sdk
    from lightspeed_agentic.tls import configure_tls
    configure_tls()
    if resolve_sdk().name != "deepagents":
        raise RuntimeError("unexpected_provider")
    return resolve_router_model("deepagents", resolve_startup_model("deepagents"))


async def query(provider, root, prompt):
    from lightspeed_agentic.types import ProviderQueryOptions
    options = ProviderQueryOptions(prompt=prompt,
        system_prompt="Follow the task exactly. Work only in the current directory. Never push; the test parent handles Git transport.",
        model=configure(), max_turns=32, allowed_tools=[], cwd=str(root),
        tool_output_inspection_enabled=True)
    start = time.monotonic()
    first = None
    events = Counter()
    final = None
    async with asyncio.timeout(180):
        async for event in provider.query(options):
            if first is None:
                first = time.monotonic() - start
            events[event.type] += 1
            if event.type == "result":
                final = event
    if final is None:
        raise RuntimeError("missing_result")
    return final.text, {"durationSeconds": round(time.monotonic() - start, 3),
        "firstEventSeconds": round(first, 3) if first is not None else None,
        "events": dict(events), "inputTokens": final.input_tokens,
        "outputTokens": final.output_tokens}


async def continuity(root, count):
    import deepagents
    from langgraph.checkpoint.memory import InMemorySaver
    from lightspeed_agentic.providers.deepagents import DeepAgentsProvider
    original = deepagents.create_deep_agent
    saver = InMemorySaver()
    state = {"adapted": False, "thread": None, "ids": [], "graph": None}

    class ObserveGraph:
        def __init__(self, graph):
            self.graph = graph

        async def astream(self, inputs, config, **kwargs):
            if state["adapted"]:
                config = {**config, "configurable": {**config["configurable"],
                          "thread_id": state["thread"]}}
            state["ids"].append(config["configurable"]["thread_id"])
            async for item in self.graph.astream(inputs, config=config, **kwargs):
                yield item

    def instrumented_factory(**kwargs):
        if state["adapted"]:
            kwargs["checkpointer"] = saver
        graph = original(**kwargs)
        state["graph"] = graph
        return ObserveGraph(graph)

    deepagents.create_deep_agent = instrumented_factory
    try:
        for sample in range(1, count + 1):
            sample_root = root / str(sample)
            sample_root.mkdir(parents=True)
            provider = DeepAgentsProvider()
            for adapted in (False, True):
                state.update(adapted=adapted, thread="probe-" + secrets.token_hex(8), ids=[])
                value = secrets.token_hex(16)
                first_prompt = "Remember this continuity value for our next turn: " + value + ". Reply STORED. Do not use tools or write it to any file."
                _, first = await query(provider, sample_root, first_prompt)
                before = list(sample_root.rglob("*"))
                recalled, second = await query(provider, sample_root, FOLLOWUP)
                same_ids = state["ids"][0] == state["ids"][1]
                history_contains_value = False
                history_contains_both_prompts = False
                if adapted:
                    saved = await state["graph"].aget_state({"configurable": {"thread_id": state["thread"]}})
                    history_contains_value = any(value in str(m.content) for m in saved.values.get("messages", []))
                    history_contains_both_prompts = all(any(m.type == "human" and m.content == p
                        for m in saved.values.get("messages", [])) for p in (first_prompt, FOLLOWUP))
                no_tools = not any(record["events"].get("tool_call", 0) for record in (first, second))
                record = {"kind": "continuity", "sample": sample,
                    "path": "experimental-checkpoint" if adapted else "unmodified-provider",
                    "sameThread": same_ids, "threadIDs": list(state["ids"]),
                    "recalled": synthetic_answer(recalled) == value,
                    "rawExactMatch": recalled.strip() == value,
                    "responseContainsValue": value in recalled,
                    "responseCharacters": len(recalled),
                    "emptyListBoundaryArtifacts": recalled.startswith("[]") or recalled.endswith("[]"),
                    "checkpointContainsPriorPrompt": history_contains_value,
                    "checkpointContainsBothPrompts": history_contains_both_prompts,
                    "workspaceEmpty": not before and not list(sample_root.rglob("*")),
                    "noToolCalls": no_tools, "turns": [first, second]}
                if adapted:
                    state["thread"] = "fresh-" + secrets.token_hex(8)
                    control, control_stats = await query(provider, sample_root, FOLLOWUP)
                    record["freshThreadControl"] = {"recalled": value in control,
                        "answeredUnknown": synthetic_answer(control) == "UNKNOWN", **control_stats}
                record["assertion"] = "PASS" if (same_ids == adapted and
                    record["recalled"] == adapted and no_tools and record["workspaceEmpty"] and
                    (not adapted or (history_contains_value and history_contains_both_prompts
                        and not record["freshThreadControl"]["recalled"]
                        and record["freshThreadControl"]["answeredUnknown"]))) else "FAIL"
                emit(record)
            await provider.aclose()
    finally:
        deepagents.create_deep_agent = original


def expected_handoff(checkout):
    data = json.loads((checkout / ".konveyor/handoff.json").read_text())
    if set(data) != {"version", "input_sha256", "next"} or data["version"] != 1 or data["next"] != "sha256":
        raise ValueError("invalid_handoff")
    content = (checkout / "input.txt").read_bytes()
    if hashlib.sha256(content).hexdigest() != data["input_sha256"]:
        raise ValueError("invalid_handoff")
    return hashlib.sha256(content).hexdigest() + "\n"


def advance_allowed(agent_succeeded, push_succeeded, artifact_valid):
    return agent_succeeded and push_succeeded and artifact_valid


async def stage(root, name):
    from lightspeed_agentic.providers.deepagents import DeepAgentsProvider
    provider = DeepAgentsProvider()
    try:
        if name == "a":
            value = secrets.token_hex(16) + "\n"
            digest = hashlib.sha256(value.encode()).hexdigest()
            handoff = json.dumps({"version": 1, "input_sha256": digest, "next": "sha256"}) + "\n"
            prompt = ("Create input.txt with exact content " + repr(value) +
                " and .konveyor/handoff.json with exact content " + repr(handoff) +
                ". Commit only those files with message 'Stage A handoff'. Never push. Reply DONE after a successful commit.")
        else:
            expected_handoff(root)  # fail before inference for absent/invalid input
            prompt = ("Read .konveyor/handoff.json and input.txt. Follow the handoff's next action: "
                "write result.txt containing the lowercase SHA256 hex digest of the complete input.txt bytes followed by one newline. "
                "Commit only result.txt with message 'Stage B result'. Never push. Reply DONE after a successful commit.")
        text, stats = await query(provider, root, prompt)
        stats.update({"kind": "stage", "stage": name, "pid": os.getpid(),
                      "workspace": str(root), "reportedDone": text.strip() == "DONE"})
        if name == "a":
            if (root / "input.txt").read_text() != value or (root / ".konveyor/handoff.json").read_text() != handoff:
                raise RuntimeError("stage_a_content")
        else:
            if (root / "result.txt").read_text() != expected_handoff(root):
                raise RuntimeError("stage_b_content")
        if git(root, "status", "--porcelain").stdout.strip():
            raise RuntimeError("dirty_stage")
        emit(stats)
    finally:
        await provider.aclose()


def handoff(root, count):
    script = str(Path(__file__).resolve())
    for sample in range(1, count + 1):
        trial = root / str(sample)
        trial.mkdir(parents=True)
        remote = trial / "remote.git"
        git(trial, "init", "--bare", str(remote))
        seed = trial / "seed"
        git(trial, "init", "--initial-branch=main", str(seed))
        git(seed, "config", "user.name", "Continuation fixture")
        git(seed, "config", "user.email", "fixture@example.test")
        (seed / "README.md").write_text("Synthetic handoff fixture.\n")
        git(seed, "add", "README.md")
        git(seed, "commit", "-m", "Seed")
        git(seed, "remote", "add", "origin", str(remote))
        git(seed, "push", "origin", "main")
        base = git(seed, "rev-parse", "HEAD").stdout.strip()
        shutil.rmtree(seed)
        stages = []
        heads = []
        transition_start = None
        for name in ("a", "b"):
            checkout = trial / ("stage-" + name)
            git(trial, "clone", "--branch", "main" if name == "a" else "work", str(remote), str(checkout))
            git(checkout, "config", "user.name", "Continuation fixture")
            git(checkout, "config", "user.email", "fixture@example.test")
            if name == "a":
                git(checkout, "checkout", "-b", "work")
            else:
                if git(checkout, "rev-parse", "HEAD").stdout.strip() != heads[0]:
                    raise RuntimeError("wrong_stage_b_base")
                expected_handoff(checkout)
            home = trial / ("home-" + name)
            home.mkdir()
            start = time.monotonic()
            child_env = {**os.environ, "HOME": str(home)}
            child = subprocess.run([sys.executable, script, "--mode", "stage", "--stage", name,
                                    "--root", str(checkout)], env=child_env, text=True,
                                    capture_output=True, timeout=210)
            if child.returncode:
                raise RuntimeError("stage_" + name + "_failed")
            stats = json.loads(child.stdout)
            stats["processSeconds"] = round(time.monotonic() - start, 3)
            if name == "b":
                stats["transitionSeconds"] = round(start - transition_start, 3)
            sha = git(checkout, "rev-parse", "HEAD").stdout.strip()
            parent = git(checkout, "rev-parse", "HEAD^").stdout.strip()
            if parent != (base if name == "a" else heads[0]):
                raise RuntimeError("commit_ancestry")
            heads.append(sha)
            git(checkout, "push", "origin", "work")
            stats.update(commit=sha, finalPush=True)
            stages.append(stats)
            if name == "a":
                transition_start = time.monotonic()
            shutil.rmtree(checkout)
            shutil.rmtree(home)
        inspector = trial / "independent-inspector"
        git(trial, "clone", "--branch", "work", str(remote), str(inspector))
        expected = expected_handoff(inspector)
        if (inspector / "result.txt").read_text() != expected:
            raise RuntimeError("independent_content")
        main_unchanged = git(remote, "rev-parse", "main").stdout.strip() == base
        failure_controls = {"agentFailureBlocked": not advance_allowed(False, True, True),
                            "pushFailureBlocked": not advance_allowed(True, False, True)}
        # A real Git transport failure, followed by the same sequencer gate.
        failed_push = git(inspector, "push", str(trial / "absent-remote"), "HEAD:work", check=False)
        failure_controls["gitPushActuallyFailed"] = failed_push.returncode != 0
        failure_controls["failedPushDidNotAdvance"] = not advance_allowed(True, failed_push.returncode == 0, True)
        path = inspector / ".konveyor/handoff.json"
        path.unlink()
        try:
            expected_handoff(inspector)
            failure_controls["missingHandoffRejected"] = False
        except (ValueError, FileNotFoundError):
            failure_controls["missingHandoffRejected"] = True
        path.write_text('{"version":1,"input_sha256":"wrong","next":"sha256"}')
        try:
            expected_handoff(inspector)
            failure_controls["invalidHandoffRejected"] = False
        except ValueError:
            failure_controls["invalidHandoffRejected"] = True
        emit({"kind": "handoff", "sample": sample, "stages": stages,
              "sourceBranchUnchanged": main_unchanged, "baseCommit": base,
              "distinctProcesses": stages[0]["pid"] != stages[1]["pid"],
              "priorWorkspaceDeleted": not (trial / "stage-a").exists(),
              "independentArtifactVerified": True, "failureControls": failure_controls,
              "transport": "local-bare-git", "hubTokenArrangement": "none-runtime-only-probe",
              "assertion": "PASS" if main_unchanged and all(failure_controls.values()) and stages[0]["pid"] != stages[1]["pid"] else "FAIL"})


def main():
    logging.basicConfig(level=logging.ERROR)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("continuity", "handoff", "stage"), required=True)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--samples", type=int, choices=range(1, 4), default=3)
    parser.add_argument("--stage", choices=("a", "b"))
    args = parser.parse_args()
    args.root.mkdir(parents=True, exist_ok=True)
    os.environ["LIGHTSPEED_PROVIDER"] = "anthropic"
    os.environ["LIGHTSPEED_MODEL"] = MODEL
    os.environ["LIGHTSPEED_PROVIDER_URL"] = os.environ["KONVEYOR_LLM_ENDPOINT"]
    os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
    emit({"kind": "provenance", "mode": args.mode, "pid": os.getpid(),
          "packages": {n: importlib.metadata.version(n) for n in ("deepagents", "langgraph", "langgraph-checkpoint", "lightspeed-agentic")},
          "scriptSHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}) if args.mode != "stage" else None
    if args.mode == "continuity":
        asyncio.run(continuity(args.root, args.samples))
    elif args.mode == "handoff":
        handoff(args.root, args.samples)
    else:
        asyncio.run(stage(args.root, args.stage))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        emit({"kind": "failure", "errorType": type(exc).__name__})
        raise SystemExit(1)
