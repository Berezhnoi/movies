---
name: plan-to-issues
description: Turns an approved docs/plans/<slug>.md into GitHub issues (tracking issue + one per task); use after feature-plan, before implementation.
---

# Plan to GitHub issues (Movies project)

Use this skill after `docs/plans/SLUG.md` is approved and before any implementation starts. It exports the plan's tasks to GitHub issues so each task can be implemented, tracked and closed on its own. It changes no source code.

## Rules
- Input: the plan path given as an argument, or the newest file in `docs/plans/`. If its status is not `approved`, stop and say so.
- Do a dry run first. Create nothing until the user approves the dry run.
- Only create issues, labels and (if the user agrees) one milestone. Never edit, close or delete existing issues, and never push, commit or change source files. The only file you may edit is the plan itself (to write back issue numbers).
- Use the `gh` CLI for the current repository only. Never use `--web` or interactive prompts. Never print tokens.
- The repository may be public: issue text comes from the plan and PRD only. Do not include secrets, `.env` values, tokens or internal hostnames.
- Each issue must be self-contained: an engineer (or an agent in a fresh session) can implement it from the issue and the linked PRD and plan alone.

## Process
1. Preconditions. Run `gh auth status` and `gh repo view --json nameWithOwner,visibility`. Read the plan and its PRD. If `gh` cannot reach GitHub (sandbox, no login), do not retry in loops: go to the fallback below.
2. Parse the tasks (T1, T2, ...): title, requirements covered (FR/NFR ids), dependencies, files, steps, tests, verification command.
3. Classify each task with a phase label: `phase:research` (spikes and unknowns), `phase:tracer` (the first thin end-to-end slice), `phase:build` (the rest), `phase:docs`, `phase:verify` (full verification and review). Add the label `feature:SLUG` and the label `task`.
4. Check for duplicates: `gh issue list --state all --search "SLUG in:title,body"`. If issues for this plan already exist, report them and ask whether to skip them. Never create a duplicate.
5. Dry run. Print a table (task, title, labels, depends on, covers) and one full example issue body. Say which labels or milestone would be created and whether the repository is public. Wait for approval.
6. Create what is missing: labels with `gh label create` (ignore "already exists"). Then create the task issues in dependency order with `gh issue create --title ... --body-file ... --label ...` (write each body to a temporary file in the scratchpad or `/tmp`, not in the repo), so `Depends on #N` always points to an existing issue. Stop at the first error and report what was created.
7. Create the tracking issue last: title `Feature: NAME`, body with the PRD and plan paths, a one-paragraph summary, and a checklist `- [ ] #N T1 title` for every task in order.
8. Write back: add the issue number to each task heading in the plan (for example `### T1: title (#12)`) and to the traceability table. Do not commit.
9. Report: the list of issue numbers and URLs, and the suggested order to implement them.

## Issue body template

```markdown
## Goal
One or two sentences.

## Covers
FR-1, FR-3 (from docs/prd/SLUG.md)

## Depends on
#N or none

## Files
- path/to/file.go

## Steps
1. ...

## Tests
- ...

## Verify
`command`  ->  expected result

## Definition of done
- [ ] Verification command passes
- [ ] gofmt, go vet and go test -race are green
- [ ] README or docs updated if the task says so
- [ ] Existing contracts unchanged (NewRouter(), Movie JSON, error format)

Plan: docs/plans/SLUG.md  |  PRD: docs/prd/SLUG.md
```

## Fallback when gh is unavailable
Write `docs/plans/SLUG-issues.md` with every issue (title, labels, body) and a ready-to-run shell block of `gh label create` and `gh issue create` commands in dependency order, using placeholders for the issue numbers of dependencies. Tell the user to run it from their own terminal.

## Quality check before finishing
- Every task in the plan has exactly one issue; every FR and NFR appears in at least one issue.
- Every issue has a verification command and valid dependencies.
- Labels exist; no duplicates were created.
- The tracking issue links every task issue.
- Nothing was committed or pushed, and no existing issue was changed.