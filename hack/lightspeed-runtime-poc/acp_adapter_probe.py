#!/usr/bin/env python3
"""First-party ACP adapter smoke with deterministic graphs, no model or sockets.

Run with pinned deepagents-acp and ACP SDK wheels added to sys.path. This tests
package/API compatibility in the locked runtime, not Lightspeed graph assembly,
network reconnect, multi-viewer delivery, or durable storage.
"""
import asyncio
from collections import Counter
import importlib.metadata
import json
import time

from acp.schema import TextContentBlock
from deepagents_acp.server import AgentServerACP
from langchain_core.messages import AIMessage
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import END, START, MessagesState, StateGraph


class Client:
    def __init__(self):
        self.updates = []

    async def session_update(self, **kwargs):
        self.updates.append(kwargs["update"].model_dump(mode="json", by_alias=True))


def compile_graph(node):
    graph = StateGraph(MessagesState)
    graph.add_node("respond", node)
    graph.add_edge(START, "respond")
    graph.add_edge("respond", END)
    return graph.compile(checkpointer=InMemorySaver())


async def main():
    async def respond(state):
        return {"messages": [AIMessage(content="synthetic-ack")]}

    graph = compile_graph(respond)
    server = AgentServerACP(graph, load_sessions=True)
    client = Client()
    server.on_connect(client)
    initialized = await server.initialize(protocol_version=1)
    session = await server.new_session(cwd="/workspace/acp-synthetic", mcp_servers=[])
    for _ in range(2):
        result = await server.prompt(session_id=session.session_id,
            prompt=[TextContentBlock(type="text", text="synthetic-input")])
        assert result.stop_reason == "end_turn"
    state = await graph.aget_state({"configurable": {"thread_id": session.session_id}})
    human_count = sum(m.type == "human" for m in state.values["messages"])
    assert human_count == 2
    reattached = AgentServerACP(graph, load_sessions=True)
    viewer = Client()
    reattached.on_connect(viewer)
    await reattached.load_session(cwd="/workspace/acp-synthetic", session_id=session.session_id,
                                 mcp_servers=[])
    kinds = Counter(u["sessionUpdate"] for u in viewer.updates)
    assert kinds["user_message_chunk"] == 2 and kinds["agent_message_chunk"] == 2
    print(json.dumps({"kind": "acp-adapter", "check": "same-process-new-prompt-load",
        "packages": {n: importlib.metadata.version(n) for n in ("deepagents-acp", "agent-client-protocol", "deepagents")},
        "protocolVersion": initialized.protocol_version,
        "loadAdvertised": initialized.agent_capabilities.load_session,
        "priorHumanMessages": human_count, "replayUpdates": dict(kinds), "assertion": "PASS"}), flush=True)

    for sample in range(1, 4):
        entered, release = asyncio.Event(), asyncio.Event()
        effects = []

        async def slow_node(state):
            entered.set()
            await release.wait()
            effects.append("node-effect")
            return {"messages": [AIMessage(content="finished")]}

        slow = AgentServerACP(compile_graph(slow_node))
        slow.on_connect(Client())
        sid = (await slow.new_session(cwd="/workspace/acp-synthetic", mcp_servers=[])).session_id
        task = asyncio.create_task(slow.prompt(session_id=sid,
            prompt=[TextContentBlock(type="text", text="start")]))
        await asyncio.wait_for(entered.wait(), 5)
        start = time.monotonic()
        await slow.cancel(session_id=sid)
        await asyncio.sleep(0.1)
        blocked_after_cancel = not task.done()
        effects_before_release = len(effects)
        release.set()
        result = await asyncio.wait_for(task, 5)
        assert blocked_after_cancel and effects_before_release == 0 and len(effects) == 1
        assert result.stop_reason == "cancelled"
        print(json.dumps({"kind": "acp-adapter", "check": "cancel-blocked-node", "sample": sample,
            "promptStillBlockedAfterCancel": blocked_after_cancel, "observationWindowSeconds": 0.1,
            "nodeEffectsAfterCancel": len(effects), "stopReasonAfterRelease": result.stop_reason,
            "cancelToCompletionSeconds": round(time.monotonic() - start, 3),
            "assertion": "PASS", "meaning": "cancel flag does not interrupt a blocked graph node"}), flush=True)


if __name__ == "__main__":
    asyncio.run(main())
