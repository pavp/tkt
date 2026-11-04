// Move and Assign as the TUI words them (internal/tui/move.go, assign.go): every Jira write asks first.

const cancel = { label: "Cancel", action: "cancel" };

/** The Move to… menu from GET /v1/tickets/{key}/transitions. */
export function moveMenu(key, view) {
  const items = (view.moves ?? []).map((tr) => {
    if (tr.in_jira) return { label: `${tr.to} · in Jira ↗`, action: "open", url: view.url };
    if ((tr.fields ?? []).length > 0) return { label: tr.to, action: "form", move: tr };
    return {
      label: tr.to, action: "submenu",
      menu: { title: `Move ${key} to ${tr.to}?`, items: [{ label: "Yes, move it", action: "transition", key, move: tr, values: {}, comment: "" }, cancel] },
    };
  });
  return { title: `Move ${key} to`, items };
}

/** The first required field the form still lacks, worded as the TUI's refusal; empty when none. */
export function missingField(move, values) {
  const f = (move.fields ?? []).find((f) => f.required && !values[f.id]);
  return f ? `choose ${f.name} first` : "";
}

/** A move's form: each field and its value, the comment, then the move itself. */
export function formMenu(key, move, form) {
  const items = (move.fields ?? []).map((field) => {
    const value = form.values[field.id]?.value ?? form.values[field.id]?.name ?? "—";
    if (field.kind === "user") return { label: `${field.name}: ${value}`, action: "fieldUser", field };
    return {
      label: `${field.name}: ${value}`, action: "submenu",
      menu: { title: field.name, items: [{ label: "— none", action: "fieldValue", field, value: null }, ...(field.options ?? []).map((o) => ({ label: o.value, action: "fieldValue", field, value: o }))] },
    };
  });
  items.push({ label: `Comment: ${form.comment || "—"}`, action: "comment" });
  items.push({ label: `Move ${key} to ${move.to} now`, action: "transition", key, move, values: form.values, comment: form.comment }, cancel);
  return { title: `Move ${key} to ${move.to}`, items };
}

/** The Assign to… menu for a ticket now assigned to now (empty: nobody). */
export function assignMenu(key, now) {
  const items = [{ label: "Me", action: "confirmAssign", key, who: { to: "me" } }];
  if (now) items.push({ label: "Unassign", action: "confirmAssign", key, who: { to: "nobody" } });
  items.push({ label: "Search someone…", action: "searchUser", key });
  return { title: `Assign ${key} · now ${now || "unassigned"}`, items };
}

/** Asks before giving key to who ({to: me|nobody|user, account_id, name}). */
export function confirmAssign(key, who) {
  const words = who.to === "me" ? "you" : who.name;
  const [title, yes] = who.to === "nobody" ? [`Leave ${key} unassigned?`, "Yes, unassign it"] : [`Assign ${key} to ${words}?`, "Yes, assign it"];
  return { title, items: [{ label: yes, action: "assign", key, who }, cancel] };
}
