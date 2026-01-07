'use client';

import { useEffect, useState, useCallback } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import dynamic from 'next/dynamic';
import { useWebSocket } from '../../../hooks/useWebSocket';

// Dynamic import for Terminal to avoid SSR issues
const Terminal = dynamic(() => import('../../../components/Terminal'), { ssr: false });

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface GPU {
  id: string;
  index: number;
  name: string;
  vendor: string;
  vram: number;
  busId: string | null;
  temperature: number | null;
  memTemp: number | null;
  fanSpeed: number | null;
  powerDraw: number | null;
  coreClock: number | null;
  memoryClock: number | null;
  hashrate: number | null;
}

interface MinerInstance {
  id: string;
  minerName: string;
  algo: string;
  pool: string;
  wallet: string;
  status: string;
  hashrate: number | null;
  accepted: number;
  rejected: number;
  startedAt: string | null;
  pid: number | null;
}

interface RigEvent {
  id: string;
  type: string;
  severity: string;
  message: string;
  timestamp: string;
}

interface CPU {
  id: string;
  model: string;
  vendor: string;
  cores: number;
  threads: number;
  temperature: number | null;
  usage: number | null;
  frequency: number | null;
  maxFrequency: number | null;
  powerDraw: number | null;
  hashrate: number | null;
}

interface FlightSheet {
  id: string;
  name: string;
  coin: string;
  wallet: { name: string; address: string };
  pool: { name: string; url: string };
  miner: { name: string; version: string; algo: string };
}

interface OCProfile {
  id: string;
  name: string;
  vendor: string;
  powerLimit: number | null;
  coreOffset: number | null;
  memOffset: number | null;
  coreLock: number | null;
  memLock: number | null;
  fanSpeed: number | null;
}

interface RigGroup {
  id: string;
  name: string;
  color: string;
}

interface Rig {
  id: string;
  name: string;
  hostname: string;
  ipAddress: string | null;
  macAddress: string | null;
  os: string | null;
  osVersion: string | null;
  agentVersion: string | null;
  token: string;
  status: string;
  lastSeen: string | null;
  cpuMiningEnabled: boolean;
  gpuMiningEnabled: boolean;
  flightSheetId: string | null;
  flightSheet: FlightSheet | null;
  ocProfileId: string | null;
  ocProfile: OCProfile | null;
  groups: RigGroup[];  // Changed to array for many-to-many
  cpu: CPU | null;
  gpus: GPU[];
  minerInstances: MinerInstance[];
  events: RigEvent[];
  createdAt: string;
}

// Icons
const RefreshIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
  </svg>
);

const BackIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 19l-7-7m0 0l7-7m-7 7h18" />
  </svg>
);

const TrashIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
  </svg>
);

const TerminalIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
  </svg>
);

const ChevronIcon = ({ expanded }: { expanded: boolean }) => (
  <svg 
    className={`w-5 h-5 transition-transform duration-200 ${expanded ? 'rotate-180' : ''}`} 
    fill="none" 
    stroke="currentColor" 
    viewBox="0 0 24 24"
  >
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
  </svg>
);

// Collapsible Section Component
function CollapsibleSection({ 
  title, 
  subtitle,
  icon, 
  iconBg,
  defaultExpanded = true,
  children,
  actions
}: { 
  title: string;
  subtitle?: string;
  icon: React.ReactNode;
  iconBg: string;
  defaultExpanded?: boolean;
  children: React.ReactNode;
  actions?: React.ReactNode;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full px-5 py-4 flex items-center justify-between hover:bg-slate-700/20 transition-colors"
      >
        <div className="flex items-center gap-3">
          <div className={`w-8 h-8 rounded-lg ${iconBg} flex items-center justify-center`}>
            {icon}
          </div>
          <div className="text-left">
            <h2 className="text-lg font-semibold">{title}</h2>
            {subtitle && <p className="text-sm text-slate-400">{subtitle}</p>}
          </div>
        </div>
        <div className="flex items-center gap-3">
          {actions && <div onClick={(e) => e.stopPropagation()}>{actions}</div>}
          <ChevronIcon expanded={expanded} />
        </div>
      </button>
      {expanded && (
        <div className="border-t border-slate-700/50">
          {children}
        </div>
      )}
    </div>
  );
}

// Format bytes to human readable
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

// System Info Component
function SystemInfoSection({ rigId, basicInfo }: { 
  rigId: string; 
  basicInfo: {
    hostname: string;
    ipAddress: string | null;
    macAddress: string | null;
    os: string | null;
    osVersion: string | null;
    agentVersion: string | null;
    token: string;
    createdAt: string;
  }
}) {
  const [sysInfo, setSysInfo] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [expanded, setExpanded] = useState(false);

  async function fetchSystemInfo() {
    setLoading(true);
    try {
      const res = await fetch(`http://${window.location.hostname}:3001/api/ssh/rig/${rigId}/system-info`, {
        credentials: 'include',
      });
      if (res.ok) {
        const data = await res.json();
        setSysInfo(data);
        setExpanded(true);
      }
    } catch (err) {
      console.error('Failed to fetch system info');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
      <div className="px-5 py-4 border-b border-slate-700/50 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-slate-500/20 flex items-center justify-center">
            <svg className="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
            </svg>
          </div>
          <h2 className="text-lg font-semibold">System Information</h2>
        </div>
        <button
          onClick={fetchSystemInfo}
          disabled={loading}
          className="px-3 py-1.5 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm transition-colors disabled:opacity-50"
        >
          {loading ? 'Loading...' : expanded ? 'Refresh' : 'Load Details'}
        </button>
      </div>

      <div className="p-5">
        {/* Basic Info - Always Shown */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
          <div>
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Hostname</p>
            <p className="font-medium">{basicInfo.hostname}</p>
          </div>
          <div>
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">IP Address</p>
            <p className="font-medium font-mono text-sm">{basicInfo.ipAddress || 'N/A'}</p>
          </div>
          <div>
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">MAC Address</p>
            <p className="font-medium font-mono text-sm">{basicInfo.macAddress || 'N/A'}</p>
          </div>
          <div>
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Operating System</p>
            <p className="font-medium">{`${basicInfo.os || ''} ${basicInfo.osVersion || ''}`.trim() || 'Unknown'}</p>
          </div>
        </div>

        {/* Rig Token */}
        <div className="mb-4">
          <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Rig Token</p>
          <div className="flex items-center gap-2">
            <code className="flex-1 text-xs font-mono bg-slate-900/50 px-3 py-2 rounded-lg truncate">{basicInfo.token}</code>
            <button
              onClick={() => navigator.clipboard.writeText(basicInfo.token)}
              className="p-2 hover:bg-slate-700 rounded-lg transition-colors"
              title="Copy token"
            >
              <svg className="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
            </button>
          </div>
        </div>

        {/* Detailed Info - Shown when loaded */}
        {sysInfo && expanded && (
          <div className="space-y-4 pt-4 border-t border-slate-700/50">
            {/* OS & System */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-blue-400"></span>
                Operating System
              </h4>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 bg-slate-900/30 rounded-lg p-3">
                <div><span className="text-xs text-slate-500">OS:</span> <span className="text-sm">{sysInfo.os?.name}</span></div>
                <div><span className="text-xs text-slate-500">Kernel:</span> <span className="text-sm font-mono">{sysInfo.os?.kernel}</span></div>
                <div><span className="text-xs text-slate-500">Arch:</span> <span className="text-sm">{sysInfo.os?.arch}</span></div>
                <div><span className="text-xs text-slate-500">Uptime:</span> <span className="text-sm">{sysInfo.os?.uptime}</span></div>
              </div>
            </div>

            {/* CPU */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-green-400"></span>
                CPU
              </h4>
              <div className="grid grid-cols-2 md:grid-cols-3 gap-3 bg-slate-900/30 rounded-lg p-3">
                <div className="col-span-2 md:col-span-3"><span className="text-xs text-slate-500">Model:</span> <span className="text-sm">{sysInfo.cpu?.model}</span></div>
                <div><span className="text-xs text-slate-500">Cores:</span> <span className="text-sm">{sysInfo.cpu?.cores}</span></div>
                <div><span className="text-xs text-slate-500">Threads:</span> <span className="text-sm">{sysInfo.cpu?.threads}</span></div>
                <div><span className="text-xs text-slate-500">Max Freq:</span> <span className="text-sm">{sysInfo.cpu?.maxFrequency} MHz</span></div>
                <div><span className="text-xs text-slate-500">Current Freq:</span> <span className="text-sm">{sysInfo.cpu?.currentFrequency} MHz</span></div>
                <div><span className="text-xs text-slate-500">Cache:</span> <span className="text-sm">{sysInfo.cpu?.cache}</span></div>
              </div>
            </div>

            {/* Memory */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-purple-400"></span>
                Memory
              </h4>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 bg-slate-900/30 rounded-lg p-3">
                <div><span className="text-xs text-slate-500">Total:</span> <span className="text-sm">{formatBytes(sysInfo.memory?.total || 0)}</span></div>
                <div><span className="text-xs text-slate-500">Used:</span> <span className="text-sm">{formatBytes(sysInfo.memory?.used || 0)}</span></div>
                <div><span className="text-xs text-slate-500">Free:</span> <span className="text-sm">{formatBytes(sysInfo.memory?.free || 0)}</span></div>
                <div><span className="text-xs text-slate-500">Swap:</span> <span className="text-sm">{formatBytes(sysInfo.memory?.swapTotal || 0)}</span></div>
              </div>
            </div>

            {/* Storage */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-yellow-400"></span>
                Storage
              </h4>
              <div className="bg-slate-900/30 rounded-lg p-3">
                <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-2">
                  <div><span className="text-xs text-slate-500">Root Total:</span> <span className="text-sm">{formatBytes(sysInfo.storage?.root?.total || 0)}</span></div>
                  <div><span className="text-xs text-slate-500">Used:</span> <span className="text-sm">{formatBytes(sysInfo.storage?.root?.used || 0)}</span></div>
                  <div><span className="text-xs text-slate-500">Free:</span> <span className="text-sm">{formatBytes(sysInfo.storage?.root?.free || 0)}</span></div>
                  <div><span className="text-xs text-slate-500">Usage:</span> <span className="text-sm">{sysInfo.storage?.root?.usedPercent}</span></div>
                </div>
                {sysInfo.storage?.disks?.length > 0 && (
                  <div className="mt-2 pt-2 border-t border-slate-700/50">
                    <p className="text-xs text-slate-500 mb-1">Disks:</p>
                    {sysInfo.storage.disks.map((disk: any, i: number) => (
                      <div key={i} className="text-sm font-mono">
                        {disk.name}: {formatBytes(disk.size)} - {disk.model}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>

            {/* Motherboard & BIOS */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-orange-400"></span>
                Motherboard & BIOS
              </h4>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3 bg-slate-900/30 rounded-lg p-3">
                <div>
                  <p className="text-xs text-slate-500">Motherboard</p>
                  <p className="text-sm">{sysInfo.motherboard?.vendor} {sysInfo.motherboard?.name} {sysInfo.motherboard?.version}</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">BIOS</p>
                  <p className="text-sm">{sysInfo.bios?.vendor} {sysInfo.bios?.version} ({sysInfo.bios?.date})</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">System</p>
                  <p className="text-sm">{sysInfo.system?.vendor} {sysInfo.system?.product}</p>
                </div>
              </div>
            </div>

            {/* Network */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-cyan-400"></span>
                Network
              </h4>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3 bg-slate-900/30 rounded-lg p-3">
                <div>
                  <p className="text-xs text-slate-500">IP Addresses</p>
                  <p className="text-sm font-mono">{sysInfo.network?.ipAddresses?.join(', ') || 'N/A'}</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">MAC Addresses</p>
                  <p className="text-sm font-mono">{sysInfo.network?.macAddresses?.join(', ') || 'N/A'}</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">Gateway</p>
                  <p className="text-sm font-mono">{sysInfo.network?.gateway}</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">DNS</p>
                  <p className="text-sm font-mono">{sysInfo.network?.dns?.join(', ') || 'N/A'}</p>
                </div>
              </div>
            </div>

            {/* GPU */}
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-red-400"></span>
                GPU
              </h4>
              <div className="bg-slate-900/30 rounded-lg p-3">
                <div className="mb-2">
                  <span className="text-xs text-slate-500">Driver:</span> <span className="text-sm">{sysInfo.gpu?.driver}</span>
                </div>
                {sysInfo.gpu?.devices?.length > 0 && (
                  <div className="space-y-2">
                    {sysInfo.gpu.devices.map((gpu: any, i: number) => (
                      <div key={i} className="text-sm bg-slate-800/50 rounded p-2">
                        <p className="font-medium">[{gpu.index}] {gpu.name}</p>
                        <p className="text-xs text-slate-400 font-mono">
                          VRAM: {gpu.vram} MB | Bus: {gpu.busId} | Power Limit: {gpu.powerLimit}W
                        </p>
                      </div>
                    ))}
                  </div>
                )}
                {sysInfo.gpu?.pciDevices?.length > 0 && (
                  <div className="mt-2 pt-2 border-t border-slate-700/50">
                    <p className="text-xs text-slate-500 mb-1">PCI Devices:</p>
                    {sysInfo.gpu.pciDevices.map((device: string, i: number) => (
                      <p key={i} className="text-xs font-mono text-slate-400">{device}</p>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

interface FlightSheetOption {
  id: string;
  name: string;
  coin: string;
}

export default function RigDetailPage() {
  const params = useParams();
  const router = useRouter();
  const [rig, setRig] = useState<Rig | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [command, setCommand] = useState('');
  const [commandOutput, setCommandOutput] = useState<string | null>(null);
  const [commandLoading, setCommandLoading] = useState(false);
  const [commandHistory, setCommandHistory] = useState<string[]>([]);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [flightSheets, setFlightSheets] = useState<FlightSheetOption[]>([]);
  const [ocProfiles, setOcProfiles] = useState<OCProfile[]>([]);
  const [rigGroups, setRigGroups] = useState<RigGroup[]>([]);
  const [minerRunning, setMinerRunning] = useState(false);
  const [minerLoading, setMinerLoading] = useState(false);
  const [ocApplying, setOcApplying] = useState(false);
  const [alertConfig, setAlertConfig] = useState({
    tempAlertEnabled: true,
    offlineAlertEnabled: true,
    hashrateAlertEnabled: true,
    gpuTempThreshold: 80,
    cpuTempThreshold: 85,
    offlineThreshold: 300,
    hashrateDropPercent: 20,
  });
  const [alertConfigLoading, setAlertConfigLoading] = useState(false);
  const [showTerminal, setShowTerminal] = useState(false);
  const REFRESH_INTERVAL = 30000; // 30 seconds

  // WebSocket handler for real-time rig updates
  const handleRigsUpdate = useCallback((data: unknown) => {
    if (Array.isArray(data)) {
      const updatedRig = (data as Rig[]).find(r => r.id === params.id);
      if (updatedRig) {
        setRig(updatedRig);
        setLastUpdated(new Date());
        setLoading(false);
      }
    }
  }, [params.id]);

  // Connect to WebSocket for real-time updates
  const { isConnected } = useWebSocket({
    onRigsUpdate: handleRigsUpdate,
  });

  useEffect(() => {
    if (params.id) {
      fetchRig(params.id as string);
      fetchFlightSheets();
      fetchOcProfiles();
      fetchRigGroups();
      fetchMinerStatus(params.id as string);
      fetchAlertConfig(params.id as string);

      // Only use polling as fallback when WebSocket is not connected
      let intervalId: NodeJS.Timeout | null = null;
      if (autoRefresh && !isConnected) {
        intervalId = setInterval(() => {
          fetchRig(params.id as string);
          fetchMinerStatus(params.id as string);
        }, REFRESH_INTERVAL);
      }

      return () => {
        if (intervalId) clearInterval(intervalId);
      };
    }
  }, [params.id, autoRefresh, isConnected]);

  async function fetchFlightSheets() {
    try {
      const res = await fetch(`${getApiUrl()}/api/flight-sheets`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setFlightSheets(data);
      }
    } catch (err) {
      console.error('Failed to fetch flight sheets');
    }
  }

  async function fetchOcProfiles() {
    try {
      const res = await fetch(`${getApiUrl()}/api/oc-profiles`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setOcProfiles(data);
      }
    } catch (err) {
      console.error('Failed to fetch OC profiles');
    }
  }

  async function fetchRigGroups() {
    try {
      const res = await fetch(`${getApiUrl()}/api/rig-groups`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setRigGroups(data);
      }
    } catch (err) {
      console.error('Failed to fetch rig groups');
    }
  }

  async function updateGroups(groupIds: string[]) {
    if (!rig) return;
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/groups`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ groupIds }),
      });

      if (res.ok) {
        await fetchRig(rig.id);
      } else {
        const data = await res.json();
        setError(data.error || 'Failed to update groups');
      }
    } catch (err) {
      setError('Failed to update groups');
    }
  }

  function toggleGroup(groupId: string) {
    if (!rig) return;
    const currentIds = rig.groups.map(g => g.id);
    const newIds = currentIds.includes(groupId)
      ? currentIds.filter(id => id !== groupId)
      : [...currentIds, groupId];
    updateGroups(newIds);
  }

  async function fetchAlertConfig(rigId: string) {
    try {
      const res = await fetch(`${getApiUrl()}/api/alerts/config/${rigId}`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      setAlertConfig(data);
    } catch (err) {
      console.error('Failed to fetch alert config');
    }
  }

  async function updateAlertConfig(updates: Partial<typeof alertConfig>) {
    if (!rig) return;
    setAlertConfigLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/alerts/config/${rig.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(updates),
      });

      if (res.ok) {
        const data = await res.json();
        setAlertConfig(data);
      }
    } catch (err) {
      console.error('Failed to update alert config');
    } finally {
      setAlertConfigLoading(false);
    }
  }

  async function fetchMinerStatus(id: string) {
    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${id}/miner/status`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      setMinerRunning(data.running);
    } catch (err) {
      console.error('Failed to fetch miner status');
    }
  }

  async function fetchRig(id: string) {
    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${id}`, {
        credentials: 'include',
      });
      if (!res.ok) {
        setError('Rig not found');
        return;
      }
      const data = await res.json();
      setRig(data);
      setLastUpdated(new Date());
    } catch (err) {
      setError('Failed to fetch rig');
    } finally {
      setLoading(false);
    }
  }

  function formatTimeAgo(date: Date | null) {
    if (!date) return '';
    const diff = Date.now() - date.getTime();
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return `${secs}s ago`;
    const mins = Math.floor(secs / 60);
    return `${mins}m ago`;
  }

  async function handleDelete() {
    if (!rig) return;
    if (!confirm(`Are you sure you want to delete "${rig.name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}`, {
        method: 'DELETE',
        credentials: 'include',
      });

      if (res.ok) {
        router.push('/rigs');
      }
    } catch (err) {
      setError('Failed to delete rig');
    }
  }

  async function toggleMonitoring(type: 'cpu' | 'gpu') {
    if (!rig) return;
    
    const body = type === 'cpu' 
      ? { cpuMiningEnabled: !rig.cpuMiningEnabled }
      : { gpuMiningEnabled: !rig.gpuMiningEnabled };

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/monitoring`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(body),
      });

      if (res.ok) {
        // Refresh rig data
        await fetchRig(rig.id);
      }
    } catch (err) {
      setError('Failed to update monitoring settings');
    }
  }

  async function assignFlightSheet(flightSheetId: string | null) {
    if (!rig) return;
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/flight-sheet`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ flightSheetId }),
      });

      if (res.ok) {
        await fetchRig(rig.id);
      } else {
        const data = await res.json();
        setError(data.error || 'Failed to assign flight sheet');
      }
    } catch (err) {
      setError('Failed to assign flight sheet');
    }
  }

  async function handleStartMiner() {
    if (!rig) return;
    setMinerLoading(true);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/miner/start`, {
        method: 'POST',
        credentials: 'include',
      });

      const data = await res.json();
      if (res.ok) {
        setMinerRunning(true);
        await fetchRig(rig.id);
      } else {
        setError(data.message || 'Failed to start miner');
      }
    } catch (err) {
      setError('Failed to start miner');
    } finally {
      setMinerLoading(false);
    }
  }

  async function handleStopMiner() {
    if (!rig) return;
    setMinerLoading(true);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/miner/stop`, {
        method: 'POST',
        credentials: 'include',
      });

      const data = await res.json();
      if (res.ok) {
        setMinerRunning(false);
        await fetchRig(rig.id);
      } else {
        setError(data.message || 'Failed to stop miner');
      }
    } catch (err) {
      setError('Failed to stop miner');
    } finally {
      setMinerLoading(false);
    }
  }

  async function assignOcProfile(ocProfileId: string | null) {
    if (!rig) return;
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/oc-profile`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ ocProfileId }),
      });

      if (res.ok) {
        await fetchRig(rig.id);
      } else {
        const data = await res.json();
        setError(data.error || 'Failed to assign OC profile');
      }
    } catch (err) {
      setError('Failed to assign OC profile');
    }
  }

  async function handleApplyOC() {
    if (!rig) return;
    setOcApplying(true);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/oc-profile/apply`, {
        method: 'POST',
        credentials: 'include',
      });

      const data = await res.json();
      if (res.ok) {
        await fetchRig(rig.id);
      } else {
        setError(data.message || 'Failed to apply OC profile');
      }
    } catch (err) {
      setError('Failed to apply OC profile');
    } finally {
      setOcApplying(false);
    }
  }

  async function handleResetOC() {
    if (!rig) return;
    if (!confirm('Reset OC settings to default?')) return;
    setOcApplying(true);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${rig.id}/oc-profile/reset`, {
        method: 'POST',
        credentials: 'include',
      });

      const data = await res.json();
      if (!res.ok) {
        setError(data.message || 'Failed to reset OC');
      }
    } catch (err) {
      setError('Failed to reset OC');
    } finally {
      setOcApplying(false);
    }
  }

  async function handleRefresh() {
    if (!rig) return;
    setRefreshing(true);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/ssh/rig/${rig.id}/refresh`, {
        method: 'POST',
        credentials: 'include',
      });

      if (res.ok) {
        await fetchRig(rig.id);
      } else {
        const data = await res.json();
        setError(data.message || 'Failed to refresh rig');
      }
    } catch (err) {
      setError('Failed to refresh rig');
    } finally {
      setRefreshing(false);
    }
  }

  async function handleExecuteCommand(e: React.FormEvent) {
    e.preventDefault();
    if (!rig || !command.trim()) return;
    
    setCommandLoading(true);
    setCommandOutput(null);
    setError(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/ssh/rig/${rig.id}/exec`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ command }),
      });

      const data = await res.json();

      if (data.success) {
        setCommandOutput(data.output);
        setCommandHistory(prev => [command, ...prev.filter(c => c !== command)].slice(0, 10));
      } else {
        setError(data.message || 'Command failed');
      }
    } catch (err) {
      setError('Failed to execute command');
    } finally {
      setCommandLoading(false);
    }
  }

  function getStatusDot(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500 shadow-green-500/50';
      case 'WARNING': return 'bg-yellow-500 shadow-yellow-500/50';
      case 'ERROR': return 'bg-red-500 shadow-red-500/50';
      default: return 'bg-slate-500 shadow-slate-500/50';
    }
  }

  function getStatusBadge(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500/10 text-green-400 ring-1 ring-green-500/30';
      case 'WARNING': return 'bg-yellow-500/10 text-yellow-400 ring-1 ring-yellow-500/30';
      case 'ERROR': return 'bg-red-500/10 text-red-400 ring-1 ring-red-500/30';
      default: return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-500/30';
    }
  }

  function getSeverityIcon(severity: string) {
    switch (severity) {
      case 'INFO': return 'bg-blue-500/20 text-blue-400';
      case 'WARNING': return 'bg-yellow-500/20 text-yellow-400';
      case 'ERROR':
      case 'CRITICAL': return 'bg-red-500/20 text-red-400';
      default: return 'bg-slate-500/20 text-slate-400';
    }
  }

  function formatDate(dateString: string) {
    return new Date(dateString).toLocaleString();
  }

  function formatMinerUptime(startedAt: string) {
    const diff = Date.now() - new Date(startedAt).getTime();
    const hours = Math.floor(diff / 3600000);
    const mins = Math.floor((diff % 3600000) / 60000);
    if (hours > 24) {
      const days = Math.floor(hours / 24);
      return `${days}d ${hours % 24}h`;
    }
    if (hours > 0) {
      return `${hours}h ${mins}m`;
    }
    return `${mins}m`;
  }

  function formatLastSeen(lastSeen: string | null) {
    if (!lastSeen) return 'Never';
    const diff = Date.now() - new Date(lastSeen).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'Just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
  }

  function getTempColor(temp: number | null) {
    if (temp === null) return '';
    if (temp > 80) return 'text-red-400';
    if (temp > 70) return 'text-yellow-400';
    return 'text-green-400';
  }

  if (loading) {
    return (
      <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
        <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
        <p className="text-slate-400 mt-3">Loading rig details...</p>
      </div>
    );
  }

  if (!rig) {
    return (
      <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
        <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-red-500/20 flex items-center justify-center">
          <svg className="w-8 h-8 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p className="text-red-400 mb-4">{error || 'Rig not found'}</p>
        <Link
          href="/rigs"
          className="inline-flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors"
        >
          <BackIcon /> Back to Rigs
        </Link>
      </div>
    );
  }

  const totalHashrate = rig.gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);
  const gpuPower = rig.gpus.reduce((sum, gpu) => sum + (gpu.powerDraw || 0), 0);
  const cpuPower = rig.cpu?.powerDraw || 0;
  const totalPower = gpuPower + cpuPower;
  const temps = rig.gpus.map(g => g.temperature).filter((t): t is number => t !== null);
  const avgTemp = temps.length > 0 ? temps.reduce((a, b) => a + b, 0) / temps.length : null;
  const maxTemp = temps.length > 0 ? Math.max(...temps) : null;

  return (
    <div className="space-y-6">
      {/* Error Banner */}
      {error && (
        <div className="flex items-center justify-between p-4 bg-red-500/10 border border-red-500/30 rounded-xl text-red-400">
          <div className="flex items-center gap-3">
            <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{error}</span>
          </div>
          <button
            onClick={() => setError(null)}
            className="p-1 hover:bg-red-500/20 rounded-lg transition-colors"
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      )}

      {/* Header */}
      <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-6">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div className="flex items-center gap-4">
            <div className={`w-4 h-4 rounded-full ${getStatusDot(rig.status)} shadow-lg`}></div>
            <div>
              <div className="flex items-center gap-3 flex-wrap">
                <h1 className="text-2xl font-bold">{rig.name}</h1>
                <span className={`px-2.5 py-1 rounded-lg text-xs font-medium ${getStatusBadge(rig.status)}`}>
                  {rig.status}
                </span>
                {/* Group Tags */}
                <div className="flex items-center gap-2 flex-wrap">
                  {rig.groups.map((g) => (
                    <span
                      key={g.id}
                      onClick={() => toggleGroup(g.id)}
                      className="px-2 py-0.5 rounded text-xs font-medium cursor-pointer hover:opacity-75 transition-opacity"
                      style={{ backgroundColor: `${g.color}30`, color: g.color, border: `1px solid ${g.color}` }}
                      title="Click to remove"
                    >
                      {g.name} ×
                    </span>
                  ))}
                  {/* Add Group Dropdown */}
                  <div className="relative group">
                    <button className="px-2 py-0.5 text-xs rounded border border-dashed border-slate-500 text-slate-400 hover:border-blox-500 hover:text-blox-400 transition-colors">
                      + Group
                    </button>
                    <div className="absolute left-0 top-full mt-1 w-40 bg-slate-800 border border-slate-700 rounded-lg shadow-xl z-50 hidden group-hover:block">
                      {rigGroups
                        .filter(g => !rig.groups.some(rg => rg.id === g.id))
                        .map(g => (
                          <button
                            key={g.id}
                            onClick={() => toggleGroup(g.id)}
                            className="w-full text-left px-3 py-2 text-sm hover:bg-slate-700 transition-colors flex items-center gap-2"
                          >
                            <span className="w-2 h-2 rounded-full" style={{ backgroundColor: g.color }}></span>
                            {g.name}
                          </button>
                        ))}
                      {rigGroups.filter(g => !rig.groups.some(rg => rg.id === g.id)).length === 0 && (
                        <p className="px-3 py-2 text-xs text-slate-500">All groups assigned</p>
                      )}
                    </div>
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-2 mt-1 text-sm text-slate-400">
                <span className="font-mono">{rig.ipAddress || 'No IP'}</span>
                <span>•</span>
                <span>{rig.hostname}</span>
                <span>•</span>
                <span>{rig.os} {rig.osVersion}</span>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-3">
            {/* Live connection toggle */}
            <button
              onClick={() => setAutoRefresh(!autoRefresh)}
              className={`flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-all ${
                isConnected
                  ? 'bg-green-500/20 text-green-400'
                  : autoRefresh
                  ? 'bg-yellow-500/20 text-yellow-400'
                  : 'bg-slate-700 text-slate-400'
              }`}
              title={isConnected ? 'WebSocket connected' : autoRefresh ? 'Polling mode' : 'Updates paused'}
            >
              <span className={`w-2 h-2 rounded-full ${
                isConnected ? 'bg-green-400 animate-pulse' : 
                autoRefresh ? 'bg-yellow-400 animate-pulse' : 
                'bg-slate-500'
              }`}></span>
              {isConnected ? 'Live' : autoRefresh ? 'Polling' : 'Paused'}
            </button>
            {lastUpdated && (
              <span className="text-slate-500 text-xs hidden md:inline">
                {formatTimeAgo(lastUpdated)}
              </span>
            )}
            <button
              onClick={handleRefresh}
              disabled={refreshing}
              className="inline-flex items-center gap-2 px-4 py-2 bg-blox-500/20 hover:bg-blox-500/30 text-blox-400 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              <RefreshIcon />
              {refreshing ? 'Refreshing...' : 'Refresh'}
            </button>
            <Link
              href="/rigs"
              className="inline-flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors"
            >
              <BackIcon /> Back
            </Link>
            <button
              onClick={() => setShowTerminal(true)}
              className="inline-flex items-center gap-2 px-4 py-2 bg-purple-500/10 hover:bg-purple-500/20 text-purple-400 rounded-lg text-sm font-medium transition-colors"
            >
              <TerminalIcon /> Terminal
            </button>
            <button
              onClick={handleDelete}
              className="inline-flex items-center gap-2 px-4 py-2 bg-red-500/10 hover:bg-red-500/20 text-red-400 rounded-lg text-sm font-medium transition-colors"
            >
              <TrashIcon /> Delete
            </button>
          </div>
        </div>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-4">
          <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">GPUs</p>
          <p className="text-xl font-bold text-blue-400">{rig.gpus.length}</p>
        </div>
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-4">
          <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Hashrate</p>
          <p className="text-xl font-bold text-purple-400">{totalHashrate.toFixed(1)} MH/s</p>
        </div>
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-4">
          <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Avg Temp</p>
          <p className={`text-xl font-bold ${getTempColor(avgTemp) || 'text-slate-400'}`}>
            {avgTemp !== null ? `${avgTemp.toFixed(0)}°C` : 'N/A'}
          </p>
        </div>
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-4">
          <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Last Seen</p>
          <p className="text-xl font-bold text-slate-300">{formatLastSeen(rig.lastSeen)}</p>
        </div>
      </div>

      {/* Power Breakdown */}
      <CollapsibleSection
        title="Power Consumption"
        subtitle="Real-time power breakdown"
        icon={<svg className="w-4 h-4 text-yellow-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>}
        iconBg="bg-yellow-500/20"
        defaultExpanded={true}
      >
        <div className="p-4 grid grid-cols-3 gap-4">
          <div className="bg-slate-900/50 rounded-lg p-4 text-center">
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-2">GPU Power</p>
            <p className="text-2xl font-bold text-blue-400">{gpuPower} <span className="text-sm text-slate-400">W</span></p>
          </div>
          <div className="bg-slate-900/50 rounded-lg p-4 text-center">
            <p className="text-xs text-slate-500 uppercase tracking-wider mb-2">CPU Power</p>
            <p className="text-2xl font-bold text-green-400">{cpuPower} <span className="text-sm text-slate-400">W</span></p>
          </div>
          <div className="bg-gradient-to-br from-yellow-500/20 to-orange-500/20 rounded-lg p-4 text-center border border-yellow-500/30">
            <p className="text-xs text-yellow-400 uppercase tracking-wider mb-2">Total Power</p>
            <p className="text-2xl font-bold text-yellow-400">{totalPower} <span className="text-sm">W</span></p>
          </div>
        </div>
      </CollapsibleSection>

      {/* Monitoring Toggles */}
      <CollapsibleSection
        title="Mining Monitoring"
        subtitle="Toggle GPU/CPU monitoring"
        icon={<svg className="w-4 h-4 text-cyan-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" /></svg>}
        iconBg="bg-cyan-500/20"
        defaultExpanded={true}
        actions={
          <div className="flex items-center gap-2">
            <span className={`w-2 h-2 rounded-full ${rig.gpuMiningEnabled ? 'bg-blue-400' : 'bg-slate-500'}`}></span>
            <span className={`w-2 h-2 rounded-full ${rig.cpuMiningEnabled ? 'bg-green-400' : 'bg-slate-500'}`}></span>
          </div>
        }
      >
        <div className="p-4 flex items-center justify-end gap-4">
          <button
            onClick={() => toggleMonitoring('gpu')}
            className={`flex items-center gap-2 px-4 py-2 rounded-lg transition-all ${
              rig.gpuMiningEnabled
                ? 'bg-blue-500/20 text-blue-400 ring-1 ring-blue-500/30'
                : 'bg-slate-700 text-slate-400'
            }`}
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
            </svg>
            GPU {rig.gpuMiningEnabled ? 'On' : 'Off'}
          </button>
          <button
            onClick={() => toggleMonitoring('cpu')}
            className={`flex items-center gap-2 px-4 py-2 rounded-lg transition-all ${
              rig.cpuMiningEnabled
                ? 'bg-green-500/20 text-green-400 ring-1 ring-green-500/30'
                : 'bg-slate-700 text-slate-400'
            }`}
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
            </svg>
            CPU {rig.cpuMiningEnabled ? 'On' : 'Off'}
          </button>
        </div>
      </CollapsibleSection>

      {/* Flight Sheet & Miner Control */}
      <CollapsibleSection
        title="Flight Sheet & Miner"
        subtitle="Configure and control mining"
        icon={<svg className="w-4 h-4 text-purple-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" /></svg>}
        iconBg="bg-purple-500/20"
        defaultExpanded={true}
        actions={
          <span className={`w-2 h-2 rounded-full ${minerRunning ? 'bg-green-400 animate-pulse' : 'bg-slate-500'}`}></span>
        }
      >
        <div className="p-5 grid md:grid-cols-2 gap-4">
          {/* Flight Sheet Selection */}
          <div className="bg-slate-900/50 rounded-lg p-4">
            <label className="block text-sm font-medium text-slate-300 mb-2">Flight Sheet</label>
            <select
              value={rig.flightSheetId || ''}
              onChange={(e) => assignFlightSheet(e.target.value || null)}
              className="w-full px-4 py-2.5 bg-slate-800 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-purple-500"
            >
              <option value="">-- No Flight Sheet --</option>
              {flightSheets.map((fs) => (
                <option key={fs.id} value={fs.id}>
                  {fs.name} ({fs.coin})
                </option>
              ))}
            </select>
            
            {rig.flightSheet && (
              <div className="mt-3 text-sm space-y-1">
                <p><span className="text-slate-500">Miner:</span> <span className="text-slate-300">{rig.flightSheet.miner.name} ({rig.flightSheet.miner.algo})</span></p>
                <p><span className="text-slate-500">Pool:</span> <span className="text-slate-300">{rig.flightSheet.pool.name}</span></p>
                <p><span className="text-slate-500">Wallet:</span> <span className="text-slate-300">{rig.flightSheet.wallet.name}</span></p>
              </div>
            )}
          </div>

          {/* Miner Control */}
          <div className="bg-slate-900/50 rounded-lg p-4">
            <label className="block text-sm font-medium text-slate-300 mb-2">Miner Control</label>
            <div className="flex items-center gap-3">
              {!minerRunning ? (
                <button
                  onClick={handleStartMiner}
                  disabled={!rig.flightSheet || minerLoading}
                  className="flex-1 flex items-center justify-center gap-2 px-4 py-2.5 bg-green-600 hover:bg-green-700 disabled:bg-slate-700 disabled:text-slate-500 rounded-lg font-medium transition-colors"
                >
                  {minerLoading ? (
                    <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                  ) : (
                    <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
                      <path d="M8 5v14l11-7z" />
                    </svg>
                  )}
                  Start Miner
                </button>
              ) : (
                <button
                  onClick={handleStopMiner}
                  disabled={minerLoading}
                  className="flex-1 flex items-center justify-center gap-2 px-4 py-2.5 bg-red-600 hover:bg-red-700 disabled:bg-slate-700 rounded-lg font-medium transition-colors"
                >
                  {minerLoading ? (
                    <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                  ) : (
                    <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
                      <rect x="6" y="6" width="12" height="12" />
                    </svg>
                  )}
                  Stop Miner
                </button>
              )}
            </div>

            {/* Miner Status */}
            <div className="mt-3 flex items-center gap-2">
              <span className={`w-2 h-2 rounded-full ${minerRunning ? 'bg-green-400 animate-pulse' : 'bg-slate-500'}`}></span>
              <span className={`text-sm ${minerRunning ? 'text-green-400' : 'text-slate-400'}`}>
                {minerRunning ? 'Miner Running' : 'Miner Stopped'}
              </span>
            </div>

            {!rig.flightSheet && (
              <p className="mt-3 text-xs text-yellow-400">Assign a flight sheet to start mining</p>
            )}
          </div>
        </div>
      </CollapsibleSection>

      {/* OC Profile */}
      <CollapsibleSection
        title="Overclock Profile"
        subtitle="GPU overclocking settings"
        icon={<svg className="w-4 h-4 text-orange-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>}
        iconBg="bg-orange-500/20"
        defaultExpanded={true}
        actions={
          <span className={`w-2 h-2 rounded-full ${rig.ocProfile ? 'bg-orange-400' : 'bg-slate-500'}`}></span>
        }
      >
        <div className="p-5 grid md:grid-cols-2 gap-4">
          {/* OC Profile Selection */}
          <div className="bg-slate-900/50 rounded-lg p-4">
            <label className="block text-sm font-medium text-slate-300 mb-2">OC Profile</label>
            <select
              value={rig.ocProfileId || ''}
              onChange={(e) => assignOcProfile(e.target.value || null)}
              className="w-full px-4 py-2.5 bg-slate-800 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-orange-500"
            >
              <option value="">-- No OC Profile --</option>
              {ocProfiles.map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name} ({profile.vendor})
                </option>
              ))}
            </select>
            
            {rig.ocProfile && (
              <div className="mt-3 text-sm space-y-1">
                <p><span className="text-slate-500">Vendor:</span> <span className="text-slate-300">{rig.ocProfile.vendor}</span></p>
                {rig.ocProfile.powerLimit && (
                  <p><span className="text-slate-500">Power Limit:</span> <span className="text-slate-300">{rig.ocProfile.powerLimit}W</span></p>
                )}
                {rig.ocProfile.coreOffset && (
                  <p><span className="text-slate-500">Core Offset:</span> <span className="text-slate-300">{rig.ocProfile.coreOffset > 0 ? '+' : ''}{rig.ocProfile.coreOffset} MHz</span></p>
                )}
                {rig.ocProfile.memOffset && (
                  <p><span className="text-slate-500">Memory Offset:</span> <span className="text-slate-300">{rig.ocProfile.memOffset > 0 ? '+' : ''}{rig.ocProfile.memOffset} MHz</span></p>
                )}
                {rig.ocProfile.fanSpeed !== null && (
                  <p><span className="text-slate-500">Fan Speed:</span> <span className="text-slate-300">{rig.ocProfile.fanSpeed === 0 ? 'Auto' : `${rig.ocProfile.fanSpeed}%`}</span></p>
                )}
              </div>
            )}
          </div>

          {/* OC Control */}
          <div className="bg-slate-900/50 rounded-lg p-4">
            <label className="block text-sm font-medium text-slate-300 mb-2">OC Control</label>
            <div className="flex items-center gap-3">
              <button
                onClick={handleApplyOC}
                disabled={!rig.ocProfile || ocApplying}
                className="flex-1 flex items-center justify-center gap-2 px-4 py-2.5 bg-orange-600 hover:bg-orange-700 disabled:bg-slate-700 disabled:text-slate-500 rounded-lg font-medium transition-colors"
              >
                {ocApplying ? (
                  <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                ) : (
                  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
                  </svg>
                )}
                Apply OC
              </button>
              <button
                onClick={handleResetOC}
                disabled={ocApplying}
                className="flex items-center justify-center gap-2 px-4 py-2.5 bg-slate-700 hover:bg-slate-600 rounded-lg font-medium transition-colors"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
                Reset
              </button>
            </div>

            {/* OC Status */}
            <div className="mt-3 flex items-center gap-2">
              <span className={`w-2 h-2 rounded-full ${rig.ocProfile ? 'bg-orange-400' : 'bg-slate-500'}`}></span>
              <span className={`text-sm ${rig.ocProfile ? 'text-orange-400' : 'text-slate-400'}`}>
                {rig.ocProfile ? `Profile: ${rig.ocProfile.name}` : 'No OC Profile'}
              </span>
            </div>

            {!rig.ocProfile && (
              <p className="mt-3 text-xs text-yellow-400">Assign an OC profile to apply overclocking</p>
            )}
          </div>
        </div>
      </CollapsibleSection>

      {/* CPU Section */}
      {rig.cpuMiningEnabled && rig.cpu && (
        <CollapsibleSection
          title="CPU"
          subtitle={rig.cpu.model}
          icon={<svg className="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2z" /></svg>}
          iconBg="bg-green-500/20"
          defaultExpanded={true}
        >
          <div className="p-5">
            <div className="grid grid-cols-2 md:grid-cols-7 gap-4">
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Vendor</p>
                <p className="font-medium">{rig.cpu.vendor}</p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Cores</p>
                <p className="font-medium">{rig.cpu.cores}C / {rig.cpu.threads || rig.cpu.cores * 2}T</p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Temperature</p>
                <p className={`font-medium ${getTempColor(rig.cpu.temperature)}`}>
                  {rig.cpu.temperature !== null ? `${rig.cpu.temperature}°C` : 'N/A'}
                </p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Usage</p>
                <p className="font-medium">
                  {rig.cpu.usage !== null ? `${rig.cpu.usage.toFixed(1)}%` : 'N/A'}
                </p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Frequency</p>
                <p className="font-medium">
                  {rig.cpu.frequency !== null ? `${rig.cpu.frequency} MHz` : 'N/A'}
                </p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Power</p>
                <p className="font-medium text-yellow-400">
                  {rig.cpu.powerDraw !== null ? `${rig.cpu.powerDraw} W` : 'N/A'}
                </p>
              </div>
              <div className="bg-slate-900/50 rounded-lg p-3">
                <p className="text-xs text-slate-500">Hashrate</p>
                <p className="font-medium text-purple-400">
                  {rig.cpu.hashrate !== null ? `${rig.cpu.hashrate.toFixed(0)} H/s` : 'N/A'}
                </p>
              </div>
            </div>
          </div>
        </CollapsibleSection>
      )}

      {/* GPUs Section */}
      <CollapsibleSection
        title="GPUs"
        subtitle={`${rig.gpus.length} detected`}
        icon={<svg className="w-4 h-4 text-blue-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" /></svg>}
        iconBg="bg-blue-500/20"
        defaultExpanded={true}
      >
        {rig.gpus.length === 0 ? (
          <div className="p-8 text-center text-slate-400">
            <p>No GPUs detected</p>
          </div>
        ) : (
          <div className="divide-y divide-slate-700/50">
            {rig.gpus.map((gpu) => (
              <div key={gpu.id} className="p-5 hover:bg-slate-700/20 transition-colors">
                <div className="flex items-start justify-between mb-4">
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-medium text-slate-500 bg-slate-700 px-2 py-0.5 rounded">GPU {gpu.index}</span>
                      <h3 className="font-semibold">{gpu.name}</h3>
                    </div>
                    <p className="text-sm text-slate-400 mt-1">
                      {gpu.vendor} • {gpu.vram} MB VRAM • {gpu.busId || 'Unknown Bus'}
                    </p>
                  </div>
                  {gpu.hashrate !== null && (
                    <div className="text-right">
                      <p className="text-xs text-slate-500">Hashrate</p>
                      <p className="text-lg font-bold text-purple-400">{gpu.hashrate.toFixed(2)} MH/s</p>
                    </div>
                  )}
                </div>

                <div className="grid grid-cols-4 md:grid-cols-7 gap-3">
                  {/* Hashrate gets special treatment */}
                  <div className="bg-purple-500/10 rounded-lg p-2.5 border border-purple-500/20">
                    <p className="text-xs text-purple-400">Hashrate</p>
                    <p className="font-bold text-purple-400">
                      {gpu.hashrate !== null ? `${gpu.hashrate.toFixed(2)} MH/s` : 'N/A'}
                    </p>
                  </div>
                  {[
                    { label: 'Temp', value: gpu.temperature !== null ? `${gpu.temperature}°C` : 'N/A', color: getTempColor(gpu.temperature) },
                    { label: 'Fan', value: gpu.fanSpeed !== null ? `${gpu.fanSpeed}%` : 'N/A', color: '' },
                    { label: 'Power', value: gpu.powerDraw !== null ? `${gpu.powerDraw}W` : 'N/A', color: '' },
                    { label: 'Core', value: gpu.coreClock !== null ? `${gpu.coreClock} MHz` : 'N/A', color: '' },
                    { label: 'Memory', value: gpu.memoryClock !== null ? `${gpu.memoryClock} MHz` : 'N/A', color: '' },
                    { label: 'Mem Temp', value: gpu.memTemp !== null ? `${gpu.memTemp}°C` : 'N/A', color: getTempColor(gpu.memTemp) },
                  ].map((stat) => (
                    <div key={stat.label} className="bg-slate-900/50 rounded-lg p-2.5">
                      <p className="text-xs text-slate-500">{stat.label}</p>
                      <p className={`font-medium ${stat.color}`}>{stat.value}</p>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </CollapsibleSection>

      {/* Two Column Layout: Miners & Events */}
      <div className="grid md:grid-cols-2 gap-6">
        {/* Miners */}
        <CollapsibleSection
          title="Active Miners"
          subtitle={`${rig.minerInstances.length} running`}
          icon={<svg className="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>}
          iconBg="bg-green-500/20"
          defaultExpanded={true}
        >
          {rig.minerInstances.length === 0 ? (
            <div className="p-8 text-center text-slate-400">
              <p>No miners running</p>
            </div>
          ) : (
            <div className="divide-y divide-slate-700/50">
              {rig.minerInstances.map((miner) => (
                <div key={miner.id} className="p-4">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-3">
                      <h3 className="font-medium">{miner.minerName}</h3>
                      <span className={`px-2 py-0.5 rounded text-xs font-medium ${getStatusBadge(miner.status)}`}>
                        {miner.status}
                      </span>
                      {miner.pid && (
                        <span className="text-xs text-slate-500 font-mono">PID: {miner.pid}</span>
                      )}
                    </div>
                    {miner.hashrate !== null && miner.hashrate > 0 && (
                      <div className="text-right">
                        <p className="text-lg font-bold text-purple-400">{miner.hashrate.toFixed(2)} MH/s</p>
                      </div>
                    )}
                  </div>
                  <p className="text-sm text-slate-400 mb-3">
                    <span className="text-slate-500">Algo:</span> {miner.algo} | 
                    <span className="text-slate-500 ml-2">Pool:</span> {miner.pool.split('/')[0]}
                    {miner.startedAt && (
                      <span className="ml-2">| <span className="text-slate-500">Running:</span> {formatMinerUptime(miner.startedAt)}</span>
                    )}
                  </p>
                  <div className="flex items-center gap-4 text-sm">
                    <div className="flex items-center gap-2">
                      <span className="text-slate-500">Shares:</span>
                      <span className="text-green-400 font-medium">{miner.accepted}</span>
                      <span className="text-slate-600">/</span>
                      <span className="text-red-400 font-medium">{miner.rejected}</span>
                      {miner.accepted + miner.rejected > 0 && (
                        <span className="text-slate-500 text-xs">
                          ({((miner.accepted / (miner.accepted + miner.rejected)) * 100).toFixed(1)}% efficiency)
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CollapsibleSection>

        {/* Events */}
        <CollapsibleSection
          title="Recent Events"
          subtitle={`${rig.events.length} events`}
          icon={<svg className="w-4 h-4 text-orange-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>}
          iconBg="bg-orange-500/20"
          defaultExpanded={true}
        >
          {rig.events.length === 0 ? (
            <div className="p-8 text-center text-slate-400">
              <p>No events</p>
            </div>
          ) : (
            <div className="divide-y divide-slate-700/50 max-h-64 overflow-y-auto">
              {rig.events.map((event) => (
                <div key={event.id} className="p-4 flex items-start gap-3">
                  <span className={`px-2 py-0.5 rounded text-xs font-medium ${getSeverityIcon(event.severity)}`}>
                    {event.severity}
                  </span>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm truncate">{event.message}</p>
                    <p className="text-xs text-slate-500 mt-1">{formatDate(event.timestamp)}</p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CollapsibleSection>
      </div>

      {/* System Information */}
      <SystemInfoSection rigId={rig.id} basicInfo={{
        hostname: rig.hostname,
        ipAddress: rig.ipAddress,
        macAddress: rig.macAddress,
        os: rig.os,
        osVersion: rig.osVersion,
        agentVersion: rig.agentVersion,
        token: rig.token,
        createdAt: rig.createdAt,
      }} />

      {/* Alert Settings */}
      <CollapsibleSection
        title="Alert Settings"
        subtitle="Configure alert thresholds"
        icon={<svg className="w-4 h-4 text-orange-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" /></svg>}
        iconBg="bg-orange-500/20"
        defaultExpanded={false}
      >
        <div className="p-5 space-y-6">
          {/* Alert Type Toggles */}
          <div>
            <h4 className="text-sm font-medium text-slate-300 mb-3">Alert Types</h4>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <button
                onClick={() => updateAlertConfig({ tempAlertEnabled: !alertConfig.tempAlertEnabled })}
                disabled={alertConfigLoading}
                className={`flex items-center justify-between p-3 rounded-lg transition-all ${
                  alertConfig.tempAlertEnabled
                    ? 'bg-red-500/20 text-red-400 ring-1 ring-red-500/30'
                    : 'bg-slate-800 text-slate-400'
                }`}
              >
                <div className="flex items-center gap-2">
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17.657 18.657A8 8 0 016.343 7.343S7 9 9 10c0-2 .5-5 2.986-7C14 5 16.09 5.777 17.656 7.343A7.975 7.975 0 0120 13a7.975 7.975 0 01-2.343 5.657z" />
                  </svg>
                  <span className="text-sm font-medium">Temperature</span>
                </div>
                <span className={`w-2 h-2 rounded-full ${alertConfig.tempAlertEnabled ? 'bg-red-400' : 'bg-slate-500'}`}></span>
              </button>

              <button
                onClick={() => updateAlertConfig({ offlineAlertEnabled: !alertConfig.offlineAlertEnabled })}
                disabled={alertConfigLoading}
                className={`flex items-center justify-between p-3 rounded-lg transition-all ${
                  alertConfig.offlineAlertEnabled
                    ? 'bg-gray-500/20 text-gray-400 ring-1 ring-gray-500/30'
                    : 'bg-slate-800 text-slate-400'
                }`}
              >
                <div className="flex items-center gap-2">
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M18.364 5.636a9 9 0 010 12.728m0 0l-2.829-2.829m2.829 2.829L21 21M15.536 8.464a5 5 0 010 7.072m0 0l-2.829-2.829m-4.243 2.829a4.978 4.978 0 01-1.414-2.83m-1.414 5.658a9 9 0 01-2.167-9.238m7.824 2.167a1 1 0 111.414 1.414m-1.414-1.414L3 3m8.293 8.293l1.414 1.414" />
                  </svg>
                  <span className="text-sm font-medium">Offline</span>
                </div>
                <span className={`w-2 h-2 rounded-full ${alertConfig.offlineAlertEnabled ? 'bg-gray-400' : 'bg-slate-500'}`}></span>
              </button>

              <button
                onClick={() => updateAlertConfig({ hashrateAlertEnabled: !alertConfig.hashrateAlertEnabled })}
                disabled={alertConfigLoading}
                className={`flex items-center justify-between p-3 rounded-lg transition-all ${
                  alertConfig.hashrateAlertEnabled
                    ? 'bg-yellow-500/20 text-yellow-400 ring-1 ring-yellow-500/30'
                    : 'bg-slate-800 text-slate-400'
                }`}
              >
                <div className="flex items-center gap-2">
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 17h8m0 0V9m0 8l-8-8-4 4-6-6" />
                  </svg>
                  <span className="text-sm font-medium">Hashrate Drop</span>
                </div>
                <span className={`w-2 h-2 rounded-full ${alertConfig.hashrateAlertEnabled ? 'bg-yellow-400' : 'bg-slate-500'}`}></span>
              </button>
            </div>
          </div>

          {/* Threshold Settings */}
          <div>
            <h4 className="text-sm font-medium text-slate-300 mb-3">Thresholds</h4>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {/* GPU Temp Threshold */}
              <div className="bg-slate-900/50 rounded-lg p-4">
                <label className="block text-xs text-slate-500 mb-2">GPU Temperature Threshold</label>
                <div className="flex items-center gap-3">
                  <input
                    type="range"
                    min="60"
                    max="95"
                    value={alertConfig.gpuTempThreshold}
                    onChange={(e) => updateAlertConfig({ gpuTempThreshold: parseInt(e.target.value) })}
                    className="flex-1 h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer"
                  />
                  <span className="text-lg font-bold text-red-400 w-16 text-right">{alertConfig.gpuTempThreshold}°C</span>
                </div>
              </div>

              {/* CPU Temp Threshold */}
              <div className="bg-slate-900/50 rounded-lg p-4">
                <label className="block text-xs text-slate-500 mb-2">CPU Temperature Threshold</label>
                <div className="flex items-center gap-3">
                  <input
                    type="range"
                    min="60"
                    max="100"
                    value={alertConfig.cpuTempThreshold}
                    onChange={(e) => updateAlertConfig({ cpuTempThreshold: parseInt(e.target.value) })}
                    className="flex-1 h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer"
                  />
                  <span className="text-lg font-bold text-red-400 w-16 text-right">{alertConfig.cpuTempThreshold}°C</span>
                </div>
              </div>

              {/* Offline Threshold */}
              <div className="bg-slate-900/50 rounded-lg p-4">
                <label className="block text-xs text-slate-500 mb-2">Offline Threshold (seconds)</label>
                <div className="flex items-center gap-3">
                  <input
                    type="range"
                    min="60"
                    max="900"
                    step="60"
                    value={alertConfig.offlineThreshold}
                    onChange={(e) => updateAlertConfig({ offlineThreshold: parseInt(e.target.value) })}
                    className="flex-1 h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer"
                  />
                  <span className="text-lg font-bold text-gray-400 w-16 text-right">{Math.round(alertConfig.offlineThreshold / 60)}m</span>
                </div>
              </div>

              {/* Hashrate Drop Threshold */}
              <div className="bg-slate-900/50 rounded-lg p-4">
                <label className="block text-xs text-slate-500 mb-2">Hashrate Drop Threshold</label>
                <div className="flex items-center gap-3">
                  <input
                    type="range"
                    min="5"
                    max="50"
                    step="5"
                    value={alertConfig.hashrateDropPercent}
                    onChange={(e) => updateAlertConfig({ hashrateDropPercent: parseInt(e.target.value) })}
                    className="flex-1 h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer"
                  />
                  <span className="text-lg font-bold text-yellow-400 w-16 text-right">{alertConfig.hashrateDropPercent}%</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </CollapsibleSection>

      {/* Command Execution */}
      <CollapsibleSection
        title="Remote Terminal"
        subtitle="Execute commands via SSH"
        icon={<TerminalIcon />}
        iconBg="bg-purple-500/20"
        defaultExpanded={false}
      >
        <div className="p-5">
          {/* Quick Commands */}
          <div className="flex flex-wrap gap-2 mb-4">
            {['nvidia-smi', 'uptime', 'df -h', 'free -h', 'top -bn1 | head -20'].map((cmd) => (
              <button
                key={cmd}
                onClick={() => setCommand(cmd)}
                className="px-3 py-1.5 text-xs bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors font-mono"
              >
                {cmd}
              </button>
            ))}
          </div>

          <form onSubmit={handleExecuteCommand} className="flex gap-2 mb-4">
            <div className="flex-1 relative">
              <span className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500">$</span>
              <input
                type="text"
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                placeholder="Enter command..."
                className="w-full pl-8 pr-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent font-mono text-sm"
              />
            </div>
            <button
              type="submit"
              disabled={commandLoading || !command.trim()}
              className="px-5 py-2.5 bg-blox-600 hover:bg-blox-700 rounded-lg text-sm font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
            >
              {commandLoading ? (
                <>
                  <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                  Running...
                </>
              ) : (
                'Execute'
              )}
            </button>
          </form>

          {commandOutput !== null && (
            <div className="bg-slate-900 rounded-lg overflow-hidden">
              <div className="flex items-center justify-between px-4 py-2 bg-slate-800/50 border-b border-slate-700">
                <span className="text-xs text-slate-400 font-mono">Output</span>
                <button
                  onClick={() => navigator.clipboard.writeText(commandOutput)}
                  className="text-xs text-slate-400 hover:text-white transition-colors"
                >
                  Copy
                </button>
              </div>
              <pre className="p-4 text-sm text-slate-300 whitespace-pre-wrap font-mono overflow-x-auto max-h-96">
                {commandOutput || '(no output)'}
              </pre>
            </div>
          )}

          {/* Command History */}
          {commandHistory.length > 0 && (
            <div className="mt-4 pt-4 border-t border-slate-700/50">
              <p className="text-xs text-slate-500 mb-2">Recent commands:</p>
              <div className="flex flex-wrap gap-2">
                {commandHistory.map((cmd, i) => (
                  <button
                    key={i}
                    onClick={() => setCommand(cmd)}
                    className="px-2 py-1 text-xs bg-slate-700/30 hover:bg-slate-700/50 rounded transition-colors font-mono truncate max-w-xs"
                  >
                    {cmd}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </CollapsibleSection>

      {/* Terminal Modal */}
      {showTerminal && (
        <div className="fixed inset-0 bg-black/70 backdrop-blur-sm z-50 flex items-center justify-center p-4">
          <div className="w-full max-w-5xl h-[600px]">
            <Terminal 
              rigId={rig.id} 
              onClose={() => setShowTerminal(false)} 
            />
          </div>
        </div>
      )}
    </div>
  );
}
