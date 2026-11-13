# AgentSession notification and decision mechanisms: research frame

[Conversation handoff, 2026-11-13](../handoffs/agent-session-design.md): current
decisions, unresolved questions, branch state, and the required live PoC.

Date: 2026-11-11. Working material for a research-and-grilling session, not an
accepted API design. The current [session ADR](../adr/0025-agent-sessions-and-event-routing.md)
is a draft. Its bespoke receiver, SQLite prototype, and GitHub-first experiment
must be compared with existing mechanisms before being treated as the direction.
No new CRDs or source registrations are implemented here.

## Supporting evidence

- [Wake-up scheduling and the DeepSeek routing prototype](2026-11-11-agent-wake-and-decisions.md): persisted timers, optional assessment, and execution responsibilities.
- [Kubernetes event mechanisms](2026-11-11-agent-kubernetes-event-mechanisms.md): real Argo/Knative CR examples, provider-registration ownership, dependencies, and delivery limits.
- [Advisory ingress](2026-11-11-agent-advisory-ingress.md): affected-repository alerts versus global advisories and the missing applicability step.
- [Provider webhook lifecycle](2026-11-11-agent-webhook-lifecycle.md): GitHub and Jira registration/authentication distinctions.
- [Kubernetes eventing conventions and Argo CD](2026-11-12-agent-kubernetes-eventing-standard.md): common event formats, reusable receivers/connectors, deployment notifications, and the unverified TPA integration.
- [CodeRabbit review policy](2026-11-12-agent-review-policy-coderabbit.md): configuration precedence, automatic rereviews, comment-history behavior, and the limits of documented publication guarantees.
- [Grok Bot connections and Slack](2026-11-13-agent-connections-and-slack.md): tool authorization versus event triggers, requester/execution identity, conversation continuity, reply routes, and HTTP/Socket Mode transport.

Existing-resource examples are verified against documentation, not executed.
They are evidence for the design discussion, not a selected platform or a
claim that our future AgentInput admission service already exists.

## The product contract to establish

Applying configuration must lead to an observable path from source setup to
accepted AgentInput, session wake-up, a recorded decision, and its outcome.
The user must be able to see which part is configured, ready, pending, failed,
or intentionally ignored. A hand-created input that wakes a pod proves only
the last part of notification delivery.

Separate these responsibilities before choosing public resource names:

- Source configuration describes what to listen to and how: GitHub/Jira
  subscription or poller, credentials, endpoint, events/filter, provider identity,
  and recovery position. One source may serve multiple sessions.
- A binding describes which source events can reach which authorized session,
  their stable work identity, and how event data becomes input. Predicates and
  authorization are deterministic even when later assessment uses a model.
- A schedule records a future wake-up independently of session compute. It
  needs an absolute due time or recurrence with time zone, payload, target,
  cancellation/revision identity, and a missed-occurrence policy.
- The session owns continuing context and the decision about work. A configured
  recipient can itself assess the event and route work to other permitted
  sessions or finite invocations.

These are configuration responsibilities, not a decision to introduce three
new public CRDs. Existing eventing resources may satisfy some of them; our
contract must say what an adapter still has to provide.

## Concrete source paths to evaluate

GitHub PR events need a configured provider subscription, reachable receiver,
and input admission. Compare an Argo Events GitHub EventSource and Sensor, a
Knative GitHubSource and event sink, and a small owned source adapter. Provider
registration, public ingress, and durable admission are distinct responsibilities.

Jira needs a chosen subscription/authentication model or a cursor-based poller.
A generic webhook receiver does not install that subscription. In the sibling
Hub checkout, `internal/tracker/jira.go` creates migration tickets and refreshes
known ticket keys; the inspected path is not new-issue discovery. That finding
is a bounded source inspection, not a complete audit of every Hub integration.

An affected-repository alert is a smaller vulnerability experiment than a global
advisory feed. Global advisories additionally require package/version inventory
and applicability resolution before application fan-out. Hub's `/dependencies`
API, as inspected in `internal/api/dependency.go`, models application-to-application
edges; do not mistake it for software package/version inventory.

For a timed wake-up, persist the schedule before saying it is set. A scheduler
admits an input keyed by schedule identity/revision and intended occurrence,
not the retry wall-clock time. Restart scans and retained delivery state must
close the gap between a due timer and durable acceptance. A fired timer alone
is not completed work. A model may request the schedule through a bounded tool;
the scheduler and input admission remain deterministic.

## SBOM service candidates remain open

TPA/Trustify and Dependency-Track may supply the vulnerability-correlation step
before events reach an agent. They can consume application SBOMs and provide
findings or changes that an adapter maps to application/session inputs. This is
an alternative to building the advisory-to-package matcher in our event layer.

[TPA/Trustify research](2026-11-11-agent-tpa-trustify-notifications.md) verifies a
released upstream change stream with finite replay retention, while leaving
Red Hat product support unverified. The stream reports document changes; an
adapter still queries affectedness and maps artifacts to applications.
[Dependency-Track research](2026-11-11-agent-dependency-track-notifications.md)
identifies project-aware finding notifications and distinguishes current v5
contracts from legacy v4 documentation. Its exact deployed-version behavior
still requires an integration test.

Neither candidate is selected. Source ownership, required delivery semantics,
and public configuration remain open; the user has not committed to what those
must look like. A concrete notification experiment should inform the next design
question rather than turn these candidate capabilities into requirements.

## Decision order for the grilling session

**Research update (2026-11-13):** Grok Bot distinguishes connected tool accounts,
routine event triggers, requester identity, and thread replies. For our design,
connection credentials and Secret-backed task content serve different purposes.
A connection need not imply an event subscription, an allowed target session, or
permission to publish to every audience. Slack Socket Mode is a concrete adapter
option when public HTTP ingress is undesirable; it still needs durable input
admission and reconnection/recovery. Service-owned versus requester-delegated
execution authority is an open policy choice, potentially per binding. No new
Connection CRD or Slack demo requirement has been selected; GitHub remains the
first required live PoC.

**Candidate mechanism (2026-11-13):** AgentInput can reference a Secret holding
confidential task content, rather than embedding it in its ordinary fields.
Existing AgentRun/AgentWorkflowRun `fileMounts` already project same-namespace
Secret keys as read-only files. Input authorization, immutable-content identity,
retention, replacement detection, and session delivery remain to be designed.
Kubernetes supports [immutable Secrets](https://kubernetes.io/docs/concepts/configuration/secret/#immutable-secrets),
but they can still be deleted/recreated; a name alone does not pin the input.
This is a concrete option to evaluate, not a selected public schema or a claim
that Secret mounting alone supplies embargo handling.

**Required use case (2026-11-13):** Embargoed work must be able to enter the
queue. Confidentiality is therefore an admission/delivery requirement for all
AgentInput execution paths, not an optional CVE-specific feature of a session.
Accepted restricted work must retain its protection through retries, dispatch,
and derived outputs. The source of classification, protected payload mechanism,
and isolated-session recommendation below remain open; the user has confirmed
the use case, not those implementation choices. Do not equate metadata opacity
with confidentiality or application membership with permission to receive it.

**Open concern (2026-11-12): embargoed vulnerabilities.** Opaque task data is
uninterpreted by the controller, not inherently confidential. Current run params
are rendered into a ConfigMap (`internal/controller/agentrun_controller.go`,
`createParamsConfigMap`), so that carrier alone is not a suitable confidentiality
contract. Explore minimal input metadata with a versioned protected-payload
reference and authorized runtime access; the receipt store and retained route
plan must not become an unprotected copy. Object names/labels, transcripts,
workspace artifacts, publication, model destinations, and shared learnings also
need compatible access controls. Merely selecting a Kubernetes Secret does not
establish the whole contract: [Kubernetes documents explicit RBAC and encryption
requirements](https://kubernetes.io/docs/concepts/security/secrets-good-practices/).

The key session question is whether restricted work must use a separate session
and workspace, even for the same application. A continuing conversation exposed
to embargoed material can carry it into later ordinary work. A separate restricted
run/session is the recommendation to discuss; namespace naming or a sensitivity
label alone is not enforcement. Hub/security integrations can assign policy and
release authority without requiring CVE semantics in the execution controller.
The required use case was confirmed on 2026-11-13; an implemented embargo-support
guarantee, policy CRD, or release workflow has not been established.

**Clarified (2026-11-12):** The execution controller remains agnostic to
applications, archetypes, repositories, and GitHub credentials. Hub/UI can own
application/archetype intent and project it into concrete bindings; provider
adapters own subscription setup and provider authentication. AgentInput carries
the resulting execution choice and opaque task data. Whether a generic binding
CR belongs in this project or bindings remain integration-owned is the next API
design question. Managed webhook setup is not an adopted controller capability.

Bounded local inspection supports the separation: controller run types expose
generic params/env/file mounts; `harness/cmd/migration-harness/main.go` resolves
application/repository data through Hub; workflow stages copy parent labels
generically. The sibling Hub checkout at `3fecffe` contains application repository
data and archetype membership resolution (`internal/api/resource/application.go`
and `internal/assessment/archetype.go`). No AgentRun creation path was found in
the inspected Go sources there; ADR 0006's Hub integration remains a proposed
contract rather than evidence of implementation in that checkout.

**Resolved (2026-11-12):** A ready-for-review PR for this proposal must include
a working PoC/demo against a real GitHub repository. Start with the PR-review
AgentRun path, including source setup, a published review, subsequent commits,
and duplicate delivery. Include runnable code/configuration and reproducible
evidence; this is outstanding work, not a completed experiment. A finite-run
demo does not by itself qualify the separate AgentSession continuity claims.

**Resolved (2026-11-12):** Scheduling and publication policy is read from a
trusted base revision, with repository overrides bounded by centrally enforced
settings. A PR cannot change its own policy by editing its configuration file.
Record the effective policy and configuration revision with the input; merged
changes apply to later inputs while retries retain the original selection.
Exact fields and merge rules remain open.

**Direction (2026-11-12):** New PR commits normally trigger review. Automatic
review and treatment of older running reviews should be configurable per
repository/task. Repository-file settings and defaults on the event-to-execution
binding are candidates; the trust rule is resolved above, while exact field-level
precedence remains open. Separate repeated event execution, repeated findings across reviews, and
uncertain comment publication. Persistent review history can serve finite runs
without requiring an AgentSession.

**Direction (2026-11-12):** Broaden AgentInput beyond session delivery: one
durable request can invoke an Agent or AgentWorkflow, recording its resulting
run, or address an existing AgentSession. Start with a GitHub PR-opened webhook
producing a finite review AgentRun and a correlated published result. Event
matching and parameter mapping are configurable; source authentication and
allowed destinations remain deterministic. Exact public schemas and source
registration ownership still need decisions. This direction does
not select an eventing platform or require sessions for the first example.

**Research update (2026-11-12):** Multiple ingestion paths should be evaluated
against one durable input-admission contract. CloudEvents is an interoperability
option; Argo Events and Knative are optional installed platforms. Argo CD can
manage their configuration and emit deployment notifications. Camel's Jira
connector is another candidate. No eventing dependency, connector, or public
configuration shape has been selected.

**Resolved (2026-11-11):** These are composable paths rather than exclusive
architectures. An AgentSession can itself be the routing agent. Configured
delivery addresses either a specialist or a session that evaluates the event
and chooses subsequent work. The routing role does not require another resource
kind; source delivery and model-driven routing remain distinct responsibilities.

Next, examine shared source ownership versus session-local trigger
configuration, existing eventing dependencies versus a small built-in mechanism,
Jira registration/polling, schedule creation permissions and missed wakes, then
the concrete advisory source and applicability mapping. Work through these one
at a time, with actual resource examples and failure cases; do not resolve them
implicitly by writing a complete ADR or selecting a database.
