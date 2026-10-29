import { z } from 'zod';

export const ChainPoolSchema = z.object({
  id: z.number(),
  name: z.string(),
  remark: z.string().default(''),
  mode: z.string(),
  shardSize: z.number(),
  enabled: z.boolean(),
  inboundTemplate: z.string(),
  clientTemplate: z.string(),
  chainStrategy: z.string(),
  probePolicy: z.string(),
  tagPrefix: z.string(),
  outboundStats: z.boolean(),
  revision: z.number(),
  createdAt: z.number(),
  updatedAt: z.number(),
  proxies: z.number().optional(),
  nodes: z.number().optional(),
  healthy: z.number().optional(),
});
export type ChainPool = z.infer<typeof ChainPoolSchema>;

export const ChainProxySchema = z.object({
  id: z.number(),
  poolId: z.number(),
  seq: z.number(),
  protocol: z.string(),
  host: z.string(),
  port: z.number(),
  username: z.string().default(''),
  fingerprint: z.string(),
  label: z.string().default(''),
  enabled: z.boolean(),
  allowPrivate: z.boolean(),
  health: z.string(),
  latencyMs: z.number(),
  exitIp: z.string().default(''),
  exitCountry: z.string().default(''),
  failCount: z.number(),
  lastProbeAt: z.number(),
  lastError: z.string().default(''),
});
export type ChainProxy = z.infer<typeof ChainProxySchema>;

export const ChainNodeSchema = z.object({
  id: z.number(),
  poolId: z.number(),
  proxyId: z.number().nullable().optional(),
  name: z.string(),
  email: z.string(),
  inboundId: z.number().nullable().optional(),
  inboundTag: z.string(),
  outboundTag: z.string(),
  status: z.string(),
  clientEnabled: z.boolean(),
  error: z.string().default(''),
  proxyHost: z.string().optional(),
  proxyPort: z.number().optional(),
  health: z.string().optional(),
  latencyMs: z.number().optional(),
  exitIp: z.string().optional(),
});
export type ChainNode = z.infer<typeof ChainNodeSchema>;

export const ChainJobSchema = z.object({
  id: z.number(),
  poolId: z.number().nullable().optional(),
  kind: z.string(),
  status: z.string(),
  total: z.number(),
  done: z.number(),
  failed: z.number(),
  message: z.string().default(''),
  detail: z.string().default(''),
  operator: z.string().default(''),
  createdAt: z.number(),
  startedAt: z.number(),
  finishedAt: z.number(),
});
export type ChainJob = z.infer<typeof ChainJobSchema>;

export const ChainPlanSchema = z.object({
  poolId: z.number(),
  mode: z.string(),
  shardSize: z.number(),
  proxies: z.number(),
  eligible: z.number(),
  existing: z.number(),
  newNodes: z.number(),
  newOutbounds: z.number(),
  newRules: z.number(),
  shards: z.number(),
  inboundTags: z.array(z.string()),
  conflicts: z.array(z.string()).optional(),
  warnings: z.array(z.string()).optional(),
});
export type ChainPlan = z.infer<typeof ChainPlanSchema>;

export const ChainImportLineSchema = z.object({
  lineNo: z.number(),
  line: z.string(),
  error: z.string().optional(),
  proxy: z.string().optional(),
});

export const ChainImportResultSchema = z.object({
  total: z.number(),
  valid: z.number(),
  duplicate: z.number(),
  created: z.number(),
  dryRun: z.boolean(),
  preview: z.array(ChainImportLineSchema).default([]),
  invalid: z.array(ChainImportLineSchema).optional(),
});
export type ChainImportResult = z.infer<typeof ChainImportResultSchema>;

export const ChainListSchema = <T extends z.ZodTypeAny>(item: T) =>
  z.object({ items: z.array(item), total: z.number() });

export const ChainPoolListSchema = z.array(ChainPoolSchema);
export const ChainJobListSchema = z.array(ChainJobSchema);

export const ChainMetaSchema = z.object({
  enabled: z.boolean(),
  encryption: z.boolean(),
  maxImport: z.number(),
  modes: z.array(z.string()),
  protocols: z.array(z.string()),
  defaultPort: z.number(),
});
export type ChainMeta = z.infer<typeof ChainMetaSchema>;

export const PoolFormSchema = z.object({
  name: z
    .string()
    .min(1, 'pages.chainProxy.validation.required')
    .max(64, 'pages.chainProxy.validation.required'),
  basePort: z.number().int().min(1).max(65535),
  mode: z.enum(['shared', 'sharded', 'dedicated']),
  shardSize: z.number().int().min(1).max(500),
  frontTag: z.string().max(64).optional(),
  totalGB: z.number().int().min(0),
  group: z.string().max(64).optional(),
  probeUrl: z.string().max(512).optional(),
  autoFailover: z.boolean().optional(),
  portEnd: z.number().int().min(0).max(65535).optional(),
  security: z.enum(['none', 'tls', 'reality']).optional(),
  network: z.enum(['tcp', 'ws', 'grpc', 'httpupgrade', 'xhttp']).optional(),
  path: z.string().max(128).optional(),
  transportHost: z.string().max(255).optional(),
  tlsCert: z.string().max(512).optional(),
  tlsKey: z.string().max(512).optional(),
  tlsServerName: z.string().max(255).optional(),
  realityDest: z.string().max(255).optional(),
  realityServerNames: z.string().max(512).optional(),
  realityShortIds: z.string().max(256).optional(),
  realityPrivateKey: z.string().max(128).optional(),
  realityFingerprint: z.string().max(32).optional(),
});
export type PoolFormValues = z.infer<typeof PoolFormSchema>;
