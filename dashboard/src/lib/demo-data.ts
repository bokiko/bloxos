export interface GPUInfo {
  index: number;
  name: string;
  temp_c: number;
  util_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  power_watts: number;
  fan_percent: number;
}

export interface MachineMetrics {
  machine_id: string;
  hostname: string;
  ip?: string;
  os?: string;
  cpu_percent: number;
  ram_used_bytes: number;
  ram_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  gpu_temp?: number;
  gpu_util_percent?: number;
  gpu_vram_used_bytes?: number;
  gpu_vram_total_bytes?: number;
  gpus?: GPUInfo[];
  tags?: string;
  timestamp: string;
  last_seen?: number; // epoch ms, set by dashboard
}

export interface AlertData {
  id: string;
  rule_id?: string;
  machine_id: string;
  message: string;
  severity: string;
  status: string;
  triggered_at?: string;
  resolved_at?: string;
  hostname?: string;
}

export const demoMachines: MachineMetrics[] = [
  {
    machine_id: "ai-03",
    hostname: "ai-03",
    ip: "192.168.16.205",
    os: "linux",
    cpu_percent: 23,
    ram_used_bytes: 8 * 1024 ** 3,
    ram_total_bytes: 32 * 1024 ** 3,
    disk_used_bytes: 450 * 1024 ** 3,
    disk_total_bytes: 1800 * 1024 ** 3,
    gpu_temp: 45,
    gpu_util_percent: 30,
    gpu_vram_used_bytes: 4 * 1024 ** 3,
    gpu_vram_total_bytes: 8 * 1024 ** 3,
    tags: "inference,production",
    timestamp: new Date().toISOString(),
    last_seen: Date.now(),
  },
  {
    machine_id: "ai-09",
    hostname: "ai-09",
    ip: "192.168.7.67",
    os: "linux",
    cpu_percent: 67,
    ram_used_bytes: 24 * 1024 ** 3,
    ram_total_bytes: 30 * 1024 ** 3,
    disk_used_bytes: 900 * 1024 ** 3,
    disk_total_bytes: 1800 * 1024 ** 3,
    gpu_temp: 72,
    gpu_util_percent: 85,
    gpu_vram_used_bytes: 18 * 1024 ** 3,
    gpu_vram_total_bytes: 24 * 1024 ** 3,
    tags: "inference",
    timestamp: new Date().toISOString(),
    last_seen: Date.now(),
  },
  {
    machine_id: "ai-debian",
    hostname: "ai-debian",
    ip: "192.168.7.236",
    os: "linux",
    cpu_percent: 45,
    ram_used_bytes: 18 * 1024 ** 3,
    ram_total_bytes: 30 * 1024 ** 3,
    disk_used_bytes: 800 * 1024 ** 3,
    disk_total_bytes: 1800 * 1024 ** 3,
    gpu_temp: 85,
    gpu_util_percent: 92,
    gpu_vram_used_bytes: 6 * 1024 ** 3,
    gpu_vram_total_bytes: 8 * 1024 ** 3,
    tags: "dev",
    timestamp: new Date().toISOString(),
    last_seen: Date.now(),
  },
  {
    machine_id: "ic-brain",
    hostname: "IC-Brain",
    ip: "192.168.7.99",
    os: "linux",
    cpu_percent: 12,
    ram_used_bytes: 4 * 1024 ** 3,
    ram_total_bytes: 16 * 1024 ** 3,
    disk_used_bytes: 200 * 1024 ** 3,
    disk_total_bytes: 500 * 1024 ** 3,
    tags: "proxmox",
    timestamp: new Date().toISOString(),
    last_seen: Date.now(),
  },
  {
    machine_id: "zicoai",
    hostname: "zicoai",
    ip: "192.168.7.16",
    os: "linux",
    cpu_percent: 55,
    ram_used_bytes: 20 * 1024 ** 3,
    ram_total_bytes: 30 * 1024 ** 3,
    disk_used_bytes: 600 * 1024 ** 3,
    disk_total_bytes: 1800 * 1024 ** 3,
    gpu_temp: 68,
    gpu_util_percent: 60,
    gpu_vram_used_bytes: 8 * 1024 ** 3,
    gpu_vram_total_bytes: 12 * 1024 ** 3,
    tags: "inference",
    timestamp: new Date().toISOString(),
    last_seen: Date.now(),
  },
  {
    machine_id: "fat-lolo",
    hostname: "fat-lolo",
    ip: "192.168.16.85",
    os: "linux",
    cpu_percent: 0,
    ram_used_bytes: 0,
    ram_total_bytes: 8 * 1024 ** 3,
    disk_used_bytes: 0,
    disk_total_bytes: 500 * 1024 ** 3,
    timestamp: new Date(Date.now() - 300000).toISOString(),
    last_seen: Date.now() - 300000,
  },
];
