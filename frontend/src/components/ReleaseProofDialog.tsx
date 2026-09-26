
import { useEffect, useMemo, useState } from 'react';
import { request } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import { useEligibleProofs } from '../hooks/useEligibleProofs';
import type { DomainRecord, ProofSnapshot } from '../types/domain';
import { formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';

interface ReleaseProofDialogProps {
  open: boolean;
  decision?: DomainRecord | null;
  onClose: () => void;
  onSubmitted: () => void;
}

// ReleaseProofDialog selects the batch and, from its accepted proofs pinned at
// the current configuration version, the proofs that back the decision. Stale
// proofs are never selectable and can never be carried into release.
export function ReleaseProofDialog({ open, decision = null, onClose, onSubmitted }: ReleaseProofDialogProps) {
  const { session } = useAuth();
  const [runs, setRuns] = useState<DomainRecord[]>([]);
  const [runId, setRunId] = useState<number | null>(null);
  const [selected, setSelected] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const { proofs, loading, error: eligibleError } = useEligibleProofs(open ? runId : null);

  const editMode = Boolean(decision);
  const fixedRunId = decision?.printRunId ?? null;

  useEffect(() => {
    if (!open) return;
    setError('');
    setSelected(initialSelectedProofs(decision));
    if (decision?.printRunId) {
      setRunId(decision.printRunId);
      setRuns([]);
      return;
    }
    setRunId(null);
    request<DomainRecord[]>('/runs?page=1&pageSize=100')
      .then((result) => setRuns(result.data))
      .catch((fetchError) => setError(fetchError instanceof Error ? fetchError.message : String(fetchError)));
  }, [open, decision]);

  const selectedRun = useMemo(() => runs.find((run) => run.id === runId) || null, [runs, runId]);
  if (!open) return null;

  const toggle = (proofId: number) => {
    setSelected((current) => current.includes(proofId) ? current.filter((id) => id !== proofId) : [...current, proofId]);
  };

  const submit = async () => {
    if (!runId || selected.length === 0) {
      setError('请选择批次并至少勾选一份当前版本下已接收的校样');
      return;
    }
    setSubmitting(true);
    setError('');
    const run = selectedRun;
    const code = decision ? decision.code : `RD-${Date.now().toString().slice(-7)}`;
    const payload: Record<string, unknown> = {
      code,
      name: decision ? decision.name : `放行 ${run?.code ?? ''}`,
      description: decision ? decision.description : '由前端工作台基于当前版本校样创建',
      facility: decision?.facility || run?.facility || '默认作业区',
      owner: session?.username || 'reviewer',
      category: decision?.category || run?.category || '常规',
      riskLevel: decision?.riskLevel || run?.riskLevel || 'medium',
      metricValue: decision?.metricValue ?? run?.metricValue ?? 0,
      metricUnit: decision?.metricUnit ?? run?.metricUnit ?? 'ΔE',
      effectiveAt: new Date().toISOString(),
      evidence: decision?.evidence || `已选择 ${selected.length} 份当前版本校样`,
      relatedCode: run?.code ?? decision?.relatedCode ?? '',
      printRunId: runId,
      proofIds: selected,
    };
    try {
      if (decision) {
        payload.expectedVersion = decision.version;
        await request(`/release/${decision.id}`, { method: 'PUT', body: JSON.stringify(payload) });
      } else {
        await request('/release', { method: 'POST', body: JSON.stringify(payload) });
      }
      onSubmitted();
      onClose();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
    } finally {
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop"><section className="modal modal--wide" role="dialog" aria-modal="true">
    <h2>{editMode ? `重新选择校样 · ${decision?.code}` : '基于校样创建放行决定'}</h2>
    <div className="proof-picker">
      <label className="field">
        <span>印刷批次</span>
        {editMode
          ? <strong>{decision?.relatedCode}（决定创建后不可更换批次）</strong>
          : <select value={runId ?? ''} onChange={(event) => { setRunId(event.target.value ? Number(event.target.value) : null); setSelected([]); }}>
              <option value="" disabled>选择需要放行的批次</option>
              {runs.map((run) => <option key={run.id} value={run.id}>{run.code} · {run.name}（v{run.version}）</option>)}
            </select>}
      </label>
      {selectedRun && <p className="picker-hint">批次当前配置版本：<strong>v{selectedRun.version}</strong>，只有固定在该版本、已接收的校样可用于放行。</p>}
      {fixedRunId && <p className="picker-hint">只能重新选择同一批次当前版本下已接收的校样；旧版校样不会出现。</p>}
      {eligibleError && <div className="alert" role="alert">{eligibleError}</div>}
      {error && <div className="alert" role="alert">{error}</div>}
      <div className="proof-choice-list" aria-busy={loading}>
        {loading && <p className="muted">正在加载可放行校样…</p>}
        {!loading && runId && proofs.length === 0 && <p className="empty">该批次当前版本没有已接收的校样，请先完成校样接收。</p>}
        {proofs.map((proof) => <label key={proof.id} className={`proof-choice${selected.includes(proof.id) ? ' proof-choice--selected' : ''}`}>
          <input type="checkbox" checked={selected.includes(proof.id)} onChange={() => toggle(proof.id)} />
          <span className="proof-choice__body">
            <strong>{proof.code} · {proof.name}</strong>
            <small>固定批次 {proof.pinnedRunCode} v{proof.pinnedRunVersion} · 读数 {proof.pinnedMetricValue} {proof.pinnedMetricUnit} · {formatDate(proof.pinnedAt || proof.updatedAt)}</small>
            <span className="proof-choice__meta"><StatusBadge status={proof.status} /> <em>{proof.evidence || '无补充证据'}</em></span>
          </span>
        </label>)}
      </div>
    </div>
    <footer>
      <button className="link-button" onClick={onClose} disabled={submitting}>取消</button>
      <button className="primary-button" onClick={() => void submit()} disabled={submitting || !runId || selected.length === 0}>{submitting ? '提交中…' : editMode ? '保存校样选择' : '创建放行决定'}</button>
    </footer>
  </section></div>;
}

function initialSelectedProofs(decision: DomainRecord | null): number[] {
  if (!decision?.revisions?.length) return [];
  const latest = [...decision.revisions].sort((a, b) => b.version - a.version)[0];
  // Pre-check snapshots that still match the run's current version; stale ones
  // stay out of the selection and cannot be carried into release.
  return (latest.proofSnapshots || [])
    .filter((snapshot: ProofSnapshot) => !snapshot.stale)
    .map((snapshot: ProofSnapshot) => snapshot.proofId);
}
