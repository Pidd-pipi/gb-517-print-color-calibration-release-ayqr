
import type { DomainRecord } from '../../types/domain';
import { StatusBadge } from './StatusBadge';

export function ProofStaleBadge({ stale }: { stale?: boolean }) {
  if (!stale) return <span className="proof-tag proof-tag--current">当前版本</span>;
  return <span className="proof-tag proof-tag--stale">已失效</span>;
}

// latestProofSnapshot returns the snapshots carried by the decision's newest
// revision, which represent the proofs currently relied upon.
function latestProofSnapshot(record: DomainRecord) {
  if (!record.revisions?.length) return undefined;
  return [...record.revisions].sort((a, b) => b.version - a.version)[0].proofSnapshots?.[0];
}

export function ColorTable({ records, title = '色彩读数与证据' }: { records: DomainRecord[]; title?: string }) {
  if (!records.length) return null;
  return <section className="color-panel" aria-label={title}><header><div><span className="eyebrow">COLOR EVIDENCE</span><h2>{title}</h2></div><small>ΔE/密度读数随版本留痕</small></header><div className="color-table"><table><thead><tr><th>样本</th><th>读数</th><th>状态</th><th>固定批次 / 版本</th><th>证据</th></tr></thead><tbody>{records.slice(0, 5).map((item) => { const snapshot = latestProofSnapshot(item); return <tr key={item.id}><td><strong>{item.code}</strong><small>{item.name}</small></td><td>{snapshot ? `${snapshot.pinnedMetricValue} ${snapshot.pinnedMetricUnit}` : `${item.metricValue} ${item.metricUnit}`}</td><td><StatusBadge status={item.status} /></td><td>{snapshot
  ? <span className="pin-cell"><strong>{snapshot.proofCode} → {snapshot.pinnedRunCode}</strong><small>校样固定于 v{snapshot.pinnedRunVersion}</small><ProofStaleBadge stale={snapshot.stale} /></span>
  : item.pinnedRunCode
    ? <span className="pin-cell"><strong>{item.pinnedRunCode}</strong><small>v{item.pinnedRunVersion} · 读数 {item.pinnedMetricValue} {item.pinnedMetricUnit}</small><ProofStaleBadge stale={item.stale} /></span>
    : <span className="muted">{item.relatedCode || '-'}<small>未接收，尚未固定</small></span>}</td><td className="evidence-cell">{snapshot?.evidence || item.evidence || '未上传'}</td></tr>; })}</tbody></table></div></section>;
}

