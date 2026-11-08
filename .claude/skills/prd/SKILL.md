PRD: product requirements for a feature (Movies project)
Use this skill at the START of a big feature, before planning or coding. A PRD says WHAT and WHY, never HOW. The output is one short, testable document that the feature-plan skill will use later.

Rules
Do not write code, do not edit source files, do not design the implementation. Only create or update docs/prd/<slug>.md.
Every requirement must be testable: someone can say "pass" or "fail" after reading it.
Keep it simple and short (about 2 pages). This is a study project: no enterprise ceremony.
Existing contracts are fixed unless the feature explicitly changes them: REST routes and status codes, the error JSON {"error":{"code","message"}}, auth and roles (viewer/editor/admin), NewRouter() and its tests, env var names.
Separate facts from assumptions. Mark each assumption as ASSUMPTION: and collect them in Open questions.
Process
Understand the request. Restate it in 2-3 sentences. The feature name becomes the slug (for example chat-history-redis).
Ground it in the project. Read README, docs/, .env.example, the relevant packages and tests (use the docs-explorer agent if it exists). Note what already exists, which patterns to follow, and which constraints apply.
Ask clarifying questions ONCE, in a single round (use AskUserQuestion if available), at most 5-7 questions, only about things that change the requirements: users and roles, scope boundaries, behaviour on errors, limits, what "done" means. Offer a recommended default for each. If the user is not available, continue with stated assumptions.
Write docs/prd/<slug>.md using the template below. Set Status: draft.
Finish with: the file path, a 5-line summary, and the open questions that block planning. Ask the user to approve or edit. When approved, change the status to approved. Do not start the plan until then.
Template
```markdown

# PRD: <feature name>
Status: draft | approved   Date: <YYYY-MM-DD>   Slug: <slug>

## 1. Problem and goal
Why this feature exists, in 3-5 sentences. One sentence goal.

## 2. Users and roles
Who uses it (viewer / editor / admin / anonymous / another service) and what each can do.

## 3. User stories
- As a <role>, I want <action> so that <benefit>.

## 4. Scope
In scope: ...
Out of scope (explicitly): ...

## 5. Functional requirements
- FR-1: <requirement>. Acceptance: Given ... When ... Then ...
- FR-2: ...
(Numbered, each with a testable acceptance criterion, including error cases.)

## 6. Non-functional requirements
Security, performance/limits, compatibility (what must NOT change), observability, simplicity constraints.

## 7. Interfaces and data (sketch, not design)
New or changed endpoints, messages, env vars, tables, in a few lines each. No code.

## 8. Risks and dependencies

## 9. Open questions and assumptions
- Q-1 / ASSUMPTION: ... (owner, blocking yes/no)

## 10. Definition of done
Checklist: all FR acceptance criteria pass, tests added and `go test -race ./...` green, README updated, existing contracts unchanged.
Quality check before finishing
Each FR has an ID and an acceptance criterion; error and edge cases are covered.
Scope has an explicit "out of scope" list.
No implementation details (no package names, no function names, no code).
Nothing contradicts an existing contract or the README.
Open questions are listed, with blocking ones marked.