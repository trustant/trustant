export function descendantSessionIDs(sessions, rootSessionID, excluded = new Set()) {
  const children = new Map();
  for (const session of sessions || []) {
    const parentID = session?.parentID;
    const id = session?.id;
    if (!parentID || !id) continue;
    const values = children.get(parentID) || [];
    values.push(id);
    children.set(parentID, values);
  }

  const result = [];
  const queue = [...(children.get(rootSessionID) || [])];
  const seen = new Set();
  while (queue.length > 0) {
    const id = queue.shift();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    if (!excluded.has(id)) result.push(id);
    queue.push(...(children.get(id) || []));
  }
  return result;
}

export function mergeChronologicalMessages(messageGroups) {
  return (messageGroups || [])
    .flat()
    .sort((left, right) => {
      const leftTime = Number(left?.info?.time?.created || 0);
      const rightTime = Number(right?.info?.time?.created || 0);
      if (leftTime !== rightTime) return leftTime - rightTime;
      return String(left?.info?.id || "").localeCompare(String(right?.info?.id || ""));
    });
}
