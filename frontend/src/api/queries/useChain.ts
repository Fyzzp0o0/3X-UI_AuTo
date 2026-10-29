import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import {
  ChainImportResultSchema,
  ChainJobListSchema,
  ChainJobSchema,
  ChainListSchema,
  ChainMetaSchema,
  ChainNodeSchema,
  ChainPlanSchema,
  ChainPoolListSchema,
  ChainPoolSchema,
  ChainProxySchema,
  type ChainImportResult,
  type ChainJob,
  type ChainMeta,
  type ChainNode,
  type ChainPlan,
  type ChainPool,
  type ChainProxy,
  type PoolFormValues,
} from '@/schemas/chain';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export type NodeQuery = {
  poolId?: number;
  status?: string;
  page?: number;
  size?: number;
};

async function fetchPools(): Promise<ChainPool[]> {
  const msg = await HttpUtil.get('/panel/api/chain/pools', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch pools');
  const parsed = parseMsg(msg, ChainPoolListSchema, 'chain/pools');
  return Array.isArray(parsed.obj) ? parsed.obj : [];
}

export function useChainPools() {
  const query = useQuery({
    queryKey: keys.chain.pools(),
    queryFn: fetchPools,
    refetchInterval: 30_000,
  });
  return {
    pools: query.data ?? [],
    loading: query.isFetching,
    fetched: query.data !== undefined || query.isError,
    fetchError: query.error ? (query.error as Error).message : '',
    refetch: query.refetch,
  };
}

export function useChainMeta() {
  return useQuery({
    queryKey: keys.chain.meta(),
    queryFn: async (): Promise<ChainMeta> => {
      const msg = await HttpUtil.get('/panel/api/chain/meta', undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch meta');
      const parsed = parseMsg(msg, ChainMetaSchema, 'chain/meta');
      if (!parsed.obj) throw new Error('chain meta missing');
      return parsed.obj;
    },
    staleTime: 5 * 60_000,
  });
}

export function useChainProxies(poolId: number | null, page = 1, size = 50) {
  return useQuery({
    queryKey: keys.chain.proxies(poolId ?? 0, page, size),
    enabled: poolId != null,
    queryFn: async () => {
      const msg = await HttpUtil.get(
        `/panel/api/chain/pools/${poolId}/proxies`,
        { page, size },
        { silent: true },
      );
      if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch proxies');
      const parsed = parseMsg(msg, ChainListSchema(ChainProxySchema), 'chain/pools/proxies');
      return parsed.obj ?? { items: [], total: 0 };
    },
  });
}

export function useChainNodes(params: NodeQuery) {
  return useQuery({
    queryKey: keys.chain.nodes(params),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/chain/nodes', params, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch nodes');
      const parsed = parseMsg(msg, ChainListSchema(ChainNodeSchema), 'chain/nodes');
      return parsed.obj ?? { items: [], total: 0 };
    },
  });
}

export function useChainJobs(poolId?: number) {
  return useQuery({
    queryKey: keys.chain.jobs(poolId),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/chain/jobs', poolId ? { poolId } : undefined, {
        silent: true,
      });
      if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch jobs');
      const parsed = parseMsg(msg, ChainJobListSchema, 'chain/jobs');
      return Array.isArray(parsed.obj) ? parsed.obj : [];
    },
    refetchInterval: 5_000,
  });
}

export function useChainJob(jobId: number | null) {
  return useQuery({
    queryKey: keys.chain.job(jobId ?? 0),
    enabled: jobId != null && jobId > 0,
    queryFn: async (): Promise<ChainJob> => {
      const msg = await HttpUtil.get(`/panel/api/chain/jobs/${jobId}`, undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'failed to fetch job');
      const parsed = parseMsg(msg, ChainJobSchema, 'chain/jobs/id');
      if (!parsed.obj) throw new Error('job missing');
      return parsed.obj;
    },
    refetchInterval: (query) => {
      const job = query.state.data;
      if (!job) return 1_500;
      return job.status === 'queued' || job.status === 'running' ? 1_500 : false;
    },
  });
}

export function useChainMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: keys.chain.root() });

  const createPoolMut = useMutation({
    mutationFn: async (values: PoolFormValues) => {
      const body = {
        name: values.name,
        remark: values.name,
        mode: values.mode,
        shardSize: values.shardSize,
        enabled: true,
        inbound: { protocol: 'vless', basePort: values.basePort, remark: values.name },
        client: { totalGB: values.totalGB, group: values.group ?? '' },
        chain: { frontTag: values.frontTag ?? '', autoFailover: values.autoFailover ?? false },
        probe: { url: values.probeUrl ?? '' },
      };
      const msg = await HttpUtil.post('/panel/api/chain/pools', body, JSON_HEADERS);
      if (!msg?.success) throw new Error(msg?.msg || 'failed to create pool');
      return parseMsg(msg, ChainPoolSchema, 'chain/pools/create');
    },
    onSuccess: invalidate,
  });

  const deletePoolMut = useMutation({
    mutationFn: async (poolId: number) => {
      const msg = await HttpUtil.post(`/panel/api/chain/pools/${poolId}/delete`, {}, JSON_HEADERS);
      if (!msg?.success) throw new Error(msg?.msg || 'failed to delete pool');
    },
    onSuccess: invalidate,
  });

  const importMut = useMutation({
    mutationFn: async (args: {
      poolId: number;
      content: string;
      dryRun: boolean;
      allowPrivate?: boolean;
    }): Promise<ChainImportResult> => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/pools/${args.poolId}/import`,
        { content: args.content, dryRun: args.dryRun, allowPrivate: args.allowPrivate ?? false },
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'import failed');
      const parsed = parseMsg(msg, ChainImportResultSchema, 'chain/import');
      if (!parsed.obj) throw new Error('import result missing');
      return parsed.obj;
    },
    onSuccess: invalidate,
  });

  const bulkProxiesMut = useMutation({
    mutationFn: async (args: { poolId: number; ids: number[]; action: string }) => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/pools/${args.poolId}/proxies/bulk`,
        { ids: args.ids, action: args.action },
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'bulk action failed');
    },
    onSuccess: invalidate,
  });

  const failoverMut = useMutation({
    mutationFn: async (poolId: number): Promise<ChainJob> => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/pools/${poolId}/failover`,
        {},
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'failover failed');
      const parsed = parseMsg(msg, ChainJobSchema, 'chain/failover');
      if (!parsed.obj) throw new Error('job missing');
      return parsed.obj;
    },
    onSuccess: invalidate,
  });

  const rebindMut = useMutation({
    mutationFn: async (id: number) => {
      const msg = await HttpUtil.post(`/panel/api/chain/nodes/${id}/rebind`, {}, JSON_HEADERS);
      if (!msg?.success) throw new Error(msg?.msg || 'rebind failed');
    },
    onSuccess: invalidate,
  });

  const probeMut = useMutation({
    mutationFn: async (poolId: number): Promise<ChainJob> => {
      const msg = await HttpUtil.post(`/panel/api/chain/pools/${poolId}/probe`, {}, JSON_HEADERS);
      if (!msg?.success) throw new Error(msg?.msg || 'probe failed');
      const parsed = parseMsg(msg, ChainJobSchema, 'chain/probe');
      if (!parsed.obj) throw new Error('job missing');
      return parsed.obj;
    },
    onSuccess: invalidate,
  });

  const planMut = useMutation({
    mutationFn: async (args: {
      poolId: number;
      requireHealthy?: boolean;
      maxNodes?: number;
    }): Promise<ChainPlan> => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/pools/${args.poolId}/plan`,
        { requireHealthy: args.requireHealthy ?? false, maxNodes: args.maxNodes ?? 0 },
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'plan failed');
      const parsed = parseMsg(msg, ChainPlanSchema, 'chain/plan');
      if (!parsed.obj) throw new Error('plan missing');
      return parsed.obj;
    },
  });

  const applyMut = useMutation({
    mutationFn: async (args: {
      poolId: number;
      requireHealthy?: boolean;
      maxNodes?: number;
    }): Promise<ChainJob> => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/pools/${args.poolId}/apply`,
        { requireHealthy: args.requireHealthy ?? false, maxNodes: args.maxNodes ?? 0 },
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'apply failed');
      const parsed = parseMsg(msg, ChainJobSchema, 'chain/apply');
      if (!parsed.obj) throw new Error('job missing');
      return parsed.obj;
    },
    onSuccess: invalidate,
  });

  const nodeEnableMut = useMutation({
    mutationFn: async (args: { id: number; enable: boolean }) => {
      const msg = await HttpUtil.post(
        `/panel/api/chain/nodes/${args.id}/${args.enable ? 'enable' : 'disable'}`,
        {},
        JSON_HEADERS,
      );
      if (!msg?.success) throw new Error(msg?.msg || 'node update failed');
    },
    onSuccess: invalidate,
  });

  return {
    createPool: createPoolMut.mutateAsync,
    creating: createPoolMut.isPending,
    deletePool: deletePoolMut.mutateAsync,
    importProxies: importMut.mutateAsync,
    importing: importMut.isPending,
    bulkProxies: bulkProxiesMut.mutateAsync,
    probe: probeMut.mutateAsync,
    failover: failoverMut.mutateAsync,
    rebindNode: rebindMut.mutateAsync,
    plan: planMut.mutateAsync,
    planning: planMut.isPending,
    apply: applyMut.mutateAsync,
    applying: applyMut.isPending,
    setNodeEnabled: nodeEnableMut.mutateAsync,
  };
}

export type { ChainImportResult, ChainJob, ChainNode, ChainPlan, ChainPool, ChainProxy };
