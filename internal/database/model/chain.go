package model

// Chain-proxy ("ChainForge") models. One proxy backs exactly one node; the
// node's traffic is routed to that proxy by the routing rule keyed on Email.
// See docs/chain-proxy.md for the design and the fail-closed activation order.

// ChainPool is a generation strategy plus the proxy resources it owns.
type ChainPool struct {
	Id              int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name            string `json:"name" gorm:"uniqueIndex;not null"`
	Remark          string `json:"remark"`
	Mode            string `json:"mode" gorm:"default:sharded"` // shared | sharded | dedicated
	ShardSize       int    `json:"shardSize" gorm:"default:40"`
	Enabled         bool   `json:"enabled"`
	InboundTemplate string `json:"inboundTemplate" gorm:"type:text"`
	ClientTemplate  string `json:"clientTemplate" gorm:"type:text"`
	ChainStrategy   string `json:"chainStrategy" gorm:"type:text"`
	ProbePolicy     string `json:"probePolicy" gorm:"type:text"`
	TagPrefix       string `json:"tagPrefix"`
	OutboundStats   bool   `json:"outboundStats"`
	Revision        int64  `json:"revision"`
	CreatedAt       int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt       int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
}

func (ChainPool) TableName() string { return "chain_pools" }

// ChainProxy is one upstream SOCKS5/HTTP egress resource. PasswordEnc is
// AES-256-GCM ciphertext bound to this row's id; plaintext never reaches the
// database or the API.
type ChainProxy struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PoolId       int    `json:"poolId" gorm:"index:idx_chain_proxies_pool_health,priority:1"`
	Seq          int    `json:"seq"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	PasswordEnc  string `json:"-" gorm:"type:text"`
	Fingerprint  string `json:"fingerprint" gorm:"uniqueIndex:idx_chain_proxies_pool_fp,priority:2"`
	RawLine      string `json:"rawLine"`
	Label        string `json:"label"`
	GroupName    string `json:"groupName" gorm:"column:group_name"`
	Enabled      bool   `json:"enabled"`
	AllowPrivate bool   `json:"allowPrivate"`
	Health       string `json:"health" gorm:"index:idx_chain_proxies_pool_health,priority:2"`
	LatencyMs    int    `json:"latencyMs"`
	ExitIp       string `json:"exitIp" gorm:"column:exit_ip;index"`
	ExitCountry  string `json:"exitCountry" gorm:"column:exit_country"`
	FailCount    int    `json:"failCount"`
	LastProbeAt  int64  `json:"lastProbeAt"`
	LastError    string `json:"lastError"`
	CreatedAt    int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt    int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
}

func (ChainProxy) TableName() string { return "chain_proxies" }

// ChainNode is one generated sellable node: a client (Email) whose traffic is
// routed to OutboundTag. InboundId/InboundTag point at the shared shard
// inbound (mode shared/sharded) or at the node's own inbound (mode dedicated).
type ChainNode struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PoolId        int    `json:"poolId" gorm:"index:idx_chain_nodes_pool_status,priority:1"`
	ProxyId       *int   `json:"proxyId" gorm:"index"`
	Name          string `json:"name" gorm:"uniqueIndex;not null"`
	Email         string `json:"email" gorm:"uniqueIndex;not null"`
	InboundId     *int   `json:"inboundId" gorm:"index"`
	InboundTag    string `json:"inboundTag"`
	OutboundTag   string `json:"outboundTag" gorm:"uniqueIndex;not null"`
	Status        string `json:"status" gorm:"index:idx_chain_nodes_pool_status,priority:2"` // provisioning|active|degraded|failed|disabled|deleting
	ClientEnabled bool   `json:"clientEnabled"`
	BoundAt       int64  `json:"boundAt"`
	ActivatedAt   int64  `json:"activatedAt"`
	LastVerifyAt  int64  `json:"lastVerifyAt"`
	LastVerifyOk  bool   `json:"lastVerifyOk"`
	Error         string `json:"error"`
	CreatedAt     int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt     int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
}

func (ChainNode) TableName() string { return "chain_nodes" }

// ChainNodeHop is one link of a node's egress chain. Position 0 is the landing
// proxy the routing rule targets; hop k's dialerProxy points at hop k+1.
type ChainNodeHop struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId      int    `json:"nodeId" gorm:"uniqueIndex:idx_chain_hops_node_pos,priority:1"`
	Position    int    `json:"position" gorm:"uniqueIndex:idx_chain_hops_node_pos,priority:2"`
	ProxyId     *int   `json:"proxyId" gorm:"index"`
	OutboundTag string `json:"outboundTag" gorm:"not null"`
	Kind        string `json:"kind"` // landing | front
}

func (ChainNodeHop) TableName() string { return "chain_node_hops" }

// ChainJob tracks one asynchronous bulk operation (import/probe/generate/...).
type ChainJob struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PoolId         *int   `json:"poolId" gorm:"index"`
	Kind           string `json:"kind"`
	Status         string `json:"status" gorm:"index"`
	Total          int    `json:"total"`
	Done           int    `json:"done"`
	Failed         int    `json:"failed"`
	Message        string `json:"message"`
	Detail         string `json:"detail" gorm:"type:text"`
	Operator       string `json:"operator"`
	IdempotencyKey string `json:"idempotencyKey" gorm:"column:idempotency_key;index"`
	CreatedAt      int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	StartedAt      int64  `json:"startedAt"`
	FinishedAt     int64  `json:"finishedAt"`
}

func (ChainJob) TableName() string { return "chain_jobs" }

// ChainAudit records who touched a proxy credential or a generated node.
// Detail is already redacted; passwords must never be stored here.
type ChainAudit struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	At         int64  `json:"at" gorm:"index"`
	Actor      string `json:"actor"`
	Action     string `json:"action" gorm:"index:idx_chain_audit_action,priority:1"`
	ObjectType string `json:"objectType"`
	ObjectId   int    `json:"objectId"`
	Ip         string `json:"ip"`
	Ua         string `json:"ua"`
	Detail     string `json:"detail" gorm:"type:text"`
}

func (ChainAudit) TableName() string { return "chain_audit" }
