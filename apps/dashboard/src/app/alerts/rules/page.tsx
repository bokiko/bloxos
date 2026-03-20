'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { getApiUrl, getCsrfToken } from '../../../lib/api';
import { formatTimeAgo } from '../../../lib/format';

type AlertCondition =
  | 'TEMP_ABOVE'
  | 'TEMP_BELOW'
  | 'HASHRATE_BELOW'
  | 'HASHRATE_DROP_PERCENT'
  | 'RIG_OFFLINE'
  | 'GPU_ERROR'
  | 'REJECTED_PERCENT';

interface AlertRule {
  id: string;
  name: string;
  enabled: boolean;
  condition: AlertCondition;
  threshold: number;
  duration: number | null;
  cooldown: number;
  notify: string[];
  lastFired: string | null;
  createdAt: string;
  updatedAt: string;
}

const CONDITIONS: { value: AlertCondition; label: string; unit: string }[] = [
  { value: 'TEMP_ABOVE', label: 'Temperature Above', unit: '°C' },
  { value: 'TEMP_BELOW', label: 'Temperature Below', unit: '°C' },
  { value: 'HASHRATE_BELOW', label: 'Hashrate Below', unit: 'MH/s' },
  { value: 'HASHRATE_DROP_PERCENT', label: 'Hashrate Drop', unit: '%' },
  { value: 'RIG_OFFLINE', label: 'Rig Offline', unit: 'min' },
  { value: 'GPU_ERROR', label: 'GPU Error', unit: 'count' },
  { value: 'REJECTED_PERCENT', label: 'Rejected Shares', unit: '%' },
];

const NOTIFY_CHANNELS = ['telegram', 'discord', 'email'] as const;

function getConditionLabel(condition: AlertCondition): string {
  return CONDITIONS.find((c) => c.value === condition)?.label ?? condition;
}

function getConditionUnit(condition: AlertCondition): string {
  return CONDITIONS.find((c) => c.value === condition)?.unit ?? '';
}

const defaultForm = {
  name: '',
  condition: 'TEMP_ABOVE' as AlertCondition,
  threshold: 80,
  duration: '',
  cooldown: 300,
  notify: ['telegram'] as string[],
};

export default function AlertRulesPage() {
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);
  const [formData, setFormData] = useState(defaultForm);
  const [saving, setSaving] = useState(false);

  const fetchRules = useCallback(async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/alert-rules`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setRules(data);
      }
    } catch (err) {
      console.error('Failed to fetch alert rules');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  function openCreateModal() {
    setEditingRule(null);
    setFormData(defaultForm);
    setShowModal(true);
  }

  function openEditModal(rule: AlertRule) {
    setEditingRule(rule);
    setFormData({
      name: rule.name,
      condition: rule.condition,
      threshold: rule.threshold,
      duration: rule.duration != null ? String(rule.duration) : '',
      cooldown: rule.cooldown,
      notify: [...rule.notify],
    });
    setShowModal(true);
  }

  function closeModal() {
    setShowModal(false);
    setEditingRule(null);
    setFormData(defaultForm);
  }

  async function handleSave() {
    if (!formData.name.trim() || formData.notify.length === 0) return;
    setSaving(true);

    const body = {
      name: formData.name,
      condition: formData.condition,
      threshold: Number(formData.threshold),
      duration: formData.duration ? Number(formData.duration) : null,
      cooldown: Number(formData.cooldown),
      notify: formData.notify,
    };

    try {
      const url = editingRule
        ? `${getApiUrl()}/api/alert-rules/${editingRule.id}`
        : `${getApiUrl()}/api/alert-rules`;

      const res = await fetch(url, {
        method: editingRule ? 'PUT' : 'POST',
        headers: {
          'Content-Type': 'application/json',
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
        body: JSON.stringify(body),
      });

      if (res.ok) {
        await fetchRules();
        closeModal();
      }
    } catch (err) {
      console.error('Failed to save alert rule');
    } finally {
      setSaving(false);
    }
  }

  async function handleToggle(rule: AlertRule) {
    try {
      const res = await fetch(`${getApiUrl()}/api/alert-rules/${rule.id}/toggle`, {
        method: 'PATCH',
        headers: { 'x-csrf-token': getCsrfToken() },
        credentials: 'include',
      });
      if (res.ok) {
        await fetchRules();
      }
    } catch (err) {
      console.error('Failed to toggle alert rule');
    }
  }

  async function handleDelete(rule: AlertRule) {
    if (!confirm(`Delete alert rule "${rule.name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/alert-rules/${rule.id}`, {
        method: 'DELETE',
        headers: { 'x-csrf-token': getCsrfToken() },
        credentials: 'include',
      });
      if (res.ok || res.status === 204) {
        await fetchRules();
      }
    } catch (err) {
      console.error('Failed to delete alert rule');
    }
  }

  function toggleNotifyChannel(channel: string) {
    setFormData((prev) => ({
      ...prev,
      notify: prev.notify.includes(channel)
        ? prev.notify.filter((c) => c !== channel)
        : [...prev.notify, channel],
    }));
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
        <p className="text-slate-400 ml-3">Loading alert rules...</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-xl bg-orange-500/20 flex items-center justify-center">
            <svg className="w-6 h-6 text-orange-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </div>
          <div>
            <h1 className="text-2xl font-bold">Alert Rules</h1>
            <p className="text-slate-400">{rules.length} rule{rules.length !== 1 ? 's' : ''} configured</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {/* Navigation tabs */}
          <div className="flex items-center bg-slate-800 rounded-lg p-1">
            <Link
              href="/alerts"
              className="px-4 py-1.5 rounded-md text-sm font-medium text-slate-400 hover:text-white transition-colors"
            >
              Active Alerts
            </Link>
            <div className="px-4 py-1.5 rounded-md text-sm font-medium bg-blox-600 text-white">
              Alert Rules
            </div>
          </div>
          <button
            onClick={openCreateModal}
            className="px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
          >
            + New Rule
          </button>
        </div>
      </div>

      {/* Rules Table */}
      {rules.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4" />
            </svg>
          </div>
          <p className="text-slate-400 mb-4">No alert rules yet. Create one to get started.</p>
          <button
            onClick={openCreateModal}
            className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
          >
            Create Rule
          </button>
        </div>
      ) : (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-slate-700/50">
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Status</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Name</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Condition</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Threshold</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Cooldown</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Notify</th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Last Fired</th>
                  <th className="px-6 py-3 text-right text-xs font-medium text-slate-400 uppercase tracking-wider">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-700/30">
                {rules.map((rule) => (
                  <tr key={rule.id} className="hover:bg-slate-700/20 transition-colors">
                    {/* Toggle */}
                    <td className="px-6 py-4">
                      <button
                        onClick={() => handleToggle(rule)}
                        className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                          rule.enabled ? 'bg-green-500' : 'bg-slate-600'
                        }`}
                      >
                        <span
                          className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                            rule.enabled ? 'translate-x-6' : 'translate-x-1'
                          }`}
                        />
                      </button>
                    </td>
                    {/* Name */}
                    <td className="px-6 py-4">
                      <span className={`font-medium ${rule.enabled ? 'text-white' : 'text-slate-500'}`}>
                        {rule.name}
                      </span>
                    </td>
                    {/* Condition */}
                    <td className="px-6 py-4">
                      <span className="text-sm text-slate-300">{getConditionLabel(rule.condition)}</span>
                    </td>
                    {/* Threshold */}
                    <td className="px-6 py-4">
                      <span className="text-sm font-mono text-slate-300">
                        {rule.threshold} {getConditionUnit(rule.condition)}
                      </span>
                    </td>
                    {/* Cooldown */}
                    <td className="px-6 py-4">
                      <span className="text-sm text-slate-400">{Math.floor(rule.cooldown / 60)}m</span>
                    </td>
                    {/* Notify */}
                    <td className="px-6 py-4">
                      <div className="flex gap-1.5">
                        {rule.notify.map((ch) => (
                          <span
                            key={ch}
                            className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300 capitalize"
                          >
                            {ch}
                          </span>
                        ))}
                      </div>
                    </td>
                    {/* Last Fired */}
                    <td className="px-6 py-4">
                      <span className="text-sm text-slate-500">
                        {rule.lastFired ? formatTimeAgo(rule.lastFired) : 'Never'}
                      </span>
                    </td>
                    {/* Actions */}
                    <td className="px-6 py-4 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => openEditModal(rule)}
                          className="p-2 rounded-lg text-slate-500 hover:text-blox-400 hover:bg-blox-500/10 transition-colors"
                        >
                          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                          </svg>
                        </button>
                        <button
                          onClick={() => handleDelete(rule)}
                          className="p-2 rounded-lg text-slate-500 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                        >
                          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                          </svg>
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-[480px] max-h-[90vh] overflow-y-auto">
            <h3 className="text-lg font-semibold mb-4">
              {editingRule ? 'Edit Alert Rule' : 'Create Alert Rule'}
            </h3>

            <div className="space-y-4">
              {/* Name */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Name</label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  placeholder="e.g., High GPU Temperature"
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>

              {/* Condition */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Condition</label>
                <select
                  value={formData.condition}
                  onChange={(e) => setFormData({ ...formData, condition: e.target.value as AlertCondition })}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                >
                  {CONDITIONS.map((c) => (
                    <option key={c.value} value={c.value}>
                      {c.label}
                    </option>
                  ))}
                </select>
              </div>

              {/* Threshold */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">
                  Threshold ({getConditionUnit(formData.condition)})
                </label>
                <input
                  type="number"
                  value={formData.threshold}
                  onChange={(e) => setFormData({ ...formData, threshold: Number(e.target.value) })}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>

              {/* Duration */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">
                  Duration (seconds, optional)
                </label>
                <input
                  type="number"
                  value={formData.duration}
                  onChange={(e) => setFormData({ ...formData, duration: e.target.value })}
                  placeholder="Seconds before triggering"
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>

              {/* Cooldown */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">
                  Cooldown (seconds)
                </label>
                <input
                  type="number"
                  value={formData.cooldown}
                  onChange={(e) => setFormData({ ...formData, cooldown: Number(e.target.value) })}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>

              {/* Notify Channels */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Notification Channels</label>
                <div className="flex gap-3">
                  {NOTIFY_CHANNELS.map((channel) => (
                    <label key={channel} className="flex items-center gap-2 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={formData.notify.includes(channel)}
                        onChange={() => toggleNotifyChannel(channel)}
                        className="w-4 h-4 rounded border-slate-600 bg-slate-900 text-blox-500 focus:ring-blox-500 focus:ring-offset-0"
                      />
                      <span className="text-sm text-slate-300 capitalize">{channel}</span>
                    </label>
                  ))}
                </div>
              </div>
            </div>

            {/* Actions */}
            <div className="flex gap-3 mt-6">
              <button
                onClick={closeModal}
                className="flex-1 px-4 py-2.5 bg-slate-700 hover:bg-slate-600 rounded-lg font-medium transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleSave}
                disabled={saving || !formData.name.trim() || formData.notify.length === 0}
                className="flex-1 px-4 py-2.5 bg-blox-600 hover:bg-blox-700 disabled:opacity-50 rounded-lg font-medium transition-colors"
              >
                {saving ? 'Saving...' : editingRule ? 'Update' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
