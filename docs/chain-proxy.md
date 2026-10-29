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

## 5. 下一步（M1）

- `internal/web/service/chain`：池/代理/节点 CRUD、计划引擎、异步 job、探活调度；
- `internal/web/controller/chain.go` + `endpoints.ts` 注册（契约测试强制）；
- `frontend/src/pages/chain-proxy`：总览 / 代理池 / 节点 / 任务四个 Tab；
- 凭证 keyring 与导入时的 `secret.Seal`（M0 只有读写原语与拒绝策略）；
- 故障切换与黑洞兜底 job；
- 多节点（runtime）下发。

## 6. 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `XUI_CHAIN_ENABLED` | `true` | 关闭后不注入、不显示入口 |
| `NODE_TOKEN_ENCRYPTION` | 空 | 写代理凭证前必须为 `required`（复用 nodetoken keyring） |
| `XUI_CHAIN_KEY_FILE` | `/etc/x-ui/chain_key.json` | 预留：独立 keyring |
| `XUI_CHAIN_KEY` | 空 | 预留：base64 32 字节 |
