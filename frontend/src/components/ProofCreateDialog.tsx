
import { useEffect, useMemo, useState } from 'react';
import { request } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type { DomainRecord } from '../types/domain';
import type { RunState } from '../types/status';
import { formatDate } from '../utils/format';
import { RunStateBadge } from './common/RunStateBadge';

interface ProofCreateDialogProps {
  open: boolean;
  onClose: () => void;
  onSubmitted: () => void;
}

// ProofCreateDialog ties a newly captured proof to a specific batch. The
// batch code/version are shown up-front; actual pinning happens later when a
// reviewer accepts the proof.
export function ProofCreateDialog({ open, onClose, onSubmitted }: ProofCreateDialogProps) {
  const { session } = useAuth();
  const [runs, setRuns] = useState<DomainRecord[]>([]);
  const [runId, setRunId] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open) return;
    setError('');
    setRunId(null);
    request<DomainRecord[]>('/runs?page=1&pageSize=100')
      .then((result) => setRuns(result.data))
      .catch((fetchError) => setError(fetchError instanceof Error ? fetchError.message : String(fetchError)));
  }, [open]);

  const run = useMemo(() => runs.find((item) => item.id === runId) || null, [runs, runId]);
  if (!open) return null;

  const submit = async () => {
    if (!run) {
      setError('请先选择测量所针对的印刷批次');
      return;
    }
    setSubmitting(true);
    setError('');
    const payload = {
      code: `CP-${Date.now().toString().slice(-7)}`,
      name: `校样 · ${run.code}`,
      description: `针对 ${run.code} 当前配置 v${run.version} 采集的校样`,
      facility: run.facility,
      owner: session?.username || 'operator',
      category: run.category,
      riskLevel: run.riskLevel,
      metricValue: run.metricValue,
      metricUnit: run.metricUnit,
      effectiveAt: new Date().toISOString(),
      evidence: `采集自 ${run.code} v${run.version}，等待质量复核员接收`,
      relatedCode: run.code,
      printRunId: run.id,
    };
    try {
      await request('/proofs', { method: 'POST', body: JSON.stringify(payload) });
      onSubmitted();
      onClose();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
    } finally {
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop"><section className="modal modal--wide" role="dialog" aria-modal="true">
    <h2>新增色彩校样</h2>
    <div className="proof-picker">
      <label className="field">
        <span>测量批次</span>
        <select value={runId ?? ''} onChange={(event) => setRunId(event.target.value ? Number(event.target.value) : null)}>
          <option value="" disabled>选择校样所属印刷批次</option>
          {runs.map((item) => <option key={item.id} value={item.id}>{item.code} · {item.name}（v{item.version}）</option>)}
        </select>
      </label>
      {run && <div className="run-preview">
        <header><strong>{run.code} · {run.name}</strong><RunStateBadge state={run.status as RunState} /></header>
        <dl>
          <div><dt>当前配置版本</dt><dd>v{run.version}（截至 {formatDate(run.updatedAt)}）</dd></div>
          <div><dt>当前读数</dt><dd>{run.metricValue} {run.metricUnit}</dd></div>
          <div><dt>作业区 / 责任人</dt><dd>{run.facility} · {run.owner}</dd></div>
        </dl>
        <p className="picker-hint">校样创建后不可改挂其他批次；复核员接收时会固定此刻的批次编码、版本与读数。批次配置改版后，旧校样将标记为已失效且不能用于放行。</p>
      </div>}
      {error && <div className="alert" role="alert">{error}</div>}
    </div>
    <footer>
      <button className="link-button" onClick={onClose} disabled={submitting}>取消</button>
      <button className="primary-button" onClick={() => void submit()} disabled={submitting || !run}>{submitting ? '提交中…' : '采集校样'}</button>
    </footer>
  </section></div>;
}
