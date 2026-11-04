// The actions menus, worded and ordered as the TUI's (internal/tui/menu.go); the Herdr 0.9.5 ticket pane is
// left out, since the mod shows details itself.

const FIX = {
  "Changes requested": ["Send the review comments to the agent", "Send the review comments to an agent ▸"],
  "Unresolved comments": ["Send the unresolved comments to the agent", "Send the unresolved comments to an agent ▸"],
  Conflicts: ["Ask the agent to resolve the conflicts", "Ask an agent to resolve the conflicts ▸"],
};
const CI = ["Send the CI failure to the agent", "Send the CI failure to an agent ▸"];

function startItem(key, agent) {
  return { label: agent ? "Go to the agent in Herdr" : "Start · open the agent", action: "start", key };
}

/** Feedback to the ticket's own agent when it has one, with another agent a step away; else to one picked. */
function feedbackItems(key, reason, agent) {
  const [own, any] = FIX[reason] ?? CI;
  if (agent) return [{ label: own, action: "fix", key }, { label: "Send its feedback to another agent ▸", action: "feedbackTo", key }];
  return [{ label: any, action: "feedbackTo", key }];
}

const copy = (label, value) => ({ label, action: "copy", value });

/** A ticket's menu: yours (Sprint), the team's, or on the Backlog (opts.backlog, with opts.me). */
export function ticketMenu(t, opts = {}) {
  const key = t.key;
  const who = t.assignee || "unassigned";
  if (opts.backlog) {
    const notYours = !t.assignee || t.assignee !== opts.me;
    const items = [notYours ? { label: startItem(key, opts.agent).label, action: "notYours", key } : startItem(key, opts.agent)];
    const move = sprintMoveItem(t, { groups: opts.groups, activeSprints: opts.activeSprints });
    if (move) items.push(move);
    items.push({ label: "Show details", action: "details", key });
    if (t.url) items.push({ label: "Open in Jira ↗", action: "open", url: t.url });
    items.push({ label: "Assign to… ▸", action: "assignMenu", key, now: t.assignee ?? "" });
    if (t.url) items.push(copy("Copy the Jira link", t.url));
    items.push(copy(`Copy the key ${key}`, key));
    return { title: `Actions for ${key}`, subtitle: [t.status, who, t.board].filter(Boolean).join(" · "), items };
  }
  if (t.team) {
    const items = [{ label: "Show details", action: "details", key }];
    if (t.url) items.push({ label: "Open in Jira ↗", action: "open", url: t.url });
    items.push({ label: "Assign to… ▸", action: "assignMenu", key, now: t.assignee ?? "" });
    if (t.url) items.push(copy("Copy the Jira link", t.url));
    items.push(copy(`Copy the key ${key}`, key));
    return { title: `Actions for ${key}`, subtitle: `${t.status} · ${who}`, items };
  }
  const prs = t.prs ?? [];
  const items = [startItem(key, opts.agent), { label: "Show details", action: "details", key }];
  if (t.url) items.push({ label: "Open in Jira ↗", action: "open", url: t.url });
  if (prs.length === 1) items.push({ label: `Open #${prs[0].number} in GitHub ↗`, action: "open", url: prs[0].url });
  if (prs.length > 1) items.push({ label: `Pull requests (${prs.length}) ▸`, action: "prs", key });
  const reason = opts.attention?.[0] ?? attentionOf(t);
  if (reason) items.push(...feedbackItems(key, reason, opts.agent));
  items.push({ label: "Move to… ▸", action: "move", key }, { label: "Assign to… ▸", action: "assignMenu", key, now: t.assignee ?? "" });
  if (prs.length === 1 && prs[0].url) items.push(copy("Copy the PR link", prs[0].url));
  if (t.url) items.push(copy("Copy the Jira link", t.url));
  items.push(copy(`Copy the key ${key}`, key), { label: "Copy the ticket context (.ticket.md)", action: "copyContext", key });
  return { title: `Actions for ${key}`, subtitle: t.status, items };
}

// Without the daemon's card, a ticket's first reason is read from its pull requests as the card would say it.
function attentionOf(t) {
  for (const pr of t.prs ?? []) if (pr.state === "open" && pr.checks === "fail") return "CI failing";
  return "";
}

/** A pull request's menu from a /v1/prs row: yours acts on it then on its ticket; anyone else's opens and reviews. */
export function prMenu(row, opts = {}) {
  const pr = row.pr;
  if (row.other || row.review || !row.ticket) {
    const how = [pr.author || "unknown author", row.review ? "review requested" : ""].filter(Boolean).join(" · ");
    return {
      title: `Pull request #${pr.number}`,
      subtitle: `${pr.repo || "unknown repository"} · ${how}`,
      items: [{ label: "Open in GitHub ↗", action: "open", url: pr.url }, { label: "Review with ▸", action: "review", pr }, copy("Copy the PR link", pr.url)],
    };
  }
  const t = row.ticket;
  const items = [{ label: `Open #${pr.number} in GitHub ↗`, action: "open", url: pr.url }];
  if (row.attention?.length) items.push(...feedbackItems(t.key, row.attention[0], opts.agent));
  items.push({ label: "Review with ▸", action: "review", pr }, startItem(t.key, opts.agent), { label: `Show details of ${t.key}`, action: "details", key: t.key });
  if (t.url) items.push({ label: `Open ${t.key} in Jira ↗`, action: "open", url: t.url });
  items.push({ label: "Move to… ▸", action: "move", key: t.key }, { label: "Assign to… ▸", action: "assignMenu", key: t.key, now: t.assignee ?? "" });
  if (pr.url) items.push(copy("Copy the PR link", pr.url));
  if (t.url) items.push(copy("Copy the Jira link", t.url));
  items.push(copy(`Copy the key ${t.key}`, t.key));
  return { title: `Pull request #${pr.number}`, subtitle: `${t.key} · ${t.status}`, items };
}

/** The Backlog's Move to current sprint for a ticket, as the TUI offers it: its board's active sprints, asking
 *  first; null when the ticket is already in an active sprint's group or its board runs none. */
export function sprintMoveItem(t, { groups = [], activeSprints = [] } = {}) {
  if (groups.some((g) => g.active && (g.rows ?? []).some((r) => r.key === t.key))) return null;
  const mine = activeSprints.filter((sp) => sp.board === t.board);
  const confirm = (sp) => ({
    title: `Move ${t.key} to ${sp.name}?`,
    items: [{ label: "Yes, move it", action: "moveToSprint", key: t.key, sprint: sp.id, name: sp.name }, { label: "Cancel", action: "cancel" }],
  });
  if (mine.length === 0) return null;
  if (mine.length === 1) return { label: "Move to current sprint", action: "submenu", menu: confirm(mine[0]) };
  return {
    label: "Move to current sprint ▸", action: "submenu",
    menu: { title: `Move ${t.key} to which sprint?`, items: mine.map((sp) => ({ label: sp.name, action: "submenu", menu: confirm(sp) })) },
  };
}
