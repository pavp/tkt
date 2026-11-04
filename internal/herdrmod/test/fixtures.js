import { readFileSync, readdirSync } from "node:fs";

const dir = new URL("../../daemon/testdata/contract/v1/", import.meta.url);

/** The daemon's contract fixture `name`, as the Go tests of `/v1` wrote it. */
export function fixture(name) {
  return JSON.parse(readFileSync(new URL(name, dir), "utf8"));
}

export function fixtureNames() {
  return readdirSync(dir).filter((f) => f.endsWith(".json"));
}
