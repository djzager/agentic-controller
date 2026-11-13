# AgentSession design handoff

Updated: 2026-11-13. This is the starting point for continuing the design
conversation, not an implementation-complete report.

## Where the work lives

- Fork: <https://github.com/djzager/agentic-controller>.
- Branch: `docs/durable-agent-sessions`.
- Local worktree: `.worktrees/docs--durable-agent-sessions`.
- The branch started from upstream main; its current local base is `369070c`.
  Fetch and inspect upstream changes before preparing an eventual PR.
- AgentLearning is a separate proposal on `docs/agent-learnings`, with
  [PR #346](https://github.com/konveyor/agentic-controller/pull/346). It was open
  and ready for review when checked on 2026-11-13. Do not alter that branch as
  part of resuming session design without a reason to do so.

This branch preserves the conversation's development history, including the
original combined learning/session proposal. Companion learning documents are
still present here. They are not a new combined proposal: reconcile against
the independent learning PR before assembling the eventual session PR. No
history rewrite or upstream rebase was needed to publish this discussion branch.

## Read these first

1. Repository `AGENTS.md` and [CONTEXT.md](../../CONTEXT.md).
2. [Session trigger discussion frame](../research/2026-11-11-session-trigger-design.md).
3. [ADR 0025](../adr/0025-agent-sessions-and-event-routing.md).
4. [Required PoC and qualification plan](../spikes/agent-session-event-routing.md).
5. [Connections and Slack research](../research/2026-11-13-agent-connections-and-slack.md)
   for the most recent discussion.

The ADR is a living exploratory draft with dated amendments. Early passages
propose a bespoke receiver, SQLite, and managed repository webhooks; those
remain prototype candidates, not settled production choices. Read the later
amendments and discussion frame before treating an earlier implementation sketch
as a requirement. The November 11 [session slides](../slides/agent-sessions-2026-11-11.md)
predate the later input, confidentiality, and connection discussions and need
revision before being presented as current.

## Current state

The branch contains documentation, research, and slides only. AgentInput,
AgentSession, event bindings, the receiver/admission path, and the demo are not
implemented. No GitHub webhook or Slack connection was registered during this
discussion, and no demo review or message was published. External documentation
was researched; no eventing platform or provider behavior was integration-tested.

Documentation checks have included `git diff --check`, local link/fence checks,
and `make verify-adr-index`. These do not qualify runtime behavior. The worktree
was clean before the handoff was added; unrelated worktrees and the main
checkout's untracked research notes must be preserved.

## Decisions and requirements established in the conversation

### Keep execution generic

The controller remains a stateless CR reconciler, dependent on Agent Sandbox.
It has no application, archetype, repository, GitHub, Jira, or Slack domain model
and does not proxy ACP or hold event-provider connections. Hub/UI can own
application/archetype intent and project it into concrete configuration. Provider
adapters own authentication, subscriptions, event interpretation, and provider
recovery. The harness interprets task data and manages the runtime.

The inspected Hub checkout (`3fecffe`) has application repository data and
archetype membership resolution. No AgentRun creation path was found in that
checkout's inspected Go sources. ADR 0006 describes the intended Hub integration;
do not present all of it as already implemented there. The current harness does
resolve application data through Hub, and run APIs expose generic parameters,
environment/Secret references, and file mounts.

### AgentInput spans finite and continuing work

The proposed AgentInput is a durable request that selects an Agent to create an
AgentRun, selects an AgentWorkflow to create an AgentWorkflowRun, or addresses
an existing AgentSession. It records its execution and outcome. It is not an
unrelated-work queue attached to an existing finite run. Existing direct run
creation remains valid without requiring a new wrapper everywhere.

An AgentSession can be a specialist or the routing agent itself. Deterministic
receipt, authorization, and input admission are separate from model judgment.
No separate classifier CRD has been selected. Sleeping between completed inputs
is sufficient initially; preserve room for future runtime backends without
requiring microVM suspension. A session can record a delegation and accept another
input while the child runs, then receive the child's correlated outcome later.

### Preserve work and make uncertainty visible

The agreed principle is: preserve completed work, recover unfinished work where
possible, and make uncertainty visible. Transport duplicates, duplicate logical
work, and duplicate external effects are different problems. Freeze routing
intent/destination plans, retain stable identities through the recovery horizon,
and recover partial fan-out without selecting new destinations on retry. Do not
claim exactly-once external publication or universal undo. Unknown outcomes may
require intervention instead of repeating an unsafe action.

### PR review is the first useful example

A GitHub PR event should produce an AgentInput, a finite review AgentRun, and a
published review attributed to the examined revision. New commits normally
request another review. Eligibility, older-review handling, queued revision
coalescing, and stale publication should be configurable per repository/task.
The specific defaults for older work are not settled.

Scheduling/publication policy comes from a trusted base revision. A PR cannot
change its own policy by editing its configuration file. Repository overrides
remain bounded by centrally enforced settings. Record effective policy and
configuration revision on the input so retries do not adopt changed behavior.
Exact fields, filename, and merge rules remain open.

Review history must distinguish an unchanged finding from a new finding and
retain provider thread/publication identifiers across runs. This task state does
not itself require an AgentSession. Session value must be shown through useful
continuity across inputs, human decisions, and outstanding delegations.

### A real demo is required before a review-ready proposal PR

The user explicitly requires runnable PoC code/configuration and evidence from
a real GitHub repository in the eventual proposal PR. Include source setup,
actual agent execution and review publication, a new commit, duplicate delivery
after receiver restart, and a repeated-finding example. Provide setup, inspection,
and cleanup instructions and describe remaining limitations. A plan, fixtures,
slides, or a hand-created input alone do not satisfy this requirement.

The demo still needs a selected test repository, deployment environment, runtime,
provider access, and setup mode. The present request authorizes publishing this
discussion branch; it does not identify a repository to register hooks on or a
destination for bot comments. Resolve those concrete details when starting the
live experiment. The first demo can use standalone configuration; a complete
Hub/UI feature is not required, but its proposed mapping must be documented.

### Embargoed work is a required use case

Restricted work must be able to enter the queue. Opaque means uninterpreted,
not confidential. Protection must extend through receipt storage, execution,
transcripts, workspace/results, derived learnings, and publication. Application
membership alone does not authorize a destination to receive restricted input.

Secret-backed input is the first concrete mechanism being evaluated. Existing
AgentRun/AgentWorkflowRun file mounts can deliver same-namespace Secret keys as
read-only files. Input authorization, stable content identity, retention,
replacement/deletion handling, and session-time delivery are still new work.
An immutable Secret can still be deleted/recreated; its name alone is not enough.
Secret-backed task content and connector credentials are separate concerns.
Separate restricted sessions/runs are recommended but not yet agreed as the
specific isolation mechanism. Test this design with synthetic confidential data.

### AgentLearning remains separate

Runs select learnings at invocation, workflows keep one fixed set, and sessions
refresh before each new input through deterministic harness work. File-based
delivery and selective loading are the proposed approach. Learning content is
immutable, approval is explicit, replacements affect future selections, and
revocation waits for unfinished consumers. Refer to the independent
[learning ADR](../adr/0024-agent-learnings.md) and PR rather than expanding its
scope here. IDE/CLI continuation and application-wide history are future design
considerations, not the first interaction to implement.

## Latest discussion and unanswered questions

Grok Bot's documented separation of connected tool accounts, routine triggers,
requester identity, and reply conversations is useful. Slack can deliver via
HTTP or an outbound Socket Mode adapter. Neither a connected account nor an
authenticated event automatically authorizes every caller, execution target,
or publication destination. No Grok Bot exactly-once/replay guarantee was found
in the inspected product documentation.

The last question was whether execution identity should be selectable per
binding: a service identity or the requesting user's delegated identity. The
recommendation was explicit per-binding choice with service credentials as the
background-automation default. The user asked to preserve/push this discussion;
do not treat that as selection of an identity model or new Connection CRD.

Other open decisions, to take one at a time:

- Does this project expose a reusable generic event-binding CR, or do integrations
  own bindings and submit AgentInputs directly? A generic CR was recommended,
  not accepted. Provider-specific lifecycle need not run in the core controller.
- How does Hub project archetype/application automation, including shared
  repositories, membership changes, provenance, and intentional fan-out?
- Which connector/bootstrap path proves the first GitHub demo: managed hook,
  existing hook, or a GitHub App? Managed hook setup is not adopted as a core
  controller feature.
- Which permissions allow a caller to reference protected input, and which
  trusted authority sets restrictions? What isolates session memory and
  authorizes release of results?
- What storage, concurrency, retention, ordering, and recovery contracts are
  actually demonstrated by the PoC? Compare existing Argo/Knative mechanisms
  with a small adapter before committing to infrastructure or public schemas.

## Suggested continuation

Resume the research-and-grilling discussion at the generic binding and execution
identity questions. Use the repository's `grill-with-docs` skill; ask one focused
question at a time and distinguish decisions from recommendations. Keep the
first implementation focused on the complete GitHub review path. Do not reopen
all prior research, assume a full Hub implementation, or turn every internal
record into another CRD.

Before making a ready-for-review PR, reconcile this branch's inherited learning
material against PR #346, update the dated slides if they are included, and build
and run the required demo. Preserve ADR history with dated amendments; generate
and verify ADR indexes after metadata changes. Keep this worktree while design
continues; remove it with `git worktree remove` only when merged or abandoned.
