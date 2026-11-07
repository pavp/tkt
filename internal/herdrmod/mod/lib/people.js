/** Milliseconds a people search waits after the last key before asking Jira. */
export const SEARCH_WAIT = 250;

/** The name to search for, or null while it has fewer than two letters. */
export function peopleQuery(text) {
  const q = (text ?? "").trim().replace(/\s+/g, " ");
  return [...q].length < 2 ? null : q;
}

/** The line under a people search, or null when its results speak for themselves. */
export function peopleStatus({ text, searching = false, users, error }) {
  const q = peopleQuery(text);
  if (!q) return "Type at least two letters";
  if (error) return `Could not search: ${error}`;
  if (users?.length) return null;
  if (searching || !users) return "Searching Jira…";
  return `Nobody in Jira matches “${q}”`;
}

/** The pick each person found stands for: assigning the ticket, or the value of a form's user field. */
export function peopleItems(prompt, users) {
  return users.map((u) => prompt.kind === "assignUser"
    ? { label: u.name, action: "confirmAssign", key: prompt.key, who: { to: "user", account_id: u.id, name: u.name } }
    : { label: u.name, action: "fieldValue", field: prompt.field, value: { id: u.id, name: u.name } });
}
