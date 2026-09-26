
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  createdAt: string;
  updatedAt: string;
  revisions?: RevisionRecord[];
  // 校样接收快照：接收时固定的批次编码、批次版本与读数
  runCode?: string;
  runVersion?: number;
  acceptedValue?: number;
  acceptedUnit?: string;
  acceptedAt?: string;
  acceptedBy?: string;
  stale?: boolean;
  // 放行决定保留的校样快照
  proofId?: number;
  proofCode?: string;
  proofVersion?: number;
  proofRunCode?: string;
  proofRunVersion?: number;
  proofValue?: number;
  proofUnit?: string;
}

export interface RevisionRecord {
  id: number; version: number; status: string; name: string; metricValue: number;
  metricUnit: string; evidence: string; actor: string; requestId: string; reason: string; createdAt: string;
  proofCode?: string; proofVersion?: number; proofRunCode?: string; proofRunVersion?: number;
  proofValue?: number; proofUnit?: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
