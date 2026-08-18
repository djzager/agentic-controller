---
name: submit-pr
description: Open a pull request that passes this repo's CI checks — correct emoji title prefix and a changelog fragment when required. Use when the user wants to submit, open, or create a PR in this repository.
---

Open a pull request that passes the two conventions enforced by
`.github/workflows/pr-checks.yml`: a valid emoji title prefix and a
changelog fragment when the change type requires one. Both are easy to
miss and will fail CI if wrong.

Work through the steps in order. Do not open the PR until the title
prefix is chosen and (if required) the changelog fragment is committed.

## 1. Classify the change

Pick the single prefix that matches the change. Use the **alias**
(e.g. `:bug:`), never the emoji character.

| Prefix        | Use for                          | Changelog fragment? |
| ------------- | -------------------------------- | ------------------- |
| `:warning:`   | Breaking change                  | required            |
| `:sparkles:`  | Non-breaking feature             | required            |
| `:bug:`       | Patch / bug fix                  | required            |
| `:book:`      | Docs only                        | no                  |
| `:seedling:`  | Infra, tests, chores, other      | no                  |
| `:ghost:`     | No release note wanted           | no                  |

The title becomes `<prefix> <concise summary>`, e.g.
`:sparkles: Add agent status reporting`.

If the change spans categories, split it into separate PRs rather than
picking a broader prefix — one logical change per PR.

## 2. Add a changelog fragment (if required)

For `:warning:`, `:sparkles:`, and `:bug:` PRs, CI requires a new file
in `changes/unreleased/`. Create it with the make target:

    make changelog-create NAME=<pr-number-or-slug>-<short-description> KIND=<kind>

`KIND` must be one of: `breaking`, `feature`, `enhancement`, `bugfix`,
`deprecation`. Choose the kind that matches the prefix:

- `:warning:` → `breaking`
- `:sparkles:` → `feature` (or `enhancement` if it improves existing
  functionality rather than adding new)
- `:bug:` → `bugfix`

If you don't know the PR number yet, use a descriptive slug for `NAME`
now and rename the file after the PR is opened, or just keep the slug —
the filename only needs to be unique.

Then edit the generated YAML: set a single-sentence `description`
written as a changelog bullet. Use the `>` operator for multi-line
prose that stays one paragraph.

Validate before committing:

    make changelog-validate

Commit the fragment together with the code change.

## 3. Open the PR

Push the branch and open the PR with the classified title. Fill in the
body — summary, and a test plan when the change is testable. Reference
the issue it fixes (e.g. `Fixes #101`).

    gh pr create --title "<prefix> <summary>" --body "<body>"

## 4. Verify CI

After opening, confirm the checks pass:

    gh pr checks <number>

The relevant jobs are **Verify PR contents** (title prefix) and
**Verify changelog fragment**. If the changelog job fails, you either
picked a prefix that needs a fragment and didn't add one, or the
fragment isn't under `changes/unreleased/` on the PR branch.

## Notes

- The PR template (`.github/pull_request_template.md`) documents these
  rules but is wrapped in an HTML comment, so it never renders — this
  skill is the authoritative checklist.
- `:book:`, `:seedling:`, and `:ghost:` PRs must NOT have a changelog
  fragment forced on them; only add one when the prefix requires it.
