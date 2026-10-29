# 链式代理模块（ChainForge）— M0 POC

> 基线：3X-UI v3.11.0（commit `c621ba9`）。本仓库是 fork，M0 只做"可验证的最小闭环"：
> 解析 → 生成出站/规则 → 运行时注入 → 热更新判定 → 端到端测试。
> 不在 M0 范围：HTTP API、前端页面、定时探活、故障切换、凭证密钥轮换。

## 1. 已落地

| 包 | 职责 | 测试 |
|---|---|---|
| `internal/chain/linkparse` | 12 种代理格式解析、规范化、私网拦截、指纹去重、密码脱敏 | `parse_test.go`（表驱动 + fuzz） |
| `internal/chain/xraygen` | tag/email 命名、socks 出站 JSON、`user`/`inboundTag` 路由规则、链拓扑校验 | `xraygen_test.go`（黄金 JSON） |
| `internal/chain/socksprobe` | TCP / SOCKS5 握手 / 真实 HTTP 出口三级探活 | `probe_test.go`（内置假 SOCKS5 服务器） |
| `internal/chain/secret` | 代理密码 AES-256-GCM、行绑定 AAD；无密钥时**拒绝写入** | `secret_test.go`（含跨行解密失败） |
| `internal/chain/inject` | DB 状态 → 出站/规则 → 合并进 `xray.Config`；插入位置、失败关闭 | `inject_test.go`、`hotdiff_test.go` |
| `internal/database/model/chain.go` | 6 张表 | 迁移一致性测试 |
| `internal/web/service/xray.go` | `GetXrayConfig()` 末尾调用注入器 | `chain_inject_test.go`（200 节点） |

对上游的改动共 4 个文件、+43 行：`config.go`（开关）、`db.go` + `migrate_data.go`（模型注册）、`xray.go`（注入调用）。

## 2. 验证证据

```text
ok  internal/chain/linkparse
ok  internal/chain/xraygen
ok  internal/chain/socksprobe
ok  internal/chain/secret
ok  internal/chain/inject        # 含 200 节点热更判定
ok  internal/web/service         # TestChainInjectionScaleAndHotApply
```

实测（本机 Apple Silicon）：

- 200 个节点 + 200 条规则 + 200 个出站，`GetXrayConfig` 全量生成 **3.3 ms**；
- `xray.ComputeHotDiff` 判定 `ok=true`、`AddedOutbounds=200`、`RoutingConfig!=nil` —— **Xray 不需要重启**；
- 同一批数据重复生成得到逐字节相同的 JSON（`TestBuildIsDeterministic`）。

## 3. 设计要点（与方案文档一致）

1. **不写模板**：出站/规则只在 `GetXrayConfig` 合并，模板保持干净；避免无锁写、整份覆盖和明文凭据进模板。
2. **按 email 精确分流**：`{"type":"field","inboundTag":[...],"user":["p3-n001@chain"],"outboundTag":"cf:p3:n001"}`。
3. **失败关闭**：注入过程任何错误都向上返回，`RestartXray` 会保留旧配置；`provisioning` 节点的规则指向 `blocked`。
4. **规则插入位置**：默认 `after_blocks`——排在 api 规则与黑洞规则之后、其它规则之前。
5. **确定性**：出站按节点 id、跳按 position 排序；同一状态 → 同一份 JSON，热更不会误判。
6. **一代理一客户**：`chain_proxies` 唯一指纹 + `chain_nodes.proxy_id` 由服务层保证独占。

## 4. 本地验证

```sh
# 领域层（无 DB、无网络）
go test ./internal/chain/...

# 注入 + 200 节点 + 热更判定（真实 SQLite）
go test ./internal/web/service/ -run TestChainInjectionScaleAndHotApply -v

# 全仓编译
make dist-stub && go build ./...
```

依赖下载慢时：`GOPROXY=https://goproxy.cn,direct go test ...`。

## 5. M1 后端（已落地）

| 层 | 内容 |
|---|---|
| 服务层 `internal/web/service/chain` | 池 CRUD、代理导入（DryRun 预览 / 加密落库）、分页列表、批量启停删、探活、Plan/Apply、节点启停、级联删除 |
| 异步任务 | `chain_jobs` + 后台 goroutine；同一池同时只允许 1 个 job；`GET /jobs/:id` 轮询 |
| 生成顺序（失败关闭） | 建分片入站 → 建**禁用**客户端 → 写 `provisioning` 节点（规则指向 blocked）→ reconcile #1 → 启用客户端 + 节点改 active → reconcile #2 |
| 定时探活 | `chain_probe_job`（`@every 2m`），按池并发探活，连续 3 次失败标记 unhealthy |
| API | `internal/web/controller/chain.go`，18 个端点挂在 `/panel/api/chain/*`，默认仅 `admin` |
| 文档/契约 | `frontend/src/pages/api-docs/endpoints.ts` 新增 Chain Proxy 分区；`TestRouteRegistryContract` 双向通过 |

**关键不变量**

1. **不泄漏**：客户端在出站与黑洞规则生效前始终是禁用的；启用与规则翻转发生在同一次热更里，且 hot diff 先加用户、后替换路由。
2. **不串流**：一代理一节点（`proxy_id` 唯一由服务层保证），规则同时带 `inboundTag` 与 `user`（email）。
3. **不毒化**：Plan 预检端口冲突、frontTag 是否存在、代理凭证是否可解密；任何冲突直接拒绝，不写半成品。
4. **不重启**：200 节点仍由 `ComputeHotDiff` 判定为可热更（M0 测试守护）。

**新增测试**

- `internal/web/service/chain/service_test.go`：池 CRUD、导入去重、加密拒绝/落库、端口冲突、200 行分片生成（3 节点 2 分片）、健康过滤、探活落库；
- `internal/web/chain_scope_test.go`：monitor token 访问 chain 路由必须 403；
- `TestRouteRegistryContract`：18 个新路由必须在 OpenAPI 注册表里。

## 6. M3 前端（已落地）

单个一级路由 `/chain-proxy`，页面内四个 AntD Tabs（路由是扁平清单，契约测试禁止嵌套路由）：

| Tab | 内容 | 关键文件 |
|---|---|---|
| 总览 | 池/代理/健康/节点 KPI，池健康度进度条，池卡片 | `sections/OverviewTab.tsx` |
| 代理池 | 池表格、新建（RHF 表单）、导入 Drawer、代理列表 Drawer、探活、生成计划 | `sections/PoolsTab.tsx`、`wizard/ImportDrawer.tsx`、`wizard/PlanModal.tsx` |
| 节点 | 池/状态筛选、分页、代理健康/延迟/出口 IP、启停 | `sections/NodesTab.tsx` |
| 任务 | 任务表格 + 当前任务进度（1.5s 轮询） | `sections/JobsTab.tsx` |

- 页面自渲染 `ConfigProvider` + `AppSidebar` + `Layout`，继承现有亮/暗/极暗主题；新增 `.chain-proxy-page` 样式块；
- 数据层 `api/queries/useChain.ts`：TanStack Query + `HttpUtil` + `parseMsg`（Zod 校验），变更后失效 `keys.chain`；
- 导入向导先 `dryRun` 预览（逐行状态），确认后才落库；生成前展示 Plan 的冲突与计数，确认后跳转任务页看进度；
- i18n：`menu.chainProxy` 与 `pages.chainProxy.*` 已写入 14 个 locale（zh-CN 人工翻译，其余英文兜底）；
- 质量门：`tsc --noEmit` 通过、`oxlint` 0 error（含图标 `aria-hidden`、表单控件 `aria-label`）、
  `navigation-contract` / `i18n-dead-keys` / `no-static-message` 全绿、`npm run build` 成功产出 `ChainProxyPage-*.js`。

## 7. M4 故障切换与激活自检（已落地）

| 能力 | 行为 |
|---|---|
| 激活自检 | 节点启用并 reconcile 后，用核心 `RoutingService.TestRoute`（`inboundTag + email`）核对实际命中的出站；不一致或无法验证 → 节点回 `failed`、客户端禁用、规则回黑洞，job 标记 `partial` |
| 自动切换 | 池开启 `autoFailover` 后，每次探活结束跑一次 Failover：代理 `unhealthy/disabled` 的节点换绑到同池健康且未占用的备用代理；**出站 tag 不变**，无需改路由 |
| 无备用兜底 | 没有可用备用时节点置 `failed` 并禁用客户端；注入器对 `failed/provisioning/disabled` 一律生成**黑洞规则**（绝不删除规则，避免回落直连） |
| 手动操作 | `POST /pools/:id/failover`（job）、`POST /nodes/:id/rebind`（同步换绑 + reconcile） |
| 可观测 | 节点错误用 Tooltip 展示；job 有 `partial` 状态；失败明细写入 job detail |

新增测试：自检失败回滚（3 节点全部回退、reconcile 3 次）、换绑保持 tag、无备用时 gate、关闭自动切换时 skip、手动换绑。前端新增"自动切换"开关与节点"换绑"按钮。

## 8. M5 入站模板扩展（已落地）

| 能力 | 说明 |
|---|---|
| 安全 | `none` / `tls` / `reality`；TLS 校验证书与私钥文件存在且可读；REALITY 校验 `dest`、`serverNames`、`shortIds` |
| 传输 | `tcp` / `ws` / `grpc` / `httpupgrade` / `xhttp`；`path`（grpc 为 serviceName）与 Host 头可配 |
| REALITY 密钥 | 留空时用 `crypto/ecdh` 现生成 X25519 密钥对（base64 raw-url），并写入 `realitySettings.settings.publicKey` 供订阅链接使用；shortId 自动生成 4 组 |
| 端口分配 | `basePort`..`portEnd` 区间自动找空闲端口；`basePort` 被占用时**自动顺延**而不是失败；区间耗尽报错 |
| 一致性 | 生成的 streamSettings 同时供 Xray 配置与订阅/分享链接使用；Plan 会展示实际分配的端口 |
| 前端 | 池表单新增安全/传输/端口段/TLS/REALITY 字段，按选择条件展开 |
| 节点详情 | 抽屉展示状态/入站/出站/落地/前置链路；复用 `GetAllClientLinks` 输出分享链接（可复制 + 二维码） |

新增测试：5 种传输的 stream JSON、REALITY 自动密钥与 shortId、TLS 文件校验、非法组合拒绝、端口跳过/顺延/耗尽、REALITY 池端到端生成（入站端口顺延且 stream 含 privateKey）。

## 9. M6 端到端、轮换与批量（已落地）

| 能力 | 说明 |
|---|---|
| 真实 Xray e2e | `XRAY_E2E_BINARY=/path/to/xray go test ./internal/chain/inject -run TestChainEndToEnd`：起真实核心 + 两个 VLESS 客户端，活跃节点必须命中 marker SOCKS5（`proxy-A`），被闸门节点不得拿到任何上游响应 |
| 凭据轮换 | `x-ui encrypt-tokens` 现在同时重加密 node token 与链式代理密码（同一 keyring）；旧 key 删除后 `secret.Open` 仍可解，测试覆盖"删旧 key 后仍能解密" |
| 批量动作 | `POST /nodes/bulk`：enable/disable/delete/rebind，最多 500 个，**整批只 reconcile 一次** |
| 链接导出 | `POST /nodes/export`：按选中节点或整池导出，去重后返回链接；前端生成 `chain-links.txt` 下载 |
| 前端 | 节点表支持多选与批量工具条（启用/停用/换绑/导出/删除） |

**多节点下发（未做，明确延后）**：3X-UI 的 node-sync 契约目前只同步 inbound/client，出站与路由规则无法安全下发；要做需要扩展上游 API 与契约测试，属于独立里程碑（M7），不在当前范围内。

## 10. 下一步（M7）

- `internal/web/service/chain`：池/代理/节点 CRUD、计划引擎、异步 job、探活调度；
- `internal/web/controller/chain.go` + `endpoints.ts` 注册（契约测试强制）；
- `frontend/src/pages/chain-proxy`：总览 / 代理池 / 节点 / 任务四个 Tab；
- 凭证 keyring 与导入时的 `secret.Seal`（M0 只有读写原语与拒绝策略）；
- 故障切换与黑洞兜底 job；
- 多节点（runtime）下发。

## 11. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `XUI_CHAIN_ENABLED` | `true` | 关闭后不注入、不显示入口 |
| `NODE_TOKEN_ENCRYPTION` | 空 | 写代理凭证前必须为 `required`（复用 nodetoken keyring） |
| `XUI_CHAIN_KEY_FILE` | `/etc/x-ui/chain_key.json` | 预留：独立 keyring |
| `XUI_CHAIN_KEY` | 空 | 预留：base64 32 字节 |
