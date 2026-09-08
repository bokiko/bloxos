export function normalizeMachineOrder(value: unknown): string[];
export function orderMachines<T extends {machine_id: string; hostname?: string; cpu_percent?: number; gpu_temp?: number}>(machines: T[], options?: {sort?: string; order?: string[]; pinned?: string[]; status?: (machine: T) => number}): T[];
export function moveMachine(order: string[], fromID: string, toID: string): string[];
export function acceptedMachineOrder(response: unknown, expected: string[]): boolean;
export function createPreferenceWriter(): <T>(write: () => Promise<T>) => Promise<T>;
