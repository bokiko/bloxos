// Connectivity never participates in stable/name or user-defined ordering.
// Keep unknown/new IDs after the saved set; ignore IDs no longer in the fleet.
export function normalizeMachineOrder(value) {
  return Array.isArray(value) ? [...new Set(value.filter(id => typeof id === 'string' && id.length > 0))] : [];
}

export function orderMachines(machines, { sort = 'name', order = [], pinned = [], status = () => 0 } = {}) {
  const rank = new Map(normalizeMachineOrder(order).map((id, i) => [id, i]));
  const pins = new Set(pinned);
  const byName = (a, b) => (a.hostname ?? '').localeCompare(b.hostname ?? '') || a.machine_id.localeCompare(b.machine_id);
  return [...machines].sort((a, b) => {
    if (sort === 'manual') {
      const ar = rank.get(a.machine_id), br = rank.get(b.machine_id);
      if (ar !== undefined || br !== undefined) return (ar ?? Infinity) - (br ?? Infinity);
      return byName(a, b);
    }
    const pin = Number(pins.has(b.machine_id)) - Number(pins.has(a.machine_id));
    if (pin) return pin;
    let delta = 0;
    if (sort === 'status') delta = status(a) - status(b);
    if (sort === 'cpu') delta = (b.cpu_percent ?? 0) - (a.cpu_percent ?? 0);
    if (sort === 'gpu_temp') delta = (b.gpu_temp ?? 0) - (a.gpu_temp ?? 0);
    return delta || byName(a, b);
  });
}

export function moveMachine(order, fromID, toID) {
  const result = normalizeMachineOrder(order);
  const from = result.indexOf(fromID), to = result.indexOf(toID);
  if (from < 0 || to < 0 || from === to) return result;
  result.splice(to, 0, result.splice(from, 1)[0]);
  return result;
}

// Reject older hubs that silently ignore an unknown PATCH field.
export function acceptedMachineOrder(response, expected) {
  return response?.default_sort === 'manual' && Array.isArray(response.machine_order)
    && response.machine_order.length === expected.length
    && expected.every((id, i) => response.machine_order[i] === id);
}

// Ordered preference PATCHes keep an older sort request from committing after
// a manual arrangement. Failed requests do not poison the next retry.
export function createPreferenceWriter() {
  let tail = Promise.resolve();
  return write => {
    const next = tail.then(write);
    tail = next.catch(() => {});
    return next;
  };
}
