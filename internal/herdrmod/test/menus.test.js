import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { ticketMenu, prMenu } from "../mod/lib/menus.js";

const labels = (m) => m.items.map((i) => i.label);

test("a ticket of yours offers what the TUI's menu offers, feedback first when it needs you", () => {
  const board = fixture("board.json");
  const row = board.columns[1].rows[0];
  const m = ticketMenu(row.ticket, { agent: board.agents[row.key] });
  assert.equal(m.title, "Actions for DEMO-1");
  assert.equal(m.subtitle, "In Progress");
  assert.deepEqual(labels(m), [
    "Go to the agent in Herdr",
    "Show details",
    "Open in Jira ↗",
    "Open #12 in GitHub ↗",
    "Send the CI failure to the agent",
    "Send its feedback to another agent ▸",
    "Move to… ▸",
    "Assign to… ▸",
    "Copy the PR link",
    "Copy the Jira link",
    "Copy the key DEMO-1",
    "Copy the ticket context (.ticket.md)",
  ]);
  assert.equal(m.items[0].action, "start");
  assert.equal(m.items[2].url, row.ticket.url);
});

test("without an agent, a ticket starts one and its feedback goes to an agent picked", () => {
  const row = fixture("board.json").columns[1].rows[0];
  const m = ticketMenu(row.ticket, {});
  assert.equal(m.items[0].label, "Start · open the agent");
  assert.ok(labels(m).includes("Send the CI failure to an agent ▸"));
});

test("a team ticket offers details, Jira, assign and copies", () => {
  const t = { key: "DEMO-8", status: "To Do", team: true, url: "https://example.atlassian.net/browse/DEMO-8" };
  const m = ticketMenu(t, {});
  assert.equal(m.subtitle, "To Do · unassigned");
  assert.deepEqual(labels(m), ["Show details", "Open in Jira ↗", "Assign to… ▸", "Copy the Jira link", "Copy the key DEMO-8"]);
});

test("a backlog ticket not yours asks before starting", () => {
  const t = { key: "DEMO-40", status: "To Do", assignee: "Grace Hopper", board: "Web", url: "u" };
  const m = ticketMenu(t, { backlog: true, me: "Ada Lovelace" });
  assert.equal(m.subtitle, "To Do · Grace Hopper · Web");
  assert.equal(m.items[0].action, "notYours");
});

test("someone's pull request opens, reviews and copies its link", () => {
  const view = fixture("prs.json");
  const review = view.sections[2].rows[0];
  const m = prMenu(review, {});
  assert.equal(m.title, "Pull request #15");
  assert.equal(m.subtitle, "example-org/api · grace · review requested");
  assert.deepEqual(labels(m), ["Open in GitHub ↗", "Review with ▸", "Copy the PR link"]);
});

test("one of your pull requests acts on it, then on its ticket", () => {
  const mine = fixture("prs.json").sections[0].rows[0];
  const m = prMenu(mine, { agent: "working" });
  assert.equal(m.title, "Pull request #12");
  assert.deepEqual(labels(m).slice(0, 5), ["Open #12 in GitHub ↗", "Send the CI failure to the agent", "Send its feedback to another agent ▸", "Review with ▸", "Go to the agent in Herdr"]);
});
