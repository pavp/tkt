// Start's pickers, worded as the TUI's (internal/tui/start.go): a worktree when the ticket has some, else its
// repositories, then the agent when more than one is installed.

function agentPicker(key, choices, base) {
  const kinds = choices.agents ?? [];
  const sel = Math.max(kinds.indexOf(choices.last_agent), 0);
  return {
    step: "agent", key, base, title: `Start ${key}`, question: "Which agent?", sel,
    rows: kinds.map((k) => ({ label: k, hint: k === choices.last_agent ? "last used" : "", value: k })),
  };
}

/** The first picker a Start shows for choices from GET /v1/tickets/{key}/start. */
export function firstPicker(key, choices) {
  if ((choices.agents ?? []).length === 0 && choices.agents_error) return { error: choices.agents_error };
  const ws = choices.worktrees ?? [];
  if (ws.length > 0) {
    const repos = new Set(ws.map((w) => w.repo));
    return {
      step: "worktree", key, title: `Open ${key}`, question: "Which worktree?", sel: 0,
      rows: ws.map((w) => ({
        label: repos.size > 1 ? `${w.repo.split("/")[1] ?? w.repo} · ${w.branch}` : w.branch,
        hint: w.agent ? `● ${w.agent}` : "no agent",
        value: w.path,
        opens: !w.agent,
      })),
    };
  }
  return {
    step: "repos", key, title: `Start ${key}`, question: "Which repositories?", multi: true, sel: 0,
    rows: (choices.repos ?? []).map((r) => ({ label: r.name, hint: r.last_used ? "last used" : "", value: r.name, checked: Boolean(r.checked) })),
  };
}

/** What follows an answer: the next picker, or the POST /v1/tickets/{key}/start body. */
export function answer(picker, values, choices) {
  const body = { herdr: "desktop", ...(picker.base ?? {}) };
  if (picker.step === "worktree") {
    body.worktree = values[0];
    const row = picker.rows.find((r) => r.value === values[0]);
    if (!row?.opens) return { post: body };
  } else if (picker.step === "repos") {
    body.repos = values;
  } else {
    body.agent = values[0];
    return { post: body };
  }
  if ((choices.agents ?? []).length === 1) return { post: { ...body, agent: choices.agents[0] } };
  return { picker: agentPicker(picker.key, choices, body) };
}

/** Where a ticket's feedback or a PR's review goes, from GET /v1/tasks/agents: an open agent, or a new one. */
export function taskPicker(view) {
  const rows = (view.agents ?? []).map((a) => ({
    label: `${a.name} · ${a.workspace || a.folder}`,
    hint: [a.status, a.same_repo ? "same repository" : ""].filter(Boolean).join(" · "),
    task: { to: "open", pane: a.pane },
  }));
  rows.push({ label: "A new agent ▸", hint: "", kinds: view.kinds ?? [], last: view.last_agent });
  return { step: "task", title: `Send ${view.phrase} to`, question: "", sel: 0, rows };
}

/** What a key does in a picker: move the highlight, tick a row of a multiple choice, or go on with the choice. */
export function pickerKey(p, k) {
  const last = p.rows.length - 1;
  if (k === "down" || k === "up") return { kind: "move", sel: Math.max(0, Math.min(last, p.sel + (k === "down" ? 1 : -1))) };
  if (k === " " && p.multi) return p.rows[p.sel] ? { kind: "toggle", value: p.rows[p.sel].value } : null;
  if (k === "enter") {
    if (p.multi) return p.checked.size > 0 ? { kind: "next" } : null;
    return p.rows[p.sel] ? { kind: "choose", index: p.sel } : null;
  }
  return null;
}
