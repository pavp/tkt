# tkt: fill a Jira transition's form from the board

Objective: move a ticket through a transition whose Jira form has fields (Blocked, Ceased on the Error type) from the board, with a form built from Jira's field metadata, instead of opening Jira. Delivery **Chain** (budget 400): tracker `feat/transition-forms` (draft PR to `main`), slices `feat/transition-forms-1-data` → `feat/transition-forms-2-form` → `feat/transition-forms-3-user`, each based on the previous. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-21): 526–837 authored changed lines; slice 1 150–250, slice 2 216–337, slice 3 160–250.

## Specs

- S1. "ok que tal si probamos implementar el de blocked y me avisas para yo probarlo?" → Blocked is done from tkt through a form. The form is generic, built from the transition's fields as Jira describes them (`expand=transitions.fields`): a list field (`option`) picks one of its allowed values; a user field (`user`) searches the users assignable to the ticket; Sprint and story points keep their values, as today. No organization's field names or ids in code or tests.
- S2. Fields tkt cannot fill (such as linked issues) are left out when optional; a transition with such a field required still opens the ticket in Jira ("STATUS · in Jira ↗"), as today.
- S3. Jira's validators are not visible through the API: the form never claims a field is optional beyond what Jira says, and when Jira rejects the transition the board shows Jira's message ("could not move KEY to STATUS: …").
- S4. The move still asks for confirmation before anything is written ("Move KEY to STATUS?"), now showing the values chosen; nothing is written without it. Team tickets still cannot be moved.
- S5. During development only reads against the real Jira; the user picks the real ticket for the first write and tries it with a test build (user: "me avisas para yo probarlo").
- S6. Every screen works in the full window and the tray panel (narrow); the prototype has no such screen.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1, S2 (data: fields with kinds and allowed values; transition posts values) | 1 | inline, no Writer reason | done (RED: did not compile → GREEN) | 6eb5879 |
| T2 | S1, S2, S3, S4, S6 (form with list fields; Blocked and Ceased from tkt) | 2 | inline, no Writer reason | done (RED: 4 tests → GREEN; rendered at 100 and 56 columns) | 33c6d94 |
| T3 | S1, S4, S6 (user field: assignable users search with a dropdown; comment, L6) | 3 | inline, no Writer reason | done (RED: did not compile → GREEN; rendered at 100 and 56 columns) | slice 3 commit |

Native grouping: never grouped: a stack would merge the draft tracker.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `feat/transition-forms`. Slice 2 base: `feat/transition-forms-1-data`. Slice 3 base: `feat/transition-forms-2-form`.

## Log

- L1 (2026-10-21, user): "luego me gustaria saber que haria falta para implementar el uso de ceased y blocked desde tkt" then "ok que tal si probamos implementar el de blocked y me avisas para yo probarlo?"
- L2 (2026-10-21, user): "ok vamos con chain" → 3 slices, Chain.
- L3: findings (read-only probe on the real Jira): Blocked's form has two select fields (schema type `option`, allowedValues with id and value), a user picker (type `user`) and linked issues (type `array`, items `issuelinks`), all reported optional; Ceased on the Error type has one select field; elsewhere Ceased has no fields. `GET /rest/api/3/user/assignable/search?issueKey=&query=` answers users. Validators are not exposed.
- L4: slice 1 done (6eb5879, 207 changed lines with this document): jira.Field/Option/Value, Transitions decodes fillable fields (option, user; required first), Transition posts values ({"id"} / {"accountId"}); tui and cli types mapped; the rule for leaving a move to Jira unchanged.
- L5: slice 2 done (217 changed lines): jira NeedsForm = a required field without default that is neither a list nor a user (or Sprint/story points required); the board fills list fields (formKinds), a required field of another kind leaves the move to Jira, optional ones (users until slice 3, linked issues) are left out. The form is a menu: "Reason *: —" rows open their options ("— none" for optional ones), "Move KEY to STATUS now" needs every required field and posts the values shown, so the form is the confirmation (S4); menus now truncate to the pane's width. Next: a test build for the user (S5), then slice 3.
- L6 (2026-10-21, user, from Jira's Blocked screen): "When an issue is moved to BLOCKED is mandatory to specify Source of Blockage, Reason for Blockage, Conditions to Unblock and the Responsible to Unblock. In the Comment section, detail the conditions required to unblock the issue." then "no lo llegue a mover" (no write happened). The API reports those fields optional (a validator or a screen note, invisible to the API). Slice 3 grows: the user field plus a Comment row in forms the board fills, posted with the transition (`update.comment`); re-forecast 220–350.
- L7: slice 3 done: jira AssignableUsers (apps left out) and Transition(…, comment) posting `update.comment` as ADF paragraphs; board formKinds += user; a form row for a user or the comment opens a text box (formPrompt, keyboard only; clicks are ignored while it is open): two letters search, results of an older query ignored, ↑↓ enter picks, esc keeps the previous value; Comment row in every form the board fills. Next: test build for the user to try Blocked on a ticket they choose (S5); then deliver the chain.
