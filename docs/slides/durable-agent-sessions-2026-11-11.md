---
marp: true
theme: konveyor
size: 16:9
paginate: true
title: Durable agents for application maintenance
description: Target use cases, proposed architecture, and spike for ADR 0024
author: David Zager
footer: 'ADR 0024 · Proposed'
style: |
  section {
    font-family: Arial, Helvetica, sans-serif;
    font-size: 30px;
    line-height: 1.35;
    padding: 48px 64px 72px;
  }
  h1 { font-size: 44px; margin: 0 0 28px; line-height: 1.15; }
  h2 { font-size: 31px; margin: 12px 0 10px; }
  p { margin: 0 0 22px; }
  li { margin-bottom: 16px; }
  footer, section::after { font-size: 18px; color: #a0a0a0; }
  section.lead { background: #1a1a2e; }
  section.lead h1 { font-size: 56px; max-width: 1040px; margin: 24px 0; }
  section.lead p { font-size: 30px; }
  section.lead img { width: 200px; height: auto; }
  table { display: table; font-size: 27px; line-height: 1.25; }
  table th, table td { padding: 15px 17px; }
  section.compact table { font-size: 25px; }
  section.compact li { margin-bottom: 12px; }
  section.references { font-size: 26px; }
  section.references li { margin-bottom: 17px; }
  pre { font-size: 25px; line-height: 1.5; }
  code { font-size: 0.88em; }
  blockquote { margin: 22px 0 0; font-size: 29px; border-radius: 0; }
---

<!-- _class: lead -->
<!-- _paginate: false -->
<!-- _footer: '' -->

![Konveyor](konveyor-logo.svg)

# Durable agents for application maintenance

Target use cases and proposed design

November 11, 2026

<!--
This is a team review of ADR 0024, not a demonstration of implemented behavior.
Begin with the work we want to support. The resource names and implementation
details matter only insofar as they satisfy those use cases.

Source: docs/adr/0024-durable-agent-sessions-and-learnings.md, proposed on 2026-11-11.
Logo: existing repository asset docs/slides/konveyor-logo.svg.
-->

---

# CVE remediation across applications

An advisory affects dependencies in **several applications**.

1. Identify affected applications and start remediation for each.
2. Coordinate each application's fix and track the result.
3. Continue handling other requests while individual fixes run.

**Success:** every affected application has a traceable outcome.
A reviewed lesson from one fix can inform another.

<!--
Target scenario, not an existing integration. The router or Hub understands
inventory and vulnerability applicability. The controller does not interpret
CVE or application data. Partial fan-out must be recoverable: the source event,
destinations, and stable input identities must survive a router restart.

The input that dispatches a fix can complete once the delegation is durable.
That does not assert that the fix itself succeeded. A child outcome returns
through a separate durable input.

Source: ADR 0024, sections 1 and 3. This scenario came from the design discussion.
-->

---

# Pull-request review over time

A review bot returns when a pull request opens or changes.

- Apply approved repository conventions and prior lessons.
- Keep context for reviewing follow-up revisions.
- Work independently from migration or maintenance activity.

**Success:** review resumes when needed, with shared knowledge and its own working context.

<!--
Target scenario, not a commitment to automatic publication of review comments.
Existing authorization and human approval policies still apply to effects.

The same application may also have maintenance or migration sessions. Shared
knowledge does not require a single conversation or concurrent writes to one
workspace. Pull-request event adapters are above the generic controller.

Source: ADR 0024, sections 1, 5, and 7; the review-bot example was part of the
design discussion.
-->

---

# Work triggered by a Jira issue

When an issue is created, start the appropriate application work.

- Retain the request until handled, even if delivery is repeated.
- Work directly or start a finite task and follow its result.
- Let later events or human messages continue the work.

**Success:** an accepted request survives a lost connection or restart.

<!--
This is a target integration, not a statement that Jira routing already exists.
The adapter is responsible for authentication, destination selection, and a
stable source-event identity. Re-delivering the same event should produce the
same logical input. A changed request under the same identity is a conflict.

Human follow-up submitted through ACP can also become a new input. Active-turn
approval replies, steering, and cancellation remain control interactions rather
than requests queued behind the work they are controlling.

Source: ADR 0024, sections 1 and 3; Jira-triggered work was an initial use case
in the design discussion.
-->

---

# Requirements shared by these use cases

- **Continuity:** identity and context outlive a pod.
- **Durable delivery:** accepted inputs remain actionable.
- **Delegation:** a session can keep working while children run.
- **Shared knowledge:** reviewed lessons inform independent workloads.
- **History:** requests, outcomes, and transcripts remain inspectable.

The first version sleeps **between completed inputs**.

<!--
These use cases motivate the primitives; they do not require process-memory
freezing. We need persistence and wake-up around a known boundary, not a
mandatory microVM snapshot facility.

Application history is a Hub/consumer view over generic references and labels.
Git retains code evolution, but it is not the sole record of decisions, inputs,
outcomes, or accepted/rejected learnings.

Source: ADR 0024, sections 2 and 7.
-->

---

# The proposed resources

| Resource | Purpose |
| --- | --- |
| **AgentSession** | Continuing identity, conversation, and workspace |
| **AgentInput** | One durable request addressed to one session |
| **AgentLearning** | A reviewable lesson with immutable content and applicability |

**AgentRun** and **AgentWorkflowRun** remain finite invocations.

<!--
All three new names are proposed and unimplemented. Exact public schemas and
conditions are outputs of the spike. An Agent remains a reusable template.
AgentInput conditions report acceptance and completion; writing a condition
does not itself implement durability.

No AgentAction CRD is proposed. Start with an internal action journal and justify
a public action resource only if it needs independent lifecycle management.

Source: ADR 0024, sections 1 and 4; CONTEXT.md, Proposed Durable Execution Concepts.
-->

---

# Controller, session worker, agent runtime

| Responsibility | Owner |
| --- | --- |
| Watch resources and wake compute | **Controller** |
| Claim inputs, prepare learnings, persist progress | **Session worker in the harness** |
| Reason, use tools, selectively read prepared memory | **Agent runtime** |

Fetching the next input and refreshing learnings happen **outside the agent invocation**.

<!--
The session worker is a deterministic harness responsibility, not necessarily
another process or service. The controller remains stateless, domain-agnostic,
and outside ACP connections. Application classification/routing stays above it.

The intended runtime target remains the Lightspeed/DeepAgents integration from
ADR 0021. Referring to Claude, Goose, Pi, or DeepAgents does not mean every
runtime has qualified checkpoint or observation support.

Source: ADR 0024, section 2; ADRs 0007 and 0021.
-->

---

<!-- _class: compact -->

# Processing one input

1. **Claim** the input and inspect saved progress.
2. **Pin** learnings for new work; restore the snapshot for a retry.
3. **Prepare** memory files, context, and workspace.
4. **Invoke or resume** the agent runtime.
5. **Persist** required results and session state.
6. **Complete** the input, then take the next one or sleep.

Taking an item off the worker queue does **not** prove completion.

<!--
Refresh before each new input even when the worker is already awake. Do not
refresh on every reconcile: unfinished work keeps its committed selection.
If preparation fails before that selection is committed, preparation can retry;
the agent has not started dependent work yet.

The AgentInput CR remains the durable record. Exclusive execution requires an
enforced ownership guard; lease expiration alone does not stop the old process.
Invoking or resuming may preserve the logical conversation rather than create
a new conversation for every input.

Source: ADR 0024, sections 1 and 2.
-->

---

# Delegation and subsequent inputs

| Session activity | Delegated run |
| --- | --- |
| Input A: record and dispatch a CVE fix | Created with a stable identity |
| Complete the dispatch input | Continues executing |
| Input B: handle another request | Still executing |
| Input C: process the child outcome | Completed |

Recovery must find the **existing child**, not create another one.

<!--
This is a sequence, not measured timing or a claim of parallel inputs within
one session. The initial model has one active input per session. Child work can
run concurrently with that session's next input.

The child owns its lifetime and learning references after dispatch completes.
Child completion needs durable notification or reconciliation; a transient
WebSocket notification is insufficient. Retained outcomes must cover child
garbage collection before the parent recovers.

Source: ADR 0024, section 3.
-->

---

# Learning selection boundaries

| Consumer | When the set is pinned |
| --- | --- |
| **AgentRun** | At creation, before any queueing delay |
| **AgentWorkflowRun** | At creation; every stage inherits the same set |
| **AgentSession** | Before each new input, including while already awake |

**Retries retain the original snapshot.**

<!--
Selection combines acceptance, authorization, and generic applicability labels.
Hub supplies application meaning; the controller need not understand it.
Labels do not themselves grant access. A snapshot must retain exact immutable
revisions/content, not just a selector or a pointer to latest.

Creation-time binding and consumer registration must coordinate with revocation.
An eventual lookup at first pod startup is too late. The admission/transaction
mechanism remains a spike question. Independently delegated invocations follow
new-invocation selection under the proposal; workflow stages inherit their set.

Source: ADR 0024, section 5.
-->

---

# Immutable learning lifecycle

**Propose → review → accept or reject**

- Approval covers the exact content **and applicability**.
- An accepted replacement changes **future selections**.
- Existing consumers keep their pinned revisions.

> Revocation is refused until every unfinished referencing consumer has finished or actually stopped/cancelled.

<!--
Where human approval is required, the authenticated human decision must be
recorded. An agent can propose a lesson or replacement; it cannot declare its
own proposal human-approved. Rejected proposals remain history.

Queued runs and entire workflows between stages count as consumers. This is
not disable-now-and-drain behavior. Rejected revocation must not silently change
selectability. Active-use registration and revocation need a race-safe boundary.

Source: ADR 0024, section 5.
-->

---

# Memory files and selective delivery

```text
memory/index.md                   summaries and paths
memory/learnings/<revision>.md    full learning content
memory/manifest.json              identities and digests
```

Load a concise approved index and essential guidance automatically.
Read longer procedures and supporting evidence when relevant.

**Read-only projection of the pinned set. No MCP required.**

<!--
Illustrative paths, not a prescribed schema or automatic DeepAgents convention.
The harness configures startup memory explicitly and generates the index from
approved summaries rather than inventing new summaries at startup. Metadata,
relevance description, full content, and evidence references stay associated.
The manifest is bookkeeping, not prompt text.

Favor one reusable lesson per record. Keep detailed evidence separate, show
size during review, and introduce subordinate indexes as the collection grows.
Do not silently truncate required guidance. Exact budgets/defaults need testing.

Source: ADR 0024, section 6; DeepAgents memory documentation:
https://docs.langchain.com/oss/python/deepagents/memory
Anthropic, The components of agent memory, November 6, 2026:
https://www.anthropic.com/research/components-of-agent-memory
Research supports evaluating selective delivery, not a universal winning policy.
-->

---

# Learning delivery evidence

| Record | What it establishes |
| --- | --- |
| **Available** | The selected revision was materialized |
| **Advertised** | Its summary entered the model's context |
| **Delivered** | Full content or an identified excerpt reached the model |
| **Agent-reported consideration** | The agent explicitly reported applicability or conflict |

A file read does not prove the agent followed the learning.

<!--
The harness persists attribution using runtime events. Observe the actual
model-input/tool-result boundary after inspection and truncation. Filesystem
access alone can include reads by another process or content never shown to the
model. Coverage for shell reads and runtime subagents needs qualification.

Consideration is a structured report, not proof of internal reasoning. Actual
compliance needs task-specific verification where possible. Keep revision IDs,
input/attempt IDs, and relevant tool/model-call references in the evidence.

Source: ADR 0024, section 6.
-->

---

# What survives compute replacement

- Accepted inputs, saved decisions, and action results
- The input's pinned learning snapshot
- Session context and recoverable workspace state
- Correlated history and transcripts while their AgentRuns exist

> Unknown external outcome: reconcile or surface an interruption. Do not blindly repeat the action.

<!--
Persist action intent before dispatch and the receipt before depending on its
completion. Completed recorded operations can be reused. If an external effect
succeeded before its receipt was saved, storage cannot automatically determine
the outcome. Use the destination's lookup/idempotency contract or intervene.
Compensation is action-specific, not universal undo.

Conversation checkpoints, user-visible inspected transcripts, and the action
journal serve different purposes. The storage backend, transaction boundary,
retention after deletion, and transcript endpoint remain open. A bucket alone
does not provide input ownership or atomic external effects.

Source: ADR 0024, sections 4 and 7; docs/research/2026-11-10-durable-session-guarantees.md.
-->

---

# Proposed spike

**One complete path:** accept → claim → prepare → invoke → persist → sleep/wake

- Prove per-input refresh and stable retry snapshots.
- Exercise delegation while the session handles later work.
- Interrupt claims, effects, completion, and idle shutdown.
- Test queued/workflow consumers against replacement and revocation.
- Qualify real-runtime memory delivery and retained transcripts.

Return reproducible evidence, API/storage proposals, and a go/no-go recommendation.

<!--
Begin with deterministic runtime/effect fixtures, then qualify a pinned real
Lightspeed/DeepAgents integration. A mocked checkpoint is not evidence of real
runtime recovery. Compare all-upfront, selective, and hybrid context policies on
the same tasks/model/corpus; measure task outcomes and adherence alongside cost.

The spike plan contains eleven failure scenarios, including stale-worker writes,
lost acceptance replies, lost child notifications, uncertain external effects,
and input arrival during idle shutdown. Preserve existing approval, ask-user,
steering/cancellation, execution-limit, handoff, and inspection requirements.

Source: docs/spikes/durable-agent-sessions-and-learnings.md.
-->

---

# Review decisions

**Review now**
Target use cases, resource boundaries, worker ownership, snapshot timing,
and the immutable learning lifecycle.

**Resolve through the spike**
Storage and transaction mechanics, API conditions, ordering/interruption,
memory budgets, runtime recovery, and observation coverage.

IDE/CLI continuation and future execution backends remain follow-ups.
The first version does not require VM suspension.

<!--
The immediate request is review of a proposed ADR and authorization/scoping of
the experiment, not approval to ship a production API. Team prioritization of
the target use cases can guide the first fixture and subsequent integrations.

Agent Sandbox remains a hard dependency. Future Agent Substrate/Agent Executor
backends should be able to implement the same behavioral contract, but their
adoption and checkpoint compatibility are unqualified. A general event router,
production Jira/PR/CVE adapters, and automatic learning extraction are outside
the first spike.

Source: ADR 0024, Relationship to existing decisions and Proposed next step;
docs/spikes/durable-agent-sessions-and-learnings.md, Decisions to return for review.
-->

---

<!-- _class: references -->

# Design and research references

- **ADR 0024:** Durable Agent Sessions, Inputs, and Learnings
- **Spike plan:** `docs/spikes/durable-agent-sessions-and-learnings.md`
- [DeepAgents: memory files and selective loading](https://docs.langchain.com/oss/python/deepagents/memory)
- [Anthropic: The components of agent memory](https://www.anthropic.com/research/components-of-agent-memory)
- [Cloudflare: conversation state and memory](https://developers.cloudflare.com/agents/concepts/conversation-state-and-memory/)

Full evidence and limitations are in `docs/research/`.

<!--
Primary design source: docs/adr/0024-durable-agent-sessions-and-learnings.md.
Related decisions: ADR 0001 (architecture), ADR 0007 (harness), ADR 0014 (skill
delivery), ADR 0021 (shared runtime and conditional Lightspeed execution).

Research notes:
docs/research/2026-11-10-durable-session-guarantees.md
docs/research/2026-11-11-durable-agent-learning-delivery.md

The notes distinguish documentation and source inspection from executed tests.
Cloudflare's memory APIs are evolving; vendor examples do not establish our
immutable approval or recovery guarantees. No research benchmark or proposed
spike experiment was run as part of this design review.
-->
