import { useEffect, useMemo, useState } from 'react';
import { request } from '../api/client';
import { roleAtLeast, useAuth } from '../hooks/useAuth';
import { usePagination } from '../hooks/usePagination';
import type { EntityConfig, DomainRecord, ProofSnapshot } from '../types/domain';
import type { RunState } from '../types/status';
import type { EntityStore } from '../stores/factory';
import { formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';
import { RunStateBadge } from './common/RunStateBadge';
import { ColorTable, ProofStaleBadge } from './common/ColorTable';
import { EmptyState } from './common/EmptyState';
import { MetricCard } from './common/MetricCard';
import { ConfirmDialog } from './common/ConfirmDialog';
import { UiButton } from './common/UiButton';
import { ProofCreateDialog } from './ProofCreateDialog';
import { ReleaseProofDialog } from './ReleaseProofDialog';

function decisionRunState(status: string): RunState {
  if (status === 'release') return 'released';
  if (status === 'rework' || status === 'quarantine') return 'hold';
  return 'proofing';
}

function nextPermittedStatus(config: EntityConfig, current: string, reviewer: boolean): string | null {
  const transitions: Record<string, Record<string, string | null>> = {
    pressUnit: { ready: 'setup', setup: 'printing', printing: 'maintenance', maintenance: 'printing' },
    printRun: { setup: 'printing', printing: 'proofing', proofing: reviewer ? 'released' : 'hold', hold: 'proofing', released: reviewer ? 'hold' : null },
    colorProof: { captured: 'review', review: reviewer ? 'accepted' : null, accepted: reviewer ? 'review' : null, rejected: reviewer ? 'review' : null },
    releaseDecision: { draft: reviewer ? 'release' : 'rework', release: reviewer ? 'rework' : null, rework: reviewer ? 'release' : null, quarantine: reviewer ? 'rework' : null },
  };
  return transitions[config.key]?.[current] ?? null;
}

export function EntityPage({ config, useStore }: { config: EntityConfig; useStore: EntityStore }) {
  const { session } = useAuth();
  const { items, meta, loading, error, load, createRecord, transition } = useStore();
  const [search, setSearch] = useState('');
  const [submittedSearch, setSubmittedSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [pending, setPending] = useState<{ item: DomainRecord; status: string } | null>(null);
  const [detail, setDetail] = useState<DomainRecord | null>(null);
  const [releasePicker, setReleasePicker] = useState<{ open: boolean; decision: DomainRecord | null }>({ open: false, decision: null });
  const { page, pageSize, pages, setPage, previous, next } = usePagination(meta.total);
  const canWrite = roleAtLeast(session?.role, 'operator');
  const canReview = roleAtLeast(session?.role, 'reviewer');

  useEffect(() => { void load(config.path, submittedSearch, page, pageSize); }, [config.path, load, page, pageSize, submittedSearch]);
  const highRisk = useMemo(() => items.filter((item) => ['high', 'critical'].includes(item.riskLevel)).length, [items]);
  const createDemo = async () => {
    const now = Date.now();
    await createRecord(config.path, { code: `${config.key.toUpperCase()}-${now.toString().slice(-6)}`, name: `新增${config.label}`,
      description: '通过前端工作台创建的业务记录', facility: '默认作业区', owner: session?.username || 'operator', category: '常规', riskLevel: 'medium',
      metricValue: 2.4, metricUnit: 'ΔE', effectiveAt: new Date().toISOString(), evidence: '已完成创建前色彩检查', relatedCode: 'PR-001' });
    setShowCreate(false);
  };
  const openDetail = async (item: DomainRecord) => {
    try { setDetail((await request<DomainRecord>(`/${config.path}/${item.id}`)).data); }
    catch { setDetail(item); }
  };

  const isProof = config.key === 'colorProof';
  const isRelease = config.key === 'releaseDecision';
  const staleCount = useMemo(() => items.filter((item) => item.stale).length, [items]);

  return <main className="workspace">
    <header className="page-header"><div><p className="eyebrow">业务工作台</p><h1>{config.label}</h1><p>{isProof
      ? '校样必须针对具体批次采集；接收时固定批次编码、配置版本与当时读数，配置改版后旧校样自动失效。'
      : isRelease
        ? '放行只能选择同一批次当前版本下已接收的校样；所选校样快照随决定版本永久留存。'
        : '统一管理' + config.label + '的状态、风险、证据与责任人。'}</p></div>
      {canWrite && !isProof && !isRelease && <UiButton onClick={() => setShowCreate(true)}>新增{config.label}</UiButton>}
      {canWrite && isProof && <UiButton onClick={() => setReleasePicker({ open: true, decision: null })}>新增{config.label}</UiButton>}
      {canWrite && isRelease && <UiButton onClick={() => setReleasePicker({ open: true, decision: null })}>选择校样发起放行</UiButton>}
    </header>
    <section className="metrics"><MetricCard label="记录总数" value={meta.total} detail="当前筛选范围"/><MetricCard label="高风险" value={highRisk} detail="需要优先复核"/>{isProof || isRelease
      ? <MetricCard label="已失效旧校样" value={staleCount} detail="固定版本已落后于批次当前版本"/>
      : <MetricCard label="状态种类" value={new Set(items.map((item) => item.status)).size} detail="状态机覆盖"/>}</section>
    {isProof && <ColorTable records={items} title="校样固定读数（接收时快照）" />}
    {isRelease && <ColorTable records={items} title="放行依据批次" />}
    <section className="toolbar"><input aria-label="搜索" placeholder={`搜索${config.label}编码或名称`} value={search} onChange={(event) => setSearch(event.target.value)} /><UiButton onClick={() => { setPage(1); setSubmittedSearch(search); }}>查询</UiButton><button className="link-button" onClick={() => { setSearch(''); setSubmittedSearch(''); setPage(1); }}>重置</button></section>
    {error && <div className="alert" role="alert">{error}</div>}
    <section className="table-shell" aria-busy={loading}><table><thead><tr><th>编码</th><th>名称</th><th>状态</th><th>{isProof ? '固定批次 / 版本' : '风险'}</th><th>责任人</th><th>指标</th><th>更新时间</th><th>操作</th></tr></thead><tbody>
      {items.map((item) => { const target = nextPermittedStatus(config, item.status, canReview); const staleProofAction = isProof && item.stale && target === 'review'; return <tr key={item.id} className={item.stale ? 'row--stale' : ''}><td><strong>{item.code}</strong></td><td><button className="record-link" onClick={() => void openDetail(item)}>{item.name}</button><small>{item.facility}</small></td><td>{config.key === 'printRun' ? <RunStateBadge state={item.status as RunState}/> : <StatusBadge status={item.status}/>} {isRelease && <RunStateBadge state={decisionRunState(item.status)}/>} {item.stale && <ProofStaleBadge stale />}</td><td>{isProof
        ? (item.pinnedRunCode ? <span className="pin-cell"><strong>{item.pinnedRunCode}</strong><small>v{item.pinnedRunVersion}</small></span> : <span className="muted">{item.relatedCode || '-'}<small>未接收</small></span>)
        : item.riskLevel}</td><td>{item.owner}</td><td>{item.metricValue} {item.metricUnit}</td><td>{formatDate(item.updatedAt)}</td><td>{canWrite && target && !staleProofAction ? <button className="table-action" onClick={() => setPending({ item, status: target })}>推进至 {target}</button> : <button className="table-action" onClick={() => void openDetail(item)}>查看详情</button>}{isRelease && canWrite && item.status !== 'release' && item.status !== 'quarantine' && <button className="table-action" onClick={() => { void openDetail(item); setReleasePicker({ open: true, decision: item }); }}>重选校样</button>}</td></tr>; })}
      {!items.length && !loading && <tr><td colSpan={8}><EmptyState title="没有匹配记录" detail="可清空搜索条件后重新查询" /></td></tr>}
    </tbody></table>{loading && <div className="loading">正在同步业务数据…</div>}</section>
    <footer className="pagination"><button onClick={previous} disabled={page <= 1}>上一页</button><span>第 {page} / {pages} 页</span><button onClick={next} disabled={page >= pages}>下一页</button></footer>
    <ConfirmDialog open={showCreate} title={`新增${config.label}`} onCancel={() => setShowCreate(false)} onConfirm={() => void createDemo()}><p>将创建一条包含完整责任人、风险和证据信息的演示记录。</p></ConfirmDialog>
    <ConfirmDialog open={Boolean(pending)} title="确认状态迁移" onCancel={() => setPending(null)} onConfirm={() => { if (pending) void transition(config.path, pending.item, pending.status).then(() => setPending(null)); }}><p>状态迁移会写入审计日志；色彩配置和放行决定同时生成不可变版本。</p>{pending?.item.stale && <p className="stale-warning">该校样固定的批次版本已失效，请在批次当前版本下重新采集并接收校样。</p>}<strong>{pending?.item.status} → {pending?.status}</strong></ConfirmDialog>
    <ConfirmDialog open={Boolean(detail)} title={`${detail?.code || ''} 记录详情`} onCancel={() => setDetail(null)} onConfirm={() => setDetail(null)}>{detail && <div className="detail-content"><p>{detail.description}</p><dl><div><dt>证据</dt><dd>{detail.evidence || '-'}</dd></div><div><dt>当前版本</dt><dd>v{detail.version}</dd></div>{isProof && <><div><dt>测量批次</dt><dd>{detail.relatedCode || '-'}（ID {detail.printRunId ?? '-'}）</dd></div>{detail.pinnedRunCode ? <div><dt>接收快照</dt><dd><strong>{detail.pinnedRunCode} v{detail.pinnedRunVersion}</strong><small>固定读数 {detail.pinnedMetricValue} {detail.pinnedMetricUnit} · {detail.pinnedBy || '-'} 于 {formatDate(detail.pinnedAt || '')}</small><ProofStaleBadge stale={detail.stale} /></dd></div> : <div><dt>接收快照</dt><dd className="muted">尚未接收；接收后才会固定批次版本与读数</dd></div>}</>}</dl><ColorTable records={[detail]} title={isProof ? '校样接收时读数快照' : '记录色彩读数'} />{detail.revisions?.length ? <div className="revision-list"><h3>版本链{isRelease && '（含所选校样快照）'}</h3>{detail.revisions.map((revision) => <article key={revision.id}><strong>v{revision.version} · {revision.status}</strong><span>{revision.actor} · {revision.reason}</span><code>{revision.requestId}</code>{revision.proofSnapshots?.length ? <SnapshotList snapshots={revision.proofSnapshots} /> : null}</article>)}</div> : null}</div>}</ConfirmDialog>
    {isProof && <ProofCreateDialog open={releasePicker.open && !releasePicker.decision} onClose={() => setReleasePicker({ open: false, decision: null })} onSubmitted={() => void load(config.path, submittedSearch, page, pageSize)} />}
    {isRelease && <ReleaseProofDialog open={releasePicker.open} decision={releasePicker.decision} onClose={() => setReleasePicker({ open: false, decision: null })} onSubmitted={() => { void load(config.path, submittedSearch, page, pageSize); if (detail) void openDetail(detail); }} />}
  </main>;
}

function SnapshotList({ snapshots }: { snapshots: ProofSnapshot[] }) {
  return <div className="snapshot-list"><h4>所选校样快照</h4>{snapshots.map((snapshot) => <div key={snapshot.id} className={`snapshot-row${snapshot.stale ? ' snapshot-row--stale' : ''}`}>
    <strong>{snapshot.proofCode} · {snapshot.proofName}</strong>
    <span>{snapshot.pinnedRunCode} v{snapshot.pinnedRunVersion} · 读数 {snapshot.pinnedMetricValue} {snapshot.pinnedMetricUnit} · {formatDate(snapshot.pinnedAt)}</span>
    <ProofStaleBadge stale={snapshot.stale} />
    <small>{snapshot.stale ? '该快照固定的批次版本已改版；历史记录保留原样，但不能再带去放行。' : '与批次当前配置版本一致'}</small>
  </div>)}</div>;
}
