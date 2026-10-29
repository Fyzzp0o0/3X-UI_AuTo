export const keys = {
  sponsors: () => ['sponsors'] as const,
  server: {
    status: () => ['server', 'status'] as const,
    fail2banStatus: () => ['server', 'fail2banStatus'] as const,
  },
  nodes: {
    root: () => ['nodes'] as const,
    list: () => ['nodes', 'list'] as const,
  },
  hosts: {
    root: () => ['hosts'] as const,
    list: () => ['hosts', 'list'] as const,
    raw: () => ['hosts', 'raw'] as const,
    byInbound: (inboundId: number) => ['hosts', 'byInbound', inboundId] as const,
    tags: () => ['hosts', 'tags'] as const,
  },
  subBalancers: {
    root: () => ['sub-balancers'] as const,
    list: () => ['sub-balancers', 'list'] as const,
  },
  settings: {
    root: () => ['settings'] as const,
    all: () => ['settings', 'all'] as const,
    defaults: () => ['settings', 'defaults'] as const,
    factoryDefaults: () => ['settings', 'factoryDefaults'] as const,
  },
  inbounds: {
    root: () => ['inbounds'] as const,
    slim: () => ['inbounds', 'slim'] as const,
    options: () => ['inbounds', 'options'] as const,
    amneziawgKernelReadiness: () => ['inbounds', 'amneziawgKernelReadiness'] as const,
  },
  clients: {
    root: () => ['clients'] as const,
    list: (params: unknown) => ['clients', 'list', params] as const,
    all: () => ['clients', 'all'] as const,
    onlines: () => ['clients', 'onlines'] as const,
    onlinesByGuid: () => ['clients', 'onlinesByGuid'] as const,
    activeInbounds: () => ['clients', 'activeInbounds'] as const,
    lastOnline: () => ['clients', 'lastOnline'] as const,
    groups: () => ['clients', 'groups'] as const,
  },
  chain: {
    root: () => ['chain'] as const,
    meta: () => ['chain', 'meta'] as const,
    pools: () => ['chain', 'pools'] as const,
    proxies: (poolId: number, page: number, size: number) =>
      ['chain', 'proxies', poolId, page, size] as const,
    nodes: (params: unknown) => ['chain', 'nodes', params] as const,
    node: (id: number) => ['chain', 'node', id] as const,
    jobs: (poolId?: number) => ['chain', 'jobs', poolId ?? 0] as const,
    job: (id: number) => ['chain', 'job', id] as const,
  },
  xray: {
    root: () => ['xray'] as const,
    config: () => ['xray', 'config'] as const,
    outboundsTraffic: () => ['xray', 'outboundsTraffic'] as const,
    geodata: {
      root: () => ['xray', 'geodata'] as const,
      files: () => ['xray', 'geodata', 'files'] as const,
      categories: (file: string, query: string) =>
        ['xray', 'geodata', 'categories', file, query] as const,
      entries: (file: string, code: string, query: string, offset: number, limit: number) =>
        ['xray', 'geodata', 'entries', file, code, query, offset, limit] as const,
    },
  },
} as const;
