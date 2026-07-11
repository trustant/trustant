import assert from "node:assert/strict";
import test from "node:test";
import { descendantSessionIDs, mergeChronologicalMessages } from "./issue98-session-tree.mjs";

test("collects nested task sessions and excludes sessions present before the prompt", () => {
  const sessions = [
    { id: "root" },
    { id: "old-child", parentID: "root" },
    { id: "new-child", parentID: "root" },
    { id: "nested-child", parentID: "new-child" },
    { id: "other", parentID: "another-root" },
  ];
  assert.deepEqual(
    descendantSessionIDs(sessions, "root", new Set(["old-child"])),
    ["new-child", "nested-child"],
  );
});

test("merges parent and child messages by creation time", () => {
  const merged = mergeChronologicalMessages([
    [{ info: { id: "parent-2", time: { created: 30 } } }],
    [
      { info: { id: "child-1", time: { created: 20 } } },
      { info: { id: "child-2", time: { created: 40 } } },
    ],
    [{ info: { id: "parent-1", time: { created: 10 } } }],
  ]);
  assert.deepEqual(merged.map((message) => message.info.id), ["parent-1", "child-1", "parent-2", "child-2"]);
});
