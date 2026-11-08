Feature plan: from PRD to tasks (Movies project)
Use this skill after a PRD exists in docs/prd/<slug>.md. A plan says HOW, in small steps, and proves that every requirement is covered. Output: one file, docs/plans/<slug>.md. No source code is changed.

Rules
Input: the PRD path given as an argument, or the newest file in docs/prd/. If its status is not approved, or it has blocking open questions, stop and ask the user to resolve them first.
Do not write or edit source code, tests or config. Only create or update docs/plans/<slug>.md.
Follow the project's existing patterns (handler -> service -> repository, ctx first, additive constructors, tests in the existing style). Existing contracts must not change (REST routes and status codes, error JSON, auth and roles, NewRouter() and its tests, env var names).
Keep it simple: no new abstractions or dependencies without a stated reason. Prefer the smallest design that satisfies the PRD.
Do not invent requirements. If the PRD is silent, list it under "Needs decision".
Process
Read the PRD fully. List the FR/NFR IDs.
Explore the code: which packages, files, tests and docs the feature touches, and which existing patterns to copy (use the docs-explorer agent if it exists). Run the baseline: go build ./..., go vet ./..., go test -race ./..., and note anything already failing.
Decide the approach. For each real decision write the choice, one alternative, and why. Ask the user only about decisions that change behaviour or cost a lot to reverse.
Break the work into tasks (rules below) and write docs/plans/<slug>.md with the template.
Check coverage: every FR and NFR appears in at least one task and one test. Fill the traceability table.
Finish with the file path, the task list (titles only), and the "Needs decision" items. Ask the user to approve. Do not start implementing.
Task rules
Small: one task is about one commit and one working, tested slice. Aim for 5-10 tasks, each doable in one session.
Vertical slices: the first tasks give something runnable end to end; avoid a long "build all layers" phase before anything works.
Order by dependency; mark which tasks can run in parallel.
Each task states: goal, files to touch (paths), steps, tests to add, a verification command, and the FR IDs it covers.
Risky or unknown parts come early (a spike task is fine).
The last tasks are always: docs/README update, full verification (gofmt, go vet, go test -race ./...), and a review with the go-review-best-practices skill.
Template
```markdown

# Plan: <feature name>
PRD: docs/prd/<slug>.md   Status: draft | approved   Date: <YYYY-MM-DD>

## 1. Summary
The approach in 5 lines.

## 2. Baseline
Build/vet/test state before starting; anything already failing.

## 3. Decisions
| # | Decision | Alternative | Why |

## 4. Impact
Packages and files touched, new files, config/env, migrations, docs. What stays unchanged.

## 5. Tasks
### T1: <title>   (covers FR-1)   depends on: none
- Goal:
- Files:
- Steps:
- Tests:
- Verify: <command and expected result>

(T2, T3, ...)

## 6. Traceability
| Requirement | Task(s) | Test(s) |

## 7. Risks and rollback
What could go wrong, how to notice, how to undo.

## 8. Needs decision
Open items for the user, each with a recommended default.

## 9. Definition of done
Checklist copied and adapted from the PRD.
Quality check before finishing
Every FR/NFR is traceable to a task and a test; no task without a requirement (except docs and verification).
Every task has a verification command.
No task is larger than one session; no task needs another task's unfinished output.
The plan changes no existing contract.
Needs-decision items are explicit, with a recommendation.