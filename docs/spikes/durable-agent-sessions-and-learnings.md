# Spike: durable sessions and file-based learnings

Status: proposed for team review, 2026-11-11. Implements experiments for
[ADR 0024](../adr/0024-durable-agent-sessions-and-learnings.md), not a production
feature or an accepted CRD schema. No experiments described here have run.

## Question and scope

Can a deterministic harness worker process durable inputs, release compute
between completed inputs, and restore the right session state and approved
learning snapshot without silently repeating completed work?

Prove a narrow vertical slice using the existing controller/Agent Sandbox path
and the intended Lightspeed/DeepAgents runtime integration. Begin with a scripted
runtime and effect fixtures for deterministic failure tests, then use a pinned
real runtime to qualify memory delivery and continuation. A mocked checkpoint
or successful model response is not sufficient evidence of runtime recovery.

Use experimental resource shapes under a clearly isolated namespace/test setup.
Do not publish generated production CRDs or widen production RBAC as a side
effect of this spike. Reuse the existing AgentRun/AgentWorkflowRun controllers
where practical; any test doubles must be named in the results.

## Demonstration scenario

Create two sessions with independent conversations/workspaces and matching
application labels, plus a second application's session that shares one
accepted learning. Seed a short verification instruction, a longer specialized
procedure, a rejected proposal, and an accepted learning that does not match.

Deliver an event as an explicitly addressed AgentInput. The first session's
worker claims it, pins accepted matching revisions, writes a read-only index
and files, and invokes the runtime. Have it delegate an AgentRun using a stable
child identity, durably complete dispatch, and process another input while that
child continues. Child completion must eventually arrive as one logical input.

Accept a replacement learning between the two inputs. The next input sees the
replacement; a retry of the earlier input still sees the original. Separately,
create a queued AgentRun and a two-stage AgentWorkflowRun before replacement.
They must retain their creation-time sets, including the later workflow stage.

Stop session compute after completing an input. Wake fresh compute with another
input and verify conversation/context restoration, independent workspace state,
and the new input's fresh learning selection. Delete a completed run's pod and
demonstrate authorized transcript retrieval while its AgentRun still exists.

## Implementation sequence

### 1. Specify the durable records and admission boundary

Write minimal experimental schemas for session identity, immutable input
identity/payload, input progress, immutable learning revision, selection
manifest, consumer references, and action intent/result. Identify which records
are authoritative, which are projections, and which transition is a commit.
Use CR status for lifecycle and references, not unbounded transcripts.

Propose precise semantics for acceptance, ordered selection, exclusive claim,
completion, cancellation, interruption, and deletion. Include stale-worker
fencing and namespace/identity authorization. Decide whether interrupted work
blocks later inputs; use a conservative single-active-input baseline until an
explicit policy is reviewed.

For run/workflow creation, show how an invocation binds its learning selection
at acceptance rather than first reconcile or pod startup. For a session, bind
the selection before its input's first dependent model call. Demonstrate how
consumer registration and revocation coordinate even if creation fails or
either process crashes. Two independent API writes or a cached consumer count
are not an atomicity argument.

### 2. Choose a concrete storage arrangement for the experiment

Compare a persistent-volume/runtime-checkpoint arrangement with a metadata
store plus immutable artifact storage. Select one minimal arrangement and
document why it can implement the required commits. Separate inbox/status,
runtime checkpoints, action receipts, transcripts, workspace recovery, and
learning artifacts in the storage map.

State what survives process loss, pod deletion, and storage loss. Keep storage
lifetime independent of worker compute. If object storage is used, specify how
an uploaded artifact becomes referenced and how incomplete uploads/orphaned
objects are reconciled. S3 compatibility is not evidence of metadata transaction
semantics. Do not add an object-store sidecar merely to satisfy a topology idea.

Record runtime/checkpoint version compatibility and identify whether a resumed
input restores runtime state or reconstructs it from recorded results. Explicitly
reject unsupported recovery instead of silently restarting side-effecting work.

### 3. Build the worker and memory adapter

Implement the claim/restore/prepare/invoke/commit loop outside the model. Use
stable request and operation identities, preserve completed decisions, and
renew/fence execution ownership. A failed preparation must not invoke the model
with missing learning content.

Render an approved summary/relevance index and versioned full-content files,
verify digests, and configure startup memory explicitly. Keep the projection
read-only and separate from the agent's scratch memory. Exercise shared
learnings without sharing mutable workspaces. Define behavior for oversized
catalogs, conflicting required statements, unavailable content, and historical
messages containing superseded advice.

Use runtime hooks to capture advertised/delivered revisions and excerpts after
inspection/truncation. Record where coverage ends for shell reads and runtime
subagents. Agent-reported consideration is separate from observed delivery.
Show attribution surviving compaction/restart, including the runtime/prompt
version needed to interpret it. Do not claim internal model reasoning is
observable.

### 4. Inject failures at the actual commit boundaries

Automate deterministic checks with explicit expected outcomes:

1. **Acceptance reply lost:** resubmitting the same input returns the existing
   logical request; a changed payload under that identity is rejected.
2. **Worker replaced after claim:** the replacement resumes; the stale worker
   cannot commit progress. Document external-effect fencing limitations.
3. **Crash before/after selection commit:** before commit, preparation may
   retry; after commit, recovery restores the exact selected revisions despite
   accepted replacements or workload-label changes.
4. **Crash after retrieval:** subsequent reasoning reuses the recorded content,
   including any truncation/filtering, rather than searching current knowledge.
5. **Child created before reference saved:** recovery finds the original child;
   it does not create a duplicate. Include retained-outcome behavior after
   child garbage collection.
6. **External effect succeeds before receipt commit:** an idempotent fixture
   yields one logical effect; an unqueryable non-idempotent fixture produces a
   visible interruption rather than a blind retry.
7. **Input completed before acknowledgment:** duplicate notification returns
   the saved outcome without another model invocation or external action.
8. **Child completion notification lost:** reconciliation/outbox recovery
   delivers the outcome once logically, including repeated transport delivery.
9. **Concurrent selection and revocation:** no successful revocation overlaps
   an unfinished referencing consumer. Queued runs and workflows between stages
   count. Rejected revocation must not silently disable future selections.
10. **Cancellation/deletion during work:** learning use is not released while
    an old worker can still act. Required evidence survives cleanup; artifacts
    remain retrievable for the owning resource's promised lifetime.
11. **Input arrives during idle shutdown:** the durable input remains pending
    and reconciliation wakes or retains exactly one authorized worker. A missed
    in-memory notification must not leave accepted work stranded.

Distinguish a retried transport message, a resumed attempt, and a new logical
input in the evidence. Publish identifiers and outcomes, not just screenshots
of a successful conversation.

### 5. Qualify the real runtime and context policy

On the pinned Lightspeed/DeepAgents integration, demonstrate a completed-input
sleep/wake cycle, fresh input preparation while already awake, and at least
one recoverable mid-input interruption. Preserve existing approval, ask-user,
steering/cancellation, execution-limit, handoff, and inspection behavior as
integration requirements; report any untested capability explicitly.

For the same model, tasks, and approved corpus, compare all-upfront,
catalog/read-on-demand, and hybrid delivery. Use both a small collection and a
collection large enough to require selective access. Include a required
verification instruction, an applicable procedure, distracting material, a bad
proposal excluded by review, and evidence omitted from a summary.

Measure task success, required-instruction adherence, relevant lessons missed,
actual delivered revisions, token/tool cost, and latency. Repeat enough trials
to expose model variability and report counts/configuration; this is a bounded
evaluation, not a general leaderboard claim. Choose defaults from outcomes,
not token savings alone. Exact budgets and the catalog format are outputs.

## Deliverables and exit criteria

Provide a reproducible branch/reference with pinned dependencies, setup and
teardown instructions, experimental schemas, the chosen storage map, a state
transition diagram, and automated failure tests. Include an evidence report
separating fixture results from real-runtime results, and measured behavior
from intended guarantees.

A recommendation to proceed requires:

- durable acceptance, exclusive execution, and restart between completed inputs;
- creation-time run/workflow selections and per-input session selections that
  remain stable through retries;
- demonstrated replacement and race-safe refusal of revocation during use;
- read-only file delivery with attributable model exposure and honest coverage
  limits;
- delegation while processing later inputs, recoverable child notification,
  and visible handling of uncertain external effects;
- transcript access after pod removal and a stated retention/cleanup contract.

If the tested runtime cannot preserve progress at a required boundary, report
that limitation and the smallest upstream/runtime or design change needed. Do
not relabel whole-prompt replay as successful recovery. The report should
recommend production slices only after these boundaries are understood.

## Decisions to return for review

Resolve storage and transaction ownership, public fields/conditions, input
ordering/interruption behavior, cancellation and retention, runtime event
coverage, context budgets, and credential renewal/cleanup. Clarify learning
selection for independently delegated invocations: the current proposal uses
new-invocation selection, while workflow stages inherit their parent's fixed
set. Verify that this matches expected delegation semantics.

Keep a general event router/classifier product, production Jira/PR/CVE adapters,
automatic learning extraction, IDE/CLI attachment UX, and VM snapshot backends
outside this spike. A small fan-out fixture can test addressed delivery without
building those integrations. Duration and ownership can be estimated after
the team reviews the intended guarantees and available runtime integration.
