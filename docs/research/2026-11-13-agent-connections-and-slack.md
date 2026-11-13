# Grok Bot connections, routines, and Slack conversations

Checked 2026-11-13 against current official x.ai documentation. Research only; no external connections, routines, or messages were changed. Documentation describes product behavior, not a public durability protocol or verified implementation.

## Findings

**A connected app is an authorized tool capability.** App connections belong to the account signed into Grok Bot, and actions are bounded by the connected account's existing access. The Slack app posts as the connected Slack user, within channels that user can post to. This is explicitly separate from Slack events that trigger routines. Personal-account connections can be used by all that account's Bots; some services support multiple labeled accounts. Connections are account-level, not tied to the current device. [Computer and apps](https://docs.x.ai/grok-bot/computer-and-apps).

**A routine supplies the trigger and work instruction.** One Bot owns a routine. It can run on a schedule or on Slack/GitHub/Linear/Sentry/PagerDuty events, incoming email, or a webhook. Event triggers use separate account integrations from tool apps. Slack triggers can match a mention, phrase, reaction, or message within a chosen channel or broader scope. Only activity after trigger creation starts runs. Webhook POSTs use a bearer key and optional JSON payload; documented `200` means accepted and a run started, not completion. The UI exposes recent run status/failure reasons and retains only the latest 20 run records per routine. [Skills and routines](https://docs.x.ai/grok-bot/skills-routines-and-automations).

**Team identity and conversation access are separate from tools.** A Team Bot is shared without copying it; private chats remain separate. Owners add shared apps/secrets/files. Individual accounts require consent; sharing one account for others is an explicit shared-connector mode, gated by an administrator. The Bot can instead use its own service account where supported. Slack account linking establishes the requester. A Team Bot can delegate and return replies to the originating thread; after a routine replies there, it follows later replies without another mention. Access depends on the Bot's share scope; some organization-shared Bots admit full workspace members with limited features before account linking. Slack guests and Slack Connect channels are excluded. This is not a general claim that every incoming sender inherits the owner's credentials. [Team Bots](https://docs.x.ai/grok-bot/team-bots).

**Approval authority is another boundary.** In a Team Bot Slack thread, only the requester can answer the action-approval card. Approval, secret collection, and app authorization use different cards. Background-work approvals expire after 30 minutes; direct-chat approvals do not have that timeout. The docs describe privacy and administrative controls separately from account permissions. Therefore “the sender can talk to the Bot,” “a credential is connected,” and “this action has approval” are different facts. [Approvals, security, and privacy](https://docs.x.ai/grok-bot/approvals-security-and-privacy).

**Continuity is visible at several levels.** Bots retain their own preferences, rules, and dated work logs; a few user facts are shared across Bots. The cloud computer's files persist across tasks, and personal Bots share that computer and its browser sign-ins. This supports a persistent product identity and workspace, but does not establish a one-to-one mapping between Slack thread and process, VM, model conversation, or durable session object. [How Grok Bot works](https://docs.x.ai/grok-bot/how-grok-bot-works).

## Limits of the comparison

The inspected pages do not specify an event delivery ID contract, deduplication retention, retry schedule, missed-event replay/catch-up, atomic input admission, action completion checkpoint, or exactly-once reply publication. They do not establish durable replay semantics from a webhook `200`, nor promise that every accepted operation survives all failures. A short UI run-history window says what the app exposes; it does not prove internal storage retention. Persistent memory/files also do not imply safe retry of an external side effect.

The Team Bot page's account rules vary by private chat, Slack DM, channel/thread, consent, shared connector mode, and administrator settings. Carry those dimensions into any comparison; avoid simplifying them to “always the owner” or “always the Slack sender.” The documentation is current and still contains Cursor-account terminology; this note follows its stated behavior without inferring implementation or organizational history.

## Design implications to discuss — our proposal, not Grok Bot guarantees

The useful separation for the domain-agnostic agent system is:

1. **Connection:** provider installation/account, authorized capabilities, credential reference, tenancy, reconnect/revocation state.
2. **Input source and policy:** which external events are admitted, authenticated provenance, matching rules, and destination selection.
3. **Invocation identity:** who requested the work, which principal/credential may execute it, and who may approve it. These may differ deliberately.
4. **Continuity:** whether to create an AgentRun/AgentWorkflowRun or deliver to an existing AgentSession; the external conversation binding should not force a running process.
5. **Return route:** workspace/channel/thread or another explicit destination, with its own permission and publication record.

A Slack interaction can therefore be an ordinary input with protected source identity, requester, target, conversation correlation, and reply capability. Provider-specific adapters can translate those details without making the Kubernetes controller understand Slack membership or business meaning. The session can decide what work to delegate while the surrounding admission/publication code enforces identity and retry behavior.

Before choosing any API, decide whether a shared assistant normally acts with scoped service credentials, a requesting user's delegated credentials, or a route-specific choice. That product decision determines connector sharing and approval behavior more than the choice of webhook versus Socket Mode.

## Slack transport and identity evidence

Slack supports HTTP Events API delivery or Socket Mode. Socket Mode uses an
outbound WebSocket opened with an app-level token and needs reconnection as URLs
refresh; it avoids a public HTTP receiver. It is a provider-adapter transport
choice, not a reason to place a connection inside the stateless execution
controller. [Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/).

For HTTP events Slack expects acknowledgement within three seconds, supplies a
globally unique `event_id`, and documents bounded retries plus an optional delayed
events feature. Slack recommends queuing work separately from reception. Our
proposal should durably accept before acknowledgement, deduplicate admission, and
report recovery coverage; Slack transport is not an indefinite replay ledger.
[Events API](https://docs.slack.dev/apis/events-api/).

Slack distinguishes bot tokens from user tokens; user tokens operate on behalf
of the authorizing workspace member. Connection owner, event author, execution
identity, and recipient must therefore be recorded separately. An authenticated
event does not automatically grant its author all permissions of the connected
account; that is an authorization rule our integration must enforce.
[Token types](https://docs.slack.dev/authentication/tokens).

Replies use a channel and the root message's `thread_ts`. An adapter should
retain that destination with the input, scope it to the installation/workspace,
and recheck permitted publication rather than letting model text choose an
arbitrary destination. Slack's method documentation also says message metadata
is accessible to workspace members/apps, so it is unsuitable for confidential
payloads or secret correlation data.
[Message publication](https://docs.slack.dev/reference/methods/chat.postMessage/).

## Implications for this proposal

Keep connection lifecycle/credentials, event subscription/filtering, and
execution/conversation mapping distinct even if a UI configures them together.
A Slack mention could produce one AgentInput for a finite invocation or an
existing authorized session; a thread identifier is not a session permission.
Persist admission and reply correlation across restarts, ignore or explicitly
handle self-generated events, and keep connector credentials separate from any
Secret-backed confidential task payload.

Restricted work adds a further distinction: the connected account's ability to
read a channel does not authorize every invoking user, target session, model,
or reply destination to see that content. Avoid implicitly sharing a continuing
conversation across audiences. These are proposed requirements to evaluate,
not claims of Grok Bot's internal implementation or selected new controller CRDs.
The required GitHub demo remains first; Slack is a useful later integration for
testing whether the admission contract is provider-agnostic.
