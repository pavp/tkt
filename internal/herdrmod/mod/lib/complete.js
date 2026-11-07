import { matching } from "./match.js";

/**
 * What a Settings prompt is completing at the end of its text: an owner, one of an owner's repositories, or a
 * folder inside dir; base is the text kept before the part typed. Null when nothing completes there.
 */
export function completing(kind, text) {
  if (kind === "addRoot") return folderPart("", text);
  if (kind !== "addRepo") return null;
  const space = text.search(/\s/);
  if (space >= 0) {
    const rest = text.slice(space).replace(/^\s+/, "");
    return folderPart(text.slice(0, text.length - rest.length), rest);
  }
  const slash = text.indexOf("/");
  if (slash < 0) return { source: "owners", base: "", text };
  return { source: "repos", owner: text.slice(0, slash), base: text.slice(0, slash + 1), text: text.slice(slash + 1) };
}

function folderPart(before, path) {
  if (!path.startsWith("~") && !path.startsWith("/")) return null;
  if (path.startsWith("~") && !path.startsWith("~/")) return { source: "folders", dir: "~/", base: `${before}~/`, text: path.slice(1) };
  const at = path.lastIndexOf("/");
  const dir = path.slice(0, at + 1);
  return { source: "folders", dir, base: before + dir, text: path.slice(at + 1) };
}

/** The names to offer for c from what is known so far; undefined when its list has not been fetched yet. */
export function suggestions(c, known) {
  if (!c) return [];
  if (c.source === "owners") return matching(known.owners ?? [], c.text);
  if (c.source === "repos") {
    const names = known.repos?.[c.owner];
    return names && matching(names.map((n) => n.slice(c.base.length)), c.text);
  }
  const names = known.folders?.[c.dir];
  return names && matching(names.filter((n) => c.text.startsWith(".") || !n.startsWith(".")), c.text);
}

/** The prompt's text once the suggestion name is taken, ready for the next part. */
export function accepted(c, name) {
  if (c.source === "owners") return `${name}/`;
  if (c.source === "repos") return `${c.base}${name} `;
  return `${c.base}${name}/`;
}

/** The GitHub owners Settings knows: those of each space's repositories, configured or seen in pull requests. */
export function ownersOf(spaces) {
  const owners = [];
  for (const sp of spaces ?? []) {
    for (const repo of [...(sp.repos ?? []).map((r) => r.repo), ...(sp.inferred ?? [])]) {
      const owner = repo.split("/")[0];
      if (repo.includes("/") && !owners.some((o) => o.toLowerCase() === owner.toLowerCase())) owners.push(owner);
    }
  }
  return owners.sort();
}
