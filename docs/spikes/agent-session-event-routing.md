# Spike: event ingress, duplicate-safe admission, and AgentSession value

Status: proposed, 2026-11-11. Supports
[ADR 0025](../adr/0025-agent-sessions-and-event-routing.md). No experiments below
have run. This spike is independent of AgentLearning; use existing skills and
static context until the session value is established.

**Update (2026-11-12):** The eventual ready-for-review proposal PR must contain
an actual runnable PoC/demo with real GitHub evidence. A plan, fixture, slide
deck, or hand-created AgentInput is not sufficient. This document currently
describes required work; no demo has been implemented or executed yet.

**Update (2026-11-12):** Use AgentInput as the common request for finite
invocations and session delivery. Begin with the smallest complete product path:
configure a repository and review Agent, receive a PR-opened webhook, admit one
AgentInput, create one AgentRun, check out the event's pinned revision, and
publish a review attributed to that revision. Record input/run/review identities
and the outcome. Repeat the event after a receiver restart and prove it does not
create a second input or run. Publication ambiguity must remain visible under
the recovery rules below. New commits request reviews by default; the behavior
is configurable, and older-review policy remains to be selected. This
first demonstration precedes the broader failure qualification and session
comparison; it does not need an AgentSession or select a public route schema.

**Update (2026-11-11):** Expand the experiment from delivery of an already
configured event to source registration, verified activation, teardown, and
publication back to the source. All checks remain proposed and unexecuted.

## Decision to inform

Does a continuing session make a repeated-event workflow materially simpler or
more capable than a duplicate-safe event router creating finite runs directly?
Can the full ingress-to-completion path uphold its declared delivery guarantees?

Deliver two gates in order. Completing ingress is useful even if the session
proposal does not pass its value gate.

## Gate 1: a real event-admission contract

Build a separately deployed receiver/router alongside Agent Sandbox/controller
execution. Start with one GitHub webhook contract and deterministic routes;
add Jira only after checking the intended registration/authentication model.
Start with provider-authenticated fixtures and an isolated namespace. Then
qualify the same path in a dedicated test repository under separately authorized
provider access. Record fixtures and live evidence separately. This proposal
does not itself authorize registering hooks or posting reviews.

For the bounded prototype, use a retained SQLite store with one active router,
transactions/unique keys for receipts and logical deliveries, and retained route
plans. Document volume and single-writer assumptions; this does not qualify HA.
Do not introduce a public CRD for every internal record. Production storage,
placement, and public route API are results to recommend, not assumed choices.

Implement:

1. Source authentication, limits, normalization, stable receipt identity, payload
   conflict detection, and durable commit before success acknowledgment.
2. Pinned route configuration, deterministic matching/enrichment, and committed
   destination plans before fan-out. The Hub resolver can be a named fixture;
   the controller must not gain application-specific lookup logic.
3. Separate transport identity and semantic work identity. Use PR repository/PR/
   head-SHA/purpose for deliberate coalescing; explicit re-review adds a new
   request identity. Preserve all contributing receipt provenance separately.
4. Unique delivery reservation, deterministic named creation, full identity and
   intent comparison on AlreadyExists, resource-UID recording, and an admission
   gate before a session input can execute or be normally garbage-collected.
5. Reconciliation after a lost create/readiness response; a retained delivery
   ledger after object cleanup; explicit replay with a new identity and lineage.
6. An AgentInput selecting an Agent invocation as the control path, with a
   correlated AgentRun and no session. Qualify input-to-run creation/recovery
   and cleanup protection too. Creating the run is dispatch, not completion of
   the requested review. Any temporary controller changes or test doubles needed for
   admission gating must be explicit; existing controllers do not already supply
   this new protocol.
7. Provider-specific recovery: for GitHub, inspect failed deliveries and request
   eligible redelivery within the documented window using the required access.
   Test the scheduling/recovery logic with a delivery-API fixture and separately
   identify what needs a permitted integration test. Expose an unrecoverable
   coverage gap; do not label current-state backfill a complete event history.

Evidence must show the receipt, route revision/plan, logical key, admitted CR
name/UID, and delivery state. An HTTP 202 is a receipt, not routing or execution
completion. Set explicit dedup/replay retention for the experiment.

### Source lifecycle required for Gate 1

Start with one managed GitHub repository hook shared across multiple routes.
Use the [lifecycle research](../research/2026-11-11-agent-webhook-lifecycle.md)
to pin the intended API/edition and permissions. An operator supplies a test
repository, reachable DNS/TLS, durable storage, and credential/signing-secret
references. A limited test token or dedicated authentication-only GitHub App
installation is bootstrap; App-native webhooks are a different integration mode.

Implement a durable source record before choosing its public API. Prove desired
configuration becomes an owned provider hook, a verified signed ping, and enabled
event delivery. Prepare the receiver before registration. Test the proposed
disabled-create/verify/enable sequence with the provider; mark any unsupported
step explicitly. Readiness must show the activation boundary and uncovered
pre-subscription history.

Inject lost create/update/delete responses. Preserve uncertain operation state,
discover only the exact owned callback/intent, and surface ambiguous adoption.
Test an unrelated hook and a second route; neither may cause an extra registration
or unauthorized provider modification. Existing mode must verify a supplied
hook without adopting its lifecycle.

Also test signing-secret rotation with in-flight events, drift, expired/revoked
credentials, and managed teardown. Define the disable cutoff, retain accepted
work, verify remote removal, and expose orphaned cleanup when access is lost.
Provider response success and receiver health do not substitute for verified
delivery. Never store secrets in the evidence bundle.

## Failure cases required for Gate 1

- Deliver the same authenticated event concurrently and after a receiver restart.
  Observe one receipt and one logical delivery per destination.
- Send distinct delivery IDs for the same PR head under the coalescing policy.
  Observe one work input with multiple receipt references; preserve a different
  head or an explicit re-review as new work.
- Lose the success response after receipt commit; lose create/readiness replies
  after Kubernetes writes. Recovery must converge without a new logical input.
- Crash after eight of twenty planned deliveries. Change inventory/configuration
  while stopped. Resume the original committed plan and only missing deliveries.
- Delete a completed AgentInput through supported cleanup, then redeliver the
  event. The retained ledger suppresses recreation. Also test crash after create
  but before ledger admission: the input must not execute/vanish in that gap.
- Recreate a destination session under the same name with a new UID. Surface the
  old delivery's stale destination rather than silently redirect it.
- Reuse an identity with a conflicting intent and fail receipt persistence.
  Neither may receive a success acknowledgment claiming durable acceptance.
  Separately attempt an unauthorized target: an accepted receipt must resolve
  to a visible routing failure with no input admitted to that target.
- Deliver older source revisions after newer ones. Record the configured stale-
  work disposition; deduplication must not be mistaken for source ordering.
- Run an explicit replay and a delivery past the retention horizon. Prove the
  documented distinction from an ordinary retry.
- Simulate provider downtime within and beyond recovery coverage, including
  GitHub's lack of automatic retry. Show visible coverage gaps when recovery
  cannot reconstruct missing events.

Gate 1 passes only with automated failure evidence, not a happy-path webhook
that creates a resource once.

### Configurable review behavior

For the finite review path, demonstrate a new revision creating new review work
and a configured opt-out preventing it. Demonstrate two declared older-work
policies without treating them as chosen defaults. Record any intermediate input
that is superseded; cancellation must not silently delete its history.

Record effective configuration and its source revision before dispatch. Change
repository/binding settings and retry an older input: it must keep its original
policy. A later input should receive the new applicable policy. Precedence and
field-level merge rules remain to be specified. Scheduling/publication settings
must be read from a trusted base revision and bounded by central enforcement.
Test a PR that edits its own configuration to disable review or permit obsolete
publication: those edits must not alter its policy. After the configuration
change is merged into the trusted base, a later input may use it, within central
constraints. Verify the original input still retries with its recorded policy
and that a repository override cannot weaken a centrally enforced setting.

Also carry an unchanged finding across two review revisions. Verify the selected
comment policy uses the prior thread/provider ID and preserves human discussion.
Test a moved finding, a distinct nearby finding, and a lost publication response;
these must not be confused with duplicate webhook delivery. Preserve evidence
of uncertain matching or publication rather than claiming universal comment
deduplication. Run this experiment without an AgentSession to isolate the
publication-history requirement from conversation continuity.

## Gate 2: a session scenario that uses earlier state

Use a PR review sequence against both the direct-run baseline and a session:

1. A head-A event dispatches a review with a stable child identity.
2. While that child runs, a human follow-up changes how the result should be
   handled. The session records the decision and outstanding child reference.
3. A head-B event makes A stale. Supersede any queued A work under an explicit
   policy and record the disposition of the already-running A review. Dispatch
   B once; do not let old results publish as a review of the new head.
4. Child outcomes arrive out of order. The continuing owner must correlate them
   with the recorded revisions and human decision before acting.
5. Publish a correlated review for the intended SHA and save its provider ID,
   or retain a visible interruption if publication cannot be established safely.
6. Complete an input, remove compute, and later deliver a follow-up referring to
   the earlier decision. Fresh compute must restore the relevant state/context.

Use an explicit API input with a stable caller key for the human follow-up;
comment-command permissions and subscriptions are a separate extension.
Retain the complete event/input/child/completion/publication correlation chain,
and suppress or explicitly handle bot-generated feedback events.

The publication fixture must model the chosen provider's actual limitations:
record operation intent, fence publishers, check staleness, and attribute a
review to the SHA examined. GitHub's head lookup followed by POST is not atomic;
do not give the fixture a stronger conditional-write contract and call that a
provider guarantee. Inject a head change between those calls and a lost publish
response. Show correct revision attribution and safe recovery or visible
uncertainty, rather than promising exactly-once publication.

A fixture demonstrates control flow; qualify real registration, signed delivery,
redelivery, and review publication in the authorized test repository. Qualify a
pinned real Lightspeed/DeepAgents runtime for context restoration and actual
decision continuity as a separate result. Retain approval, steering/cancellation,
limits, inspection, and credential boundaries; explicitly report untested
capabilities.

Compare correct dispositions/outcomes, extra coordination state/code required,
context supplied to the model, and recovery behavior. If the direct route plus
explicit task state is simpler and sufficient, recommend it for that use case.
Do not claim an advantage merely because both paths can create a run.

## Session failure boundaries

Test exclusive claims and obsolete-worker fencing, interrupted model/tool work,
a child created before its reference is saved, lost child-outcome notification,
and completion before acknowledgment. When an external effect succeeds without
a saved receipt, demonstrate safe idempotent recovery or visible interruption
for an unqueryable effect. Replaying the entire prompt is not a recovery proof.

Include input arrival during idle shutdown, cancellation before old compute
stops, retained transcripts after pod removal, and cleanup that preserves
outstanding responsibility. Initial scheduling is one active input per session;
propose exact order, interruption, and cancellation semantics from the tests.

## Deliverables and decision

### Required proposal PR demonstration

Include runnable prototype sources, manifests/sample configuration, dependency
versions, and instructions for setup, execution, inspection, and cleanup. Keep
the experiment isolated from production installation defaults. Document required
cluster ingress, Agent Sandbox, GitHub authority, and model/runtime credentials;
the reviewer supplies those values. Choose the actual source registration or
existing-hook attachment path before implementing setup.

The first walkthrough uses a real test repository and PR review:

1. Configure the repository and review Agent, establish the webhook, and show
   verified event delivery to the Kubernetes service.
2. Open a PR, observe its durable AgentInput and correlated AgentRun, then inspect
   a review published by the real runtime/adapter for the examined revision.
3. Push another commit and show a new input with its recorded effective policy,
   plus the configured disposition of any older review still running.
4. Restart the receiver and redeliver an earlier event. Observe the existing
   logical input/run rather than a second execution.
5. Carry a finding across revisions and demonstrate the chosen thread-reuse
   behavior, preserving the provider identifiers and any human replies.
6. Disconnect/clean up the demo and report which accepted records are retained.

Save reproducible commands and sanitized observations correlating repository/PR,
source delivery, input/run UIDs, reviewed revision, configuration revision, and
published review/thread URLs or IDs. Distinguish successful evidence, limitations,
and untested failure cases. The live walkthrough is required before proposal
review; the broader failure matrix and session continuity experiment still
determine which architectural claims the PoC supports. Issue triage can be a
later second use case; it is not required to prove this first path.

Return reproducible sources and dependency pins, fixtures, proposed records and
state transitions, the route/work-key contract, storage/transaction boundaries,
a failure matrix, and a source onboarding/rotation/teardown and provider
recovery/retention runbook. Keep fixture and real-provider/runtime evidence
clearly separate.

Recommend ingress independently if Gate 1 passes. Recommend AgentSession only
if Gate 2 demonstrates useful cross-input ownership with qualified recovery.
List production gaps: public schemas, HA/store/volume choice, ingress deployment
ownership, authentication/RBAC, source coverage, ordering, runtime checkpoints,
retention, cancellation, and credential lifecycle. General model classifiers,
automatic session provisioning, production CVE inventory integration, IDE/CLI UX,
and VM suspension are follow-ups rather than prerequisites for this experiment.
