# CodeRabbit review policy: documented behavior and limits

Checked 2026-11-12 using current official documentation. Research only: no repository settings, comments, reviews, or other external state changed. No agent-system API names selected.

## Configuration is part of the product contract

The current documented precedence is: workspace global overrides → organization global overrides → repository file → central `coderabbit` repository → selected repository UI settings → organization UI defaults → workspace settings → schema defaults. Repository UI settings participate only when that UI source is selected. Sources do not merge by default; `inheritance` enables parent merging. Global overrides can enforce inheritance. YAML takes precedence over TypeScript configuration. The `@coderabbitai configuration` command shows resolved values with source annotations. This is richer than “repository YAML always wins.” [Configuration overview](https://docs.coderabbit.ai/guides/configuration-overview).

The YAML guide says `.coderabbit.yaml` is read from the **feature branch under review**. This permits review configuration to change in the same PR. It also documents `remote_config.repository`, `ref`, and `path` for explicitly referenced shared files. [YAML configuration](https://docs.coderabbit.ai/getting-started/yaml-configuration).

Trust qualification: the status-check guide specifically describes reading PR branch changes on same-repository GitHub PRs by members/collaborators; global overrides supersede them. Do not assume identical head-branch trust behavior for arbitrary fork authors. This bounded research did not establish the exact immutable commit-resolution mechanism, configuration cache lifetime, or which branch an automatically discovered central configuration repository reads. [Status-check trust discussion](https://docs.coderabbit.ai/pr-reviews/status-check#what-the-check-does-and-doesnt-guarantee).

## Supported review cadence and scope controls

The current generated reference, dated November 12, 2026, documents these settings:

- `reviews.auto_review.enabled`: `true` by default.
- `reviews.auto_review.auto_incremental_review`: `true` by default; review again on each push.
- `reviews.auto_review.auto_pause_after_reviewed_commits`: default `5`; `0` disables the pause.
- `reviews.auto_review.drafts`: default `false`.
- `reviews.auto_review.labels`: default `[]`; positive labels opt in, `!label` excludes.
- `reviews.path_filters`: default `[]`; include/exclude file globs, including sparse checkout effects.
- `reviews.abort_on_close`: default `true`; abort when the PR closes or merges.

`abort_on_close` does not mean cancel-on-new-commit. [Configuration reference](https://docs.coderabbit.ai/reference/configuration).

Incremental reviews focus on commits since the previous review. Setting `auto_incremental_review: false` suppresses subsequent push-triggered reviews until manual invocation. With automatic reviews disabled, a configured positive label can request a review; negative-only labels do not opt in. Later pushes remain eligible while that positive label is present if incremental review is enabled. Automatic reviews default to PRs targeting the repository's default branch; `base_branches` adds regex-matched targets. Pausing counts **reviewed commits**, not webhook deliveries, and the counter resets when resumed. [Automatic review controls](https://docs.coderabbit.ai/configuration/auto-review).

## New commits and already published findings are different problems

Official commands distinguish an incremental review, which considers CodeRabbit's comments since its last full review, from a full review, which disregards previous comments and reviews the whole PR. Manual `review`, `full review`, `pause`, and `resume` commands exist. `resolve` can resolve previous comments. These establish user-facing history-aware behavior; they do not specify a stable finding fingerprint, thread matching algorithm, or guarantee that repeated findings never produce another comment. [Manage reviews](https://docs.coderabbit.ai/guides/commands).

The status-check documentation describes cancellation when a PR changes during review and explains that an older commit may receive no check if a newer push replaces it before review begins. It does **not** establish a configurable blanket guarantee that every new commit immediately cancels all prior execution or prevents every stale publication. It separately exposes publication failures, including a review recorded as reviewed but not published. An incremental request may decline to revisit such a commit; a full review can retry it. A passing/skipped/rate-limited status is not proof of a full review. [Status check](https://docs.coderabbit.ai/pr-reviews/status-check).

This bounded search found no official exactly-once webhook admission or review-comment publication contract, and no configurable semantic duplicate-finding algorithm. Product behavior and recovery documentation should not be promoted into those guarantees. The public architecture page describes repository context, analysis agents, and memory, but does not supply those algorithms. [Architecture](https://docs.coderabbit.ai/overview/architecture).

## Implications to discuss for our design — proposals, not CodeRabbit facts

Keep at least four decisions explicit:

1. **Transport duplication:** the same provider delivery must not accidentally admit the same input again. Keep source/delivery identity and a retained receipt.
2. **Review execution:** choose cadence and supersession policy: every revision, latest eligible revision, manual request, or coalescing. Capture PR identity, base/head revisions, resolved policy version, and explicit rerun identity. A new head is not necessarily a duplicate.
3. **Repeated findings:** decide whether an unchanged unresolved finding is suppressed, updates its existing thread, or appears in a summary; decide what reopening after regression means. This requires retained finding/thread state, beyond queue deduplication.
4. **Uncertain publication:** if a comment POST succeeded but its response was lost, recover using provider-visible correlation and recorded publication intent before retrying. Do not ask a model to regenerate the review as the first recovery step. If recovery remains inconclusive, expose that state.

A useful configurable review example would combine label/draft/path eligibility, rereview cadence, obsolete-revision handling, finding continuity, and publication recovery. Version-controlled defaults plus protected administrative overrides could make the effective policy explainable. Decide which revision is trusted for policy before adopting the convenience of PR-head configuration.
