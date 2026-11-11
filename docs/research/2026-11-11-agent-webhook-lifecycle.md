# Webhook lifecycle and a complete AgentSession experiment

Research date: 2026-11-11. Supports [ADR 0025](../adr/0025-agent-sessions-and-event-routing.md) and the [event-routing spike](../spikes/agent-session-event-routing.md). No provider writes, webhook registrations, credentials, or live experiments were performed. Provider facts are distinguished below from proposed design.

## Recommendation

Make the first experiment manage **one GitHub repository webhook** in a dedicated test repository. That demonstrates configuration becoming a real provider subscription, including creation, recovery, updates, validation, and cleanup. Supply a narrowly scoped credential through a reference; an operator-provisioned fine-grained token is acceptable for the experiment. Prefer a dedicated GitHub App installation as the subsequent credential implementation. An App used only for authentication need not also receive App-native webhooks.

Keep **App-native webhook mode** as a separate option. It can eliminate per-repository registrations, but its bootstrap and authority differ: an owner registers the App, selects permissions/events, and installs it on chosen repositories; management of the App-wide callback uses App credentials. Do not silently mix App webhook and repository webhook APIs or receive both copies without explicit coalescing.

## Primary-source facts

### GitHub repository webhook control plane

The repository API supplies list/create/get/update/delete, ping, delivery inspection, and redelivery operations. Current documentation names repository **Webhooks** read/write permissions and accepts installation access tokens or fine-grained PATs. Create accepts URL, secret, events, and active state. It documents configuration/event-overlap constraints, but no caller idempotency key. Full-hook updates warn that omitting an existing secret removes it. Configuration responses mask the secret. Ping requests trigger a separate delivery; the request response alone does not prove successful receipt. The delete section also requires the authenticated user to own/administer the repository. Pin and test the actual API/GitHub edition. [GitHub repository webhooks REST API](https://docs.github.com/en/rest/repos/webhooks)

### GitHub App mode and credentials

App-native webhook subscriptions are configured on the App registration; available event subscriptions depend on its permissions. Webhooks can be disabled for an App used only for authentication. [Using webhooks with GitHub Apps](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/using-webhooks-with-github-apps)

Installing an App grants access to selected repositories and requested permissions. Organization policy can require owner approval. Installation is distinct from authorizing an App to act as a user. [Installing a GitHub App](https://docs.github.com/en/apps/using-github-apps/installing-a-github-app-from-a-third-party)

App-level `GET/PATCH /app/hook/config` and App webhook delivery/recovery APIs require an App JWT; installation tokens cannot call them. Configuration updates expose URL, serialization, secret, and TLS-verification settings, not an event-subscription list or active flag. [GitHub App webhook REST API](https://docs.github.com/en/rest/apps/webhooks)

Installation access tokens are minted with an App JWT, expire after one hour, and can be narrowed to a subset of the installation's repositories and permissions. They cannot grant access beyond the installation. [Generating installation access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)

### Receiver validation and recovery

GitHub signs the original request body using the webhook secret and HMAC-SHA256, delivered in `X-Hub-Signature-256`. Verify before processing and compare signatures in constant time. This authenticates the body, not arbitrary HTTP headers. [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)

GitHub does not automatically retry failed deliveries; integrations must arrange recovery. [Handling failed deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries) Redelivery is available for the past three days. [Redelivering webhooks](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks) `X-GitHub-Delivery` is retained on redelivery, and successful acknowledgment is required within ten seconds. [Webhook best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

### Jira has different registration and verification models

Jira distinguishes Connect descriptor subscriptions, dynamic Connect/OAuth registrations, and admin webhooks. Dynamic hooks expire after 30 days without refresh. OAuth webhook deliveries use a bearer token signed with the App client secret; Connect descriptor hooks use the installation shared secret. Admin hooks can be configured with a separate secret and an `X-Hub-Signature` HMAC. Admin endpoints use `/rest/webhooks/1.0/webhook`, while dynamic registrations use `/rest/api/3/webhook`. Jira requires HTTPS and a trusted certificate. Its docs also flag the end of Connect support; do not select new Connect infrastructure by default. [Jira Cloud webhooks](https://developer.atlassian.com/cloud/jira/platform/webhooks/)

Dynamic registration/list/delete/refresh are documented for Connect and OAuth apps. Recommended classic OAuth scopes are `read:jira-work` and `manage:jira-webhook`; granular scopes differ by operation. Non-public OAuth apps have a registrant/app-owner delivery restriction. Delete affects only hooks registered by the caller. The Experimental failed-webhook API is documented as Connect-only, and failures may disappear after 72 hours. [Jira webhook REST API](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-webhooks/)

Jira's admin UI requires the Administer Jira global permission and can edit, disable, or delete hooks. [Manage webhooks](https://support.atlassian.com/jira-cloud-administration/docs/manage-webhooks/)

Do not extend dynamic-hook expiry or recovery promises to admin/descriptor subscriptions. A Jira follow-up must choose one model, qualify registration credentials, verify its precise token/signature contract, and prove renewal and recovery. The current mixed documentation is not evidence that a generic API token can call every webhook endpoint.

### Publishing a review

GitHub's review API accepts an explicit `commit_id`, defaults to the latest commit when absent, and requires Pull requests write permission. The documented create interface does not provide an atomic current-head equality condition or caller idempotency key. [Pull request reviews REST API](https://docs.github.com/en/rest/pulls/reviews)

## Proposed lifecycle — engineering design, not provider guarantees

### Configuration and ownership

Introduce an internal subscription record before choosing a public CRD. Its desired state contains provider/base URL, immutable repository identity, event set, callback address, credential reference, webhook-secret reference/version, ownership mode, and enabled state. Its observed state includes provider hook ID, last reconciled generation, verified delivery, errors, and recovery coverage.

Separate `Managed` from `Existing`: Managed can create/update/delete the owned hook; Existing binds an explicitly supplied provider ID and checks it without taking over lifecycle. Never adopt a hook solely because its event list or repository matches. A managed subscription receives a unique callback path derived from its stable subscription UID. That path is an ownership discriminator, not an authentication secret. Preserve it across receiver restarts.

One subscription may serve multiple routes/sessions. Adding a session route must not create another provider webhook. Source lifecycle belongs to the always-available integration/router layer, not to the sleeping session or the domain-agnostic execution controller.

### Managed creation and uncertain responses

Persist subscription intent, callback path, and secret version before making the provider request. Prepare the receiver first. Create a disabled hook, save the returned provider ID, validate callback delivery, then enable it. Qualify this sequence against the real provider; initialization coverage begins at the documented activation boundary, not before the subscription existed.

If creation times out, record `CreateUnknown`. List provider hooks and look for the exact owned path and compatible saved intent. Bind a single verified candidate; do not send another blind create. Because the secret is not readable, confirmation uses the saved secret and a signed probe, or an explicitly authorized secret replacement. Multiple candidates or unclear ownership require visible intervention. Failure to find a hook after an ambiguous request is not proof the original operation never took effect. The spike must report that uncertainty rather than pretend POST is idempotent.

Do not claim exactly one provider registration under every failure. Detect/manage duplicate owned registrations, and independently ensure duplicate notifications cannot produce extra logical work. Distinct provider hook IDs can produce distinct delivery IDs for equivalent work, making semantic work identity necessary.

### Validation, drift, rotation, and removal

Require a reachable TLS endpoint and bind requests to the configured source, repository, and subscription. Register a secret before activation. Persist the verified ping and correlate its hook/repository identity; ping is a health event, not AgentInput work. A created provider object and a healthy local Pod are insufficient evidence of end-to-end delivery.

Reconcile desired events, active state, and callback drift only for owned hooks. Preserve the configured secret on updates. Keep API credentials separate from delivery-signing secrets and from runtime tool credentials; the reasoning runtime does not need webhook-management authority.

For signing-secret rotation, stage the new verifier locally, retain the old verifier for a bounded overlap, update the provider, and validate delivery with the new version before retiring the old one. This is our proposed protocol, not a claim that GitHub supports two active provider secrets. Record uncertain updates, protect secret values from logs/status, and test in-flight delivery behavior. App private-key and API-token renewal are separate credential lifecycles.

Disabling routing is different from disabling the source. Stop creating new work while retaining accepted obligations. For managed teardown, disable the hook, reconcile remaining accepted work, delete the specific owned provider ID, and preserve identity/history tombstones. Lost delete responses require verification; access revocation must not masquerade as confirmed deletion. If credentials are revoked before cleanup, surface an orphaned subscription with an operator cleanup procedure. Existing mode must never delete an external owner's hook.

### Recovery runs independently of session compute

The recovery loop maintains overlap when scanning delivery history, tracks stable event identity rather than attempt IDs, and requests eligible redeliveries absent from the durable inbox. A recovery API acknowledgment means another attempt was requested, not that its event was accepted. Expose a coverage gap after lost access, missed scan windows, or provider history expiry. Reconciliation of present repository state can restore desired work but does not reconstruct all historical human messages.

## Minimal complete GitHub-first spike

**Operator bootstrap:** supply a test repository and account authority; deploy the receiver with DNS/TLS and durable storage; install a dedicated credential App or supply a limited test token; create credential/signing-secret references; choose retention, allowed targets, and Managed ownership. With an authentication-only App, keep App-native event delivery disabled. These bootstrap actions are explicit requirements, not invisible automation.

**Automated path:** reconcile the configured repository hook; prove a signed ping; enable its events; accept a real PR event into the inbox; persist routing and deterministic AgentInput admission; wake a session; claim the input; create a child AgentRun using durable action identity; finish the dispatch input; admit a later human input while the child runs; persist the child's terminal result and a completion input; resume the session; publish a correlated result; save the publication receipt; sleep; later wake with restored state. Compare the same sequence with a direct-run route.

For the first human event, use an explicit API input with a stable caller key. Adding GitHub comment commands requires a separate author-authorization/filter policy and event subscription; it should not be implied by receiving pull-request events.

The publisher takes a committed desired operation containing repository/PR identity, reviewed SHA, decision, and stable publication identity. Check whether the current head makes the result stale, and always bind a review to the intended SHA. A head lookup followed by POST has a race: the head can change between calls. The defensible contract is that an A review never asserts it reviewed B, with explicit obsolete-result handling. A stronger “never publish A after B becomes current” claim needs a provider-supported conditional primitive or a different product policy.

Persist publication intent first and provider review ID afterward. A marker plus author/commit matching can help reconcile an ambiguous response, but is not a server-enforced uniqueness constraint. Fence concurrent publishers; if recovery cannot establish the effect safely, expose uncertainty instead of blindly posting again.

## Evidence required before claiming the loop works

- Show desired subscription state, provider hook ID, verified ping, activation state, and exact credential permission checks. Test both allowed and denied management operations.
- Lose create/update/delete responses, restart, and demonstrate correct owned-hook discovery or a visible unresolved state. Prove an unrelated hook is untouched.
- Rotate the signing secret while deliveries are active; reject invalid signatures and verify the new signing version. Revoke credentials and show failed cleanup/recovery explicitly.
- Exercise duplicate and semantic-duplicate events, partial fan-out, input cleanup with retained deduplication records, and ingress downtime with real redelivery recovery.
- Show linked source event, receipt, route plan, input UID, child UID, terminal result, completion input, and publication identity/provider ID. Repeat after router and worker restarts.
- Exercise out-of-order child outcomes and a changed PR head. Show no duplicate logical child, no review attributed to the wrong SHA, and visible handling of ambiguous publication.
- Tear down the managed source and preserve accepted-work/history retention. Separately verify Existing mode leaves provider state intact.

Fixtures should cover crash boundaries first. They do not prove provider permissions, endpoint reachability, subscription creation, token renewal, real runtime restoration, or publication. Require a separately authorized isolated integration run for those claims and list each untested capability. This note authorizes none of those external writes.
