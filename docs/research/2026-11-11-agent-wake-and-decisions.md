# Agent wakes and routing decisions

Research date: 2026-11-11. Supporting research, not an ADR or an adopted API. Evidence is official documentation and first-party prototype documentation read on this date; no failure-injection experiments were run. Cloudflare pages are dated April–October 2026; DeepSeek references are moving `main`, not a pinned release.

## The useful separation

**A wake is delivery of an already-addressed input; deciding whether an event deserves work can be separate.** Cloudflare invokes a stored, named callback. Its optional parser uses a model to interpret natural language into a structured schedule beforehand; firing the timer needs no further model decision. [Cloudflare Scheduler](https://developers.cloudflare.com/agents/runtime/capabilities/scheduler/)

For our design, distinguish four responsibilities: receive and retain events; determine permitted destinations; optionally assess meaning; deliver inputs and activate their consumers. Jira knowledge, application applicability and semantic assessment belong above the domain-agnostic controller. A timer delivering to a known session needs no Jira classifier.

## What Cloudflare actually provides

The Agents scheduling API accepts a delay in seconds, a `Date`, or a cron string through `schedule(when, callback, payload?, options?)`; `scheduleEvery` supports intervals. Callback names resolve to Agent methods. SQLite stores schedules, which execute through the Lifecycle job queue and survive restart or eviction. APIs expose listing, lookup and cancellation. These are persisted callbacks, rather than a process remaining asleep in memory. [Schedule tasks, updated October 29](https://developers.cloudflare.com/agents/agent-class/schedule-tasks/)

The Scheduler documents UTC cron interpretation, minute precision for cron and second precision for intervals. Intervals start after the interval and skip an occurrence if the previous execution remains active. Callbacks share serial execution with the object's other jobs. Recurring schedule creation deduplicates matching callback/timing/payload by default; dated and delayed schedules require opting in. Eviction during a callback causes it to run again. This requires idempotent effects, even though creating the schedule itself can be idempotent. [Scheduler](https://developers.cloudflare.com/agents/runtime/capabilities/scheduler/)

**Persistence does not imply unlimited retries until business success.** Scheduled callbacks retry thrown failures; exhausted attempts go through `onError`. A one-shot schedule is then removed, while recurring schedules advance, regardless of success. A failed timer-to-inbox write therefore needs application-level pending-delivery records or equivalent recovery if losing that input is unacceptable. [Retries, updated October 27](https://developers.cloudflare.com/agents/agent-class/retries/)

At the lower layer, a Durable Object alarm is persisted through the Storage API and wakes the object without an incoming request. There is one pending alarm per object; a stored schedule collection can multiplex many events through it. Alarm execution is at least once, with uncaught failures retried up to six times using exponential backoff beginning at two seconds. The documentation recommends explicitly scheduling another alarm when indefinite retries are required. Restart can run the handler again from its beginning. Thus neither an alarm nor a callback is an exactly-once external transaction. [Durable Objects alarms, updated April 21](https://developers.cloudflare.com/durable-objects/api/alarms/)

Unverified limits: punctuality SLA, complete missed-cron catch-up behavior, and IANA-timezone support were not established by these pages. Do not assume every missed recurrence replays. Verify the needed behavior against a pinned SDK version.

## The likely DeepSeek reference is real agent routing

The strongest match is **DeepSeek's `dsh-superagent-prototype`**, whose README names *Exploring Super-agent Design: Towards a More Autonomous Agent System*. It is a proactive coding system for one GitHub repository, with optional Feishu integration. The publisher explicitly labels it research code, not an official product or production-ready software, and says the published code differs from the article and internal implementation. This is a credible match to the remembered agent router; it is not a reason to substitute DeepAgents or model-level expert routing. I have not independently verified that the user read this exact article. [Official repository and README](https://github.com/deepseek-ai/dsh-superagent-prototype)

Its domain model separates Source, immutable Trigger, Thread, Eval, Ticket, Mandate and Executor. Sources track read positions and translate events to threads. Subscriptions fan out context to existing Tickets; other Triggers undergo evaluation for new work. Acceptance creates a Ticket with an authorized scope. Accepting the same Trigger twice creates one Ticket. Correlation enriches context without itself choosing a route. These are documented prototype invariants, not independently measured delivery guarantees. [Domain model](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/001-domain.md)

Code-enforced admission checks distinguish explicit requests from inferred candidates. Inferred acceptance requires assessment; `observe` mode records would-accept outcomes without launching work, and `auto` mode permits positive assessments to launch it. Subscribed context can bypass admission without granting additional authority. Policy versions are recorded, and stale evaluations are discarded and queued again. The reusable lesson is a deterministic permission boundary around probabilistic assessment, with decisions visible for review. [Authorization](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/002-authorization.md)

The main Session routes; Executors perform technical work. The router receives reports and candidates asynchronously and ends its turn when there is nothing to do. It does not sit waiting on workers. Attachments require explicit references or an unambiguous matching change rather than topic similarity alone. This closely resembles a session delegating AgentRuns while remaining available for later inputs. The prototype's entity names should not automatically become our CRDs. [Prompt roles](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/004-prompts.md)

## Proposed division for our system

These are recommendations, not claims about existing controller behavior:

- **Source adapter:** authenticate the sender, normalize the payload, preserve the source event identifier and cursor, and deduplicate repeated deliveries. Persist the event before acknowledging durable acceptance.
- **Deterministic routing:** use explicit session addresses, application subscriptions, repository/PR ownership and correlation records to find permitted destinations. Persist the recipient set or resumable fan-out progress so a partial fan-out can recover.
- **Optional model assessment:** classify ambiguous intent, applicability, priority or new-work versus follow-up. Keep this outside the controller. Restrict its choices to authorized targets and actions, record the evidence and decision, and start in observation mode if false positives are costly.
- **Inbox and activation:** deliver an input idempotently, then make the session eligible to run. An already-busy session retains the input for a later turn. Delivery does not imply completion, and accepting context does not inherently authorize new action.

For an optional durable classifier, record the event snapshot, allowed destinations, routing-policy version, relevant context/learning references, model configuration and accepted structured result. Reuse a committed result on recovery. An unanswered model request can be retried and yield a different answer; avoid claiming identical inference replay. If policy changes invalidate a decision, create an explicit new assessment rather than silently changing an existing delivery.

Known Jira/PR destinations should normally take the deterministic path. For example, a PR review event tied to a session's delegated AgentRun can become that session's input directly. A new Jira issue whose application or intended task is unclear may require assessment. A CVE fan-out can combine deterministic inventory applicability with model interpretation, while retaining a separate delivery outcome for each affected application/session. These are illustrative product policies, not settled defaults.

## “Wake tomorrow morning” as a concrete flow

1. Interpret the request into a concrete date, time and timezone, using an established preference or resolving ambiguity before acceptance. Preserve the original instruction and the resolved instant. For recurring local-time schedules, preserve the IANA zone and an explicit daylight-saving policy too.
2. Persist a schedule containing its stable operation ID, target session, due instant and input payload. Confirm scheduling only after this record is committed. Repeating the request after a lost response should resolve to the same schedule when it carries the same operation ID.
3. At the due time, a scheduler attempts to create the normal durable session input. The scheduled-occurrence ID is its deduplication key. Mark delivery complete only after durable inbox acceptance; a lost receipt must be safe to retry.
4. The session consumes the input and decides what work is needed using its current relevant state. Its final outcome is separate from the timer firing or input acceptance. If already active, it processes the input in normal order rather than starting a concurrent writer.
5. Retain pending, delivered, failed and cancelled outcomes. Propose catch-up once for overdue one-shot wakes, with lateness visible; choose replay, coalescing or skipping explicitly for recurring work. Never turn a missed deadline or exhausted retry budget into apparent success.

This design does not require suspending a process or snapshotting a microVM. An independently available scheduler and durable input records can activate a fresh runtime between completed inputs. It also leaves runtime suspension as a future optimization. The storage schema and whether scheduling is an existing service, generic resource or separate component remain open.

## Kubernetes parity and remaining decisions

Kubernetes CronJobs offer recurring scheduling and an explicit timezone field stable since v1.27. Their documentation warns that scheduled Job creation is approximate and may duplicate or miss executions; Jobs must be idempotent. The current page also documents catch-up that starts only the most recent missed occurrence. A CronJob is therefore a possible wake mechanism, not the durable input-completion contract itself. Older cluster versions require checking their own catch-up behavior. [CronJob documentation, updated October 31, 2026](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/)

For our implementation, a generic scheduler can reconstruct overdue work from persistent records after restarting and reconcile delivery. That is an architectural proposal compatible with ordinary workloads, not an assertion that Kubernetes already provides our complete inbox or timer semantics. MicroVM support is unnecessary for this first version.

The next design question is whether each configured trigger means **always deliver to this known session**, or **ask an assessing agent whether to create work**. Supporting both paths does not require placing model judgement in the controller. After that, decide whether context-only events wake immediately, accumulate until another input, or use a configurable debounce; how overdue recurrences behave; and who can create, revise and cancel schedules. Those choices determine the user-visible semantics more than the name “router.”
