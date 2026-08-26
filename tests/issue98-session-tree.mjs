/*
 * Copyright 2025-2026 Nuvolaris Inc
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published
 * by the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

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
