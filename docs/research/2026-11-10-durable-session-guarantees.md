# Durable session delivery and action completion

Research date: 2026-11-10. This is a research note, not an ADR or an
implementation commitment. `AgentSession` and its input resource are proposed
concepts; their schemas and storage have not been selected. The proposed
contract is now captured in [ADR 0025](../adr/0025-agent-sessions-and-event-routing.md).

## Research question and design direction

What guarantees let a continuing agent accept inputs, sleep between completed
inputs, delegate work to AgentRuns or AgentWorkflowRuns, and recover without
silently repeating completed work?

The guiding principle is: preserve completed work, recover
unfinished work where possible, and make uncertainty visible. A session may work
directly or delegate. After durably recording a delegation, it can process another
input while the delegated invocation continues. Its eventual outcome can become
another input. Future IDE/CLI interaction and execution backends remain design
considerations, rather than prerequisites for the first version.

The current accepted architecture still leaves extended conversations and recovery
after process loss as follow-ups. The controller remains a stateless,
domain-agnostic reconciler, and workload interactions belong outside it. This note
explores an additional contract without changing those decisions. See
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md).

## What Cloudflare actually provides

### Queues: durable transport with bounded retention

Cloudflare Queues documents at-least-once delivery: consumers must tolerate
duplicates. It recommends a producer-supplied identity that can also serve as a
database key or an external service's idempotency key.
[Delivery guarantees](https://developers.cloudflare.com/queues/reference/delivery-guarantees/)

A successful queue write accepts the message into the transport. Delivery order
is not guaranteed. Neither fact establishes that application work has completed.
[How Queues works](https://developers.cloudflare.com/queues/reference/how-queues-works/)

Consumers can acknowledge messages individually. Explicitly acknowledged messages
are not redelivered because a later item in the batch fails. Failed delivery is
retried according to configuration; the default is three retries.
[Acknowledgment and retries](https://developers.cloudflare.com/queues/configuration/batching-retries/)

This is not indefinite input storage. As documented on the research date,
retention is configurable up to 14 days on the paid plan and fixed at 24 hours on
the free plan; expired messages are deleted. The maximum consumer wall time is
15 minutes, so one consumer invocation is also not an unlimited agent execution.
[Queue limits](https://developers.cloudflare.com/queues/platform/limits/)

Retry exhaustion deletes the message unless a dead-letter queue is configured.
A dead-letter queue is another queue, not a permanent archive: its documentation
specifically describes deletion after four days when it has no active consumer.
Retention configuration therefore needs explicit verification in any deployment.
[Dead-letter queues](https://developers.cloudflare.com/queues/configuration/dead-letter-queues/)

**Implication for this project:** a transport may notify us that an input exists,
but a longer-lived accepted input needs an authoritative record and a recovery
path independent of the transport's retention window.

### Durable Objects: identity, local durable state, activation

A Durable Object has a unique identity and private transactional, strongly
consistent storage. This is a useful reference for the continuing session and its
state, separate from transport delivery.
[Durable Object storage](https://developers.cloudflare.com/durable-objects/api/sqlite-storage-api/)

Hibernation discards in-memory state. A subsequent request or event reconstructs
the object and invokes its handler. Persistent state must therefore contain what
the object needs after reactivation. This resembles the proposed first version's
sleep between completed inputs; it does not imply a preserved process-memory
snapshot.
[Lifecycle](https://developers.cloudflare.com/durable-objects/concepts/durable-object-lifecycle/)

Storage input gates control interleaving around storage operations. Output gates
hold outgoing responses and fetch requests until pending storage writes finish.
External I/O still permits interleaving, so a single-threaded object does not
automatically serialize an entire multi-await operation. These facilities support
persist-before-confirm behavior; they do not provide a transaction encompassing an
arbitrary remote API and the object's database.
[Rules of Durable Objects](https://developers.cloudflare.com/durable-objects/best-practices/rules-of-durable-objects/)

Persisted alarms can wake an object without another client request. Alarm handlers
have at-least-once execution and bounded automatic retries: the documented policy
allows up to six retries with exponential backoff. An application can use alarms
with stored work records, but should not equate an alarm with unlimited successful
delivery of that work.
[Alarms](https://developers.cloudflare.com/durable-objects/api/alarms/)

### Workflows: persisted step results and explicit recovery rules

Cloudflare Workflows provides individually retryable steps and persisted results.
Deterministic step names identify cached results so later execution can reuse
completed steps. Mutable local variables are not a substitute for returned,
persisted step state. Its documentation explicitly warns that an external API can
commit an operation even when the step subsequently fails; retryable side effects
still need idempotency or reconciliation.
[Rules of Workflows](https://developers.cloudflare.com/workflows/build/rules-of-workflows/)

Saga rollback handlers are optional compensation attached to steps. When a
workflow fails, registered handlers run in reverse step-start order; a failed
step can participate even without an output. Compensation has its own retries
and timeout configuration. This is an application-defined response to failure,
not automatic reversal of every external effect.
[Rollback handlers](https://developers.cloudflare.com/workflows/build/sleeping-and-retrying/#register-rollback-handlers)

Cloudflare's Workflows product is distinct from this repository's AgentWorkflow
template and AgentWorkflowRun invocation. Similar terminology does not establish
equivalent execution guarantees.

### Agents SDK: an especially relevant recovery model

The current `cloudflare/agents` main-branch documentation describes `startFiber()`
as a durable acceptance API. It retains a record before executing the callback,
deduplicates by an optional idempotency key, and exposes status inspection.
After eviction, managed work is marked interrupted and a recovery hook receives
the last saved snapshot. The original closure cannot be replayed automatically;
application code chooses how to resume, compensate, or leave it for inspection.
Interrupted managed records are excluded from default settled-record deletion.
Cancellation is cooperative and a recorded aborted status does not prove a
non-cooperative callback has stopped producing effects.
[Agents SDK durable execution](https://github.com/cloudflare/agents/blob/main/docs/agents/durable-execution.md)

This is a close precedent for the agreed behavior. It is evidence from the current
source documentation, not a tested dependency, release qualification, or proposal
to adopt the SDK on Kubernetes.

## Deno correction

Deno's migration guide explicitly states that `Deno.Kv.enqueue()` and
`Deno.Kv.listenQueue()` are unsupported on the new Deno Deploy. It recommends an
external message queue or a database-backed job queue. The guide lists July 20,
2026 as the Deploy Classic shutdown date. Earlier queue documentation is therefore
useful historical design material, not evidence that the new hosted platform
supplies those facilities. This does not by itself establish removal from every
Deno runtime or standalone KV implementation.
[Deno migration guide](https://docs.deno.com/deploy/migration_guide/#queues)

## Proposed guarantees to evaluate

The following is project-specific synthesis for further design discussion, not
existing controller behavior or accepted implementation decisions.

### Separate the acknowledgments

1. **Accepted input:** a stable input identity, destination, payload or durable
   payload reference, and acceptance record survive loss of the accepting process.
   A duplicate submission returns the same receipt; a different payload under the
   same identity is rejected rather than silently treated as the original input.
2. **Transport acknowledgment:** an upstream broker can be acknowledged once that
   durable inbox has accepted responsibility. It need not hold the broker message
   open throughout a lengthy agent turn. From this point the inbox must drive
   retries and recovery, including when the workload is absent.
3. **Completed input:** the session's resulting state, outcome, action receipts,
   and required delegation records are durably committed. Only then may the input
   be reported as successfully processed. If work was delegated, this describes
   completion of the dispatching turn, not completion of the child invocation.
4. **Completed delegated work:** the child outcome is durably recorded and reliably
   made available as a subsequent input. Publication failure after successful work
   must preserve the result and remain visible. An outcome record and a record of
   the pending notification must be committed together or recoverably reconciled.

These stages may share a store. They must not share an ambiguous status label
that treats “a pod received the message” as “the user's request succeeded.”

### Make fan-out and turn ownership recoverable

A source CVE event needs one delivery per selected session. Persist the selected
destinations or a reproducible routing decision, with sufficient policy/version
information to explain it. Delivery identities must distinguish the source event,
destination session incarnation, and deliberate redelivery from an ordinary retry.
The router can then finish an interrupted fan-out without losing destinations or
creating new logical work for destinations already handled.

One active session turn is the current working recommendation. Enforcing it across
crashes requires ownership with a generation or fencing token: an expired worker
must not commit session state after a replacement takes ownership. A lease alone
does not stop the old process. External APIs that do not understand fencing still
require their own idempotency/reconciliation rules. Ordering, priority, and whether
unrelated inputs can pass an interrupted input remain open decisions.

### Record actions before dispatch, and results before completion

Before a side effect, persist a stable action identity, its intended arguments,
and enough metadata to recognize the resulting resource. Once its outcome is
known, persist the result or resource reference. Reuse recorded successful actions
on recovery. A restarted model should not generate a new identity for the same
already-planned action merely because its reasoning is nondeterministic.

An intent record alone cannot resolve whether a remote effect happened. When an
action has no recorded result, recovery needs an integration-specific rule:
query by stable identity, safely retry with a supported idempotency key, or retain
an uncertain outcome for intervention. Compensation is explicit policy and has
its own outcome; it is not the default response to a process crash.

Record which runtime/version/checkpoint produced the continuation. A transcript
is an audit artifact; resuming execution additionally requires runtime state,
workspace state where relevant, and the action/outcome records. Object storage can
hold large artifacts, but the contract still needs an authoritative commit and
ordering mechanism.

### Retain unresolved responsibility visibly

Pending, failed, interrupted, and uncertain inputs need a retention policy that
cannot silently convert expiration into success. A transport DLQ may be part of
the mechanism, but retained status and actionable evidence must survive its TTL
if our promised recovery period is longer. Explicit cancellation or deletion also
needs a defined disposition for child work and pending notifications.

## Failure windows to use when evaluating implementations

- **Acceptance commits; its response is lost:** resubmission with the same input
  identity returns the existing receipt.
- **Router stops after 9 of 50 deliveries:** recorded routing responsibility allows
  the remaining deliveries to be created without duplicating the first nine.
- **Worker ownership expires while the old worker continues:** reject its stale
  state commits; separately protect external effects where the receiver permits it.
- **AgentRun creation succeeds; its receipt is lost:** reconcile using the saved
  operation identity and expected resource identity. Distinguish a retained run
  from a deleted resource or a different incarnation before deciding to recreate.
- **An external action succeeds; its result is not saved:** apply that action's
  recovery rule. A failed search alone may be insufficient evidence of absence.
- **A child result is saved; its completion input is not published:** retry
  publication from recorded responsibility or reconstruct it through reconciliation.
- **Input completion commits; queue acknowledgment is lost:** duplicate delivery
  observes the completed record and does not repeat the work.
- **A completion notification is duplicated:** its identity prevents a second
  follow-up turn for the same outcome.
- **Retention expires while the session is unavailable:** expose an explicit
  expired/cancelled/failed disposition according to policy, preserving evidence;
  never imply successful handling.

The choice among input CRs, a database inbox/job queue, or a broker remains open.
The selection should follow these guarantees and expected volume. None of these
requirements depends on memory snapshots or chooses a future execution backend.

## ACP attachment: current access versus durable recovery

Local evidence was inspected on `main` at `369070c659a71cecf50d57c376cb13d09d332701`.
This was a source review, not a live cluster or client compatibility test.

The current harness exposes an authenticated WebSocket ACP endpoint on port 4000
and tees the active run's notifications to viewers. It relays permission answers,
elicitation, steering, and cancellation to the run connection. A second
`session/prompt` targeting an active run is rejected to avoid concurrent prompts
altering one conversation. Its 32-frame harness replay buffer is in memory and
does not constitute a retained transcript or execution journal.
[Harness tee](../../harness/internal/tee/tee.go),
[harness interaction documentation](../../harness/README.md#the-acp-tee-live-run-status-and-human-redirection),
[ADR 0008](../adr/0008-harness-owns-pod-acp-port.md)

An AgentRun exposes `status.sandboxName`, `status.secretKeyRef`, and an `ACPReady`
condition. The controller marks that condition false when the sandbox finishes,
with the endpoint gone. An AgentRun resource continuing to exist therefore does
not imply that it still hosts an attachable process.
[AgentRun types](../../api/v1alpha1/agentrun_types.go),
[terminal readiness handling](../../internal/controller/agentrun_controller.go)

ACP v1 supports loading history through `session/load` when the agent advertises
`loadSession`; `session/resume` is separately capability-gated. These operations
provide a protocol for reconnecting to a conversation, not a platform promise
that its underlying state survived storage loss or that external effects can be
recovered. Capability negotiation must precede their use.
[ACP session setup](https://agentclientprotocol.com/protocol/v1/session-setup)

The current ACP v1 transport documentation defines stdio and permits custom
transports, with Streamable HTTP still being specified. An arbitrary ACP client
cannot therefore be assumed to speak the harness's WebSocket transport.
[ACP transports](https://agentclientprotocol.com/protocol/v1/transports)

**Project inference:** tackle2-ui is not intrinsically required to attach to a
live run. A compatible client could use a port-forward or authorized proxy, the
run's endpoint and authentication reference, and its runtime session identity.
A convenient CLI would need to perform that discovery and negotiate supported
interaction; no existing turnkey CLI or universal client compatibility was
established here. Durable attachment after sleep would additionally require
wake/connect coordination and a retained mapping from platform session identity
to runtime state. Connecting alone should not create a second execution.

New work submitted over ACP should enter the same durable input contract as
routed events. Approval replies, steering, and cancellation need explicit
active-turn semantics so they do not queue behind the work awaiting them. This
is a proposed integration requirement, not current harness behavior. A read-only
history view should remain possible without waking compute. Lifecycle and history
retention for runs and sessions remain separate choices.

## Runtime reuse: relevant mechanisms, unqualified integration

ADR 0021 selects reuse of Lightspeed's DeepAgents runtime, with production
integration still deferred. It does not establish persistent checkpointing or
safe recovery of arbitrary tools. Existing runtime reuse research must not be
treated as proof of these additional guarantees.
[ADR 0021](../adr/0021-shared-runtime-and-conditional-lightspeed-execution.md)

LangGraph offers persistent checkpointers for thread state. An in-memory saver
loses its checkpoints on restart; persistence must use storage that survives the
intended failure domain.
[Persistence](https://docs.langchain.com/oss/python/langgraph/persistence)

Its checkpoint durability modes are materially different: `exit` does not save
intermediate progress, `async` allows execution to advance before persistence
finishes, and `sync` commits checkpoints before the next step starts. Successfully
recorded node results can be reused on recovery. These are useful mechanisms to
evaluate, not evidence of the configuration or behavior of our selected runtime
integration.
[Checkpointers](https://docs.langchain.com/oss/python/langgraph/checkpointers)

LangGraph's fault-tolerance guidance explicitly addresses the external commit
gap: generate a stable idempotency key from durable state, ensure the receiving
service honors it, and treat a missing acknowledgment as an unknown outcome.
Synchronous checkpointing cannot make an external operation and a graph state
update one transaction. Tool boundaries and recovery rules still matter.
[Retry boundaries and idempotency](https://docs.langchain.com/oss/python/langgraph/fault-tolerance#retry-boundaries-and-idempotency)

**Remaining evidence needed:** qualify the pinned Lightspeed/DeepAgents versions,
checkpoint store and flush behavior, workspace restoration, stable tool-action
identities, inspection behavior during recovery, and credential lifecycle. Verify
the failure windows above against the actual integration before advertising a
resume guarantee. This note selects neither a new execution engine nor a storage
product, and contains no crash-injection measurements.
