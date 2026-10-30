# 3X-UI AuTo · ChainForge

[![Release](https://img.shields.io/github/v/release/Fyzzp0o0/3X-UI_AuTo?label=release)](https://github.com/Fyzzp0o0/3X-UI_AuTo/releases)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](LICENSE)

> 基于 [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) `v3.11.0` 的 fork，新增**链式代理工厂**模块：
> 把成百上千条 SOCKS5 批量导入，一键生成“已配置链式代理”的可售节点。
> 上游代码与本文修改均遵循 **GPL-3.0**（见 [LICENSE](LICENSE)）。

[功能](#功能特性) · [快速开始](#快速开始) · [使用流程](#使用流程) · [安全模型](#安全模型) · [API](#api) · [开发](#开发与测试) · [已知限制](#已知限制与路线图) · [English overview](#english-overview)

---

## 这是什么

3X-UI 本身已经具备出站、路由、订阅、流量统计和热更新能力，但把 200 条 SOCKS5 变成 200 个节点需要逐条手工配置：建出站 → 建入站/客户端 → 写路由规则 → 测活 → 发链接。

ChainForge 补上的是**批发层**：

```
代理池（200 条 SOCKS5）
   → 解析 / 去重 / 探活
   → 一键生成：分片入站 + 200 客户端 + 200 socks 出站 + 200 条 user 路由
   → 激活自检（真实核心路由校验）
   → 节点矩阵：链接、二维码、流量、换绑、批量操作
```

它不修改 Xray 内核，不接管计费，也不替代 3X-UI 的订阅/客户端体系；生成的节点依然由 3X-UI 的客户端、流量与订阅系统管理。

## 功能特性

| 能力 | 说明 |
|---|---|
| **批量导入** | 12 种格式：`socks5://user:pass@host:port`、`host:port:user:pass`、`user:pass@host:port`、Clash 单行、base64、IPv6 等；逐行报错，不静默丢弃 |
| **去重与规范化** | `sha256(proto|host|port|user)` 指纹；改密码不会生成新行；默认拒绝私网/回环（可显式放行） |
| **入口模板** | VLESS；安全 `none/tls/reality`；传输 `tcp/ws/grpc/httpupgrade/xhttp`；REALITY 密钥留空自动生成 |
| **端口分配** | `basePort..portEnd` 自动跳过占用端口，冲突时顺延而不是失败 |
| **三级探活** | TCP → SOCKS5 握手（RFC1928/1929）→ 真实 HTTP 出口与出口 IP；连续失败自动标记 unhealthy |
| **按客户分流** | 每条代理一个出站，规则同时匹配 `inboundTag + user(email)`，客户之间不会串流 |
| **分片共享** | 默认每 40 个客户端一个入站，避免单入口故障放大；可调 |
| **激活自检** | 用核心 `RoutingService.TestRoute` 核对实际出站，不一致立即回滚到黑洞 |
| **失败关闭** | 代理挂了规则指向 `blocked`，绝不删除规则、绝不回落直连 |
| **自动/手动切换** | 失效代理换绑同池健康备用；无备用则 gate；出站 tag 不变 |
| **批量与导出** | 节点批量启停/删除/换绑；按选中或整池导出链接（TXT），可复制 + 二维码 |
| **凭证安全** | 代理密码 AES-256-GCM 行绑定加密；无密钥拒绝导入；`x-ui encrypt-tokens` 轮换 |
| **任务化** | 导入/探活/生成/切换都是异步 job，带进度与 partial 状态 |
| **多语言** | 14 种界面语言；新增模块文案已全部同步 |

## 界面

一级菜单 **链式代理**，四个工作区：

- **总览**：代理池、代理数、健康数、节点数、池健康度
- **代理池**：新建/编辑、导入向导、代理列表（批量启停删）、探活、故障切换、生成计划
- **节点**：状态/池筛选、分页、流量、出口 IP/延迟、启停、换绑、删除、批量操作、详情抽屉（链路 + 链接 + 二维码）
- **任务**：进度、partial/failed 原因、历史

## 快速开始

### 方式 A：一键安装（Linux + systemd）

当前最新发布：[v3.11.0-chain.2](https://github.com/Fyzzp0o0/3X-UI_AuTo/releases/tag/v3.11.0-chain.2)（`linux-amd64` / `linux-arm64`，修复核心二进制命名）。

```bash
curl -fsSL https://raw.githubusercontent.com/Fyzzp0o0/3X-UI_AuTo/main/install-chain.sh -o install-chain.sh
sudo bash install-chain.sh
```

脚本会：

- 按 CPU 架构下载 `x-ui-linux-{amd64,arm64}.tar.gz` 并校验 SHA-256
- 安装到 `/usr/local/x-ui`，写入 systemd 服务与 `x-ui` 管理命令
- 生成 `/etc/default/x-ui`（0600）：数据库路径、日志路径、`NODE_TOKEN_ENCRYPTION=required`、32 字节加密密钥
- 首次安装生成随机管理员密码并打印一次，启动后输出面板地址

常用参数：

```bash
sudo bash install-chain.sh --version v3.11.0-chain.1   # 指定版本
sudo bash install-chain.sh --port 8443 --username ops  # 自定义端口/账号
sudo bash install-chain.sh --local x-ui-linux-arm64.tar.gz  # 离线安装
sudo bash install-chain.sh --no-start                  # 只装不启动
sudo bash install-chain.sh --uninstall                 # 卸载（保留 /etc/x-ui）
sudo bash install-chain.sh --uninstall --purge         # 连数据库与密钥一起删除
```

> 当前仓库尚未发布 Release 时，请用方式 B 从源码构建，或在 Actions 里手动运行 **Chain Release** 工作流后使用方式 A。

### 方式 B：从源码构建（当前最可靠）

依赖：Go 1.27+、Node 26+、C 编译器（SQLite 需要 CGO）、git。

```bash
git clone https://github.com/Fyzzp0o0/3X-UI_AuTo.git
cd 3X-UI_AuTo
make dist-stub
cd frontend && npm ci && npm run build && cd ..
go build -o x-ui .
sudo install -m755 x-ui /usr/local/x-ui/
sudo mkdir -p /etc/x-ui
# 最小启动（生产务必启用凭证加密，见“安全模型”）
XUI_DB_FOLDER=/etc/x-ui ./x-ui
# 打开 http://<ip>:2053/panel
```

### 方式 C：Docker

仓库保留上游 Dockerfile，可直接构建：

```bash
docker build -t 3x-ui-auto .
docker run -d --name 3x-ui-auto \
  -p 2053:2053 \
  -v x-ui-db:/etc/x-ui \
  -e XUI_CHAIN_ENABLED=true \
  -e NODE_TOKEN_ENCRYPTION=required \
  -e XUI_NODE_TOKEN_KEY="$(openssl rand -base64 32)" \
  3x-ui-auto
```

> 密钥务必持久化保存；丢失后已加密的代理密码不可恢复，只能重新导入。

## 使用流程

1. **新建代理池**：起始端口、分片大小、安全/传输（先用 `none + tcp` 验证链路）、可选前置出站 tag、自动切换开关。
2. **导入代理**：粘贴 200 行 → **预览**（逐行状态：有效/重复/无效）→ 确认导入。
3. **探活**：点“探活”，任务页看进度；健康代理才参与生成（默认 `requireHealthy`）。
4. **生成**：点“生成”查看 Plan（新增节点/分片入站/规则/冲突）→ 确认 → 任务页等待 `succeeded`。
5. **交付**：节点页点节点名 → 详情抽屉复制链接/二维码；也可选中节点或整池导出 TXT。

### 链式拓扑

```
客户 → 面板入站（VLESS/TLS/REALITY）
        └─ 落地出站（socks5，来自代理池）
              └─ dialerProxy → 前置出站（可选，模板里已有 tag）
                    └─ 目标网站
```

- **一代理一客户**：默认独占，不会两条客户共用一条出口。
- **共享入口**：同池客户共用一个（或分片后的多个）入站，入口被封时的爆炸半径是分片大小。
- `dialerProxy` 是**传输链**（用它拨号到下一跳），不是协议级串联；这是 Xray 的语义边界。

## 安全模型

| 控制 | 做法 |
|---|---|
| 凭证加密 | 代理密码 AES-256-GCM，AAD 绑定 `chain/proxy/<id>/password`；密钥来自 `XUI_NODE_TOKEN_KEY` 或 `/etc/x-ui/node_token_key.json` |
| 无密钥拒绝导入 | `NODE_TOKEN_ENCRYPTION` 未开启时，带密码的导入直接失败，绝不降级明文 |
| 密钥轮换 | `x-ui encrypt-tokens` 同时重加密 node token 与代理密码；测试覆盖“删除旧 key 后仍可解密” |
| 防直连泄漏 | 非活跃节点规则指向 `blocked`；注入失败即拒绝生成配置（核心保留旧配置） |
| 激活顺序 | 禁用客户端 → 黑洞规则生效 → 启用客户端并翻规则（热更先加用户后换路由） |
| 防串流 | 规则 `inboundTag + user` 双条件；自动化测试覆盖客户隔离 |
| 权限 | `/panel/api/chain/*` 默认仅 `admin` scope；CSRF、请求体上限、SSRF 拨号守卫复用面板能力 |
| 审计 | 敏感操作写入 `chain_audit`（`GET /panel/api/chain/audit`） |
| 限流 | 导入/探活/生成/导出有独立速率限制与并发闸门 |

> REALITY 私钥按上游惯例存放在池模板 JSON 中（与 3X-UI 把入站私钥存 `inbounds.stream_settings` 一致）；数据库备份需按敏感数据保管。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `XUI_CHAIN_ENABLED` | `true` | 关闭后不注入、不显示入口 |
| `NODE_TOKEN_ENCRYPTION` | 空 | 生产必须 `required`；开启前导入带密码代理会失败 |
| `XUI_NODE_TOKEN_KEY` | 空 | base64 32 字节密钥（优先级低于 key 文件） |
| `XUI_NODE_TOKEN_KEY_FILE` | `/etc/x-ui/node_token_key.json` | 0600 keyring 文件 |
| `XUI_DB_FOLDER` | `/etc/x-ui` | 数据库目录 |
| `XUI_LOG_FOLDER` | `/var/log/x-ui` | 日志目录 |

## API

全部挂在 `/panel/api/chain/*`，默认仅 `admin` token。OpenAPI：`/panel/api/openapi.json`。

| 分组 | 端点 |
|---|---|
| 元信息 | `GET /meta` |
| 池 | `GET/POST /pools`、`GET/POST /pools/:id`、`POST /pools/:id/delete` |
| 导入/代理 | `POST /pools/:id/import`、`GET /pools/:id/proxies`、`POST /pools/:id/proxies/bulk`、`POST /proxies/:id/probe`、`POST /proxies/:id`（编辑） |
| 生成/运维 | `POST /pools/:id/probe`、`/plan`、`/apply`、`/failover` |
| 节点 | `GET /nodes`、`GET /nodes/:id`、`POST /nodes/:id/{enable,disable,rebind,delete}`、`POST /nodes/bulk`、`POST /nodes/export` |
| 任务/审计 | `GET /jobs`、`GET /jobs/:id`、`GET /audit` |

示例：

```bash
TOKEN=...  # Settings → Security → API Token (admin)
curl -s -H "Authorization: Bearer $TOKEN" \
  https://panel.example.com/panel/api/chain/pools

curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"content":"socks5://u:p@1.2.3.4:1080","dryRun":true}' \
  https://panel.example.com/panel/api/chain/pools/1/import
```

## 运维

```bash
systemctl status x-ui          # 服务状态
journalctl -u x-ui -n 100 -f   # 日志
x-ui                           # 面板管理菜单（升级/备份/证书/日志）
sudo bash install-chain.sh     # 升级到最新 Release（保留数据库与密钥）
```

**备份**：数据库 `/etc/x-ui/x-ui.db` + 环境文件 `/etc/default/x-ui`（含加密密钥）。两者缺一不可；只恢复数据库而丢密钥会导致代理密码不可解。

**恢复**：停服务 → 还原 `x-ui.db` 与 `/etc/default/x-ui` → 启动 → 节点页点“生成”做一次对账。

## 开发与测试

```bash
make dist-stub                    # 让 go:embed 找到 dist 占位
go test ./...                     # 全仓测试
golangci-lint run ./internal/... . # 0 issues 是当前基线
cd frontend && npm ci && npx vitest run --project unit --project components
npm run build                      # 产出新 UI 到 internal/web/dist
make chain-e2e XRAY_E2E_BINARY=/path/to/xray  # 真实核心端到端
```

端到端测试会启动真实 Xray：活跃节点必须命中标记 SOCKS5，处于闸门的节点不得拿到任何上游响应。

发布：Actions → **Chain Release** → 运行（填写 tag），会构建 `linux-amd64/arm64` 两个包并附加到 Release；安装脚本据此下载。

> 说明：本仓库默认关闭了 Actions。发布前在 Settings → Actions 打开，并只保留 **Chain Release**（上游 CI/release/docker 工作流建议在 Actions 页面 Disable，避免缺少 secrets 的失败运行）。由 `GITHUB_TOKEN` 创建的 tag 不会再次触发其它工作流。

## 故障排查

**面板显示 Xray「停止」，日志出现 `fork/exec bin/xray-linux-amd64: no such file or directory`**

发布包内的核心文件名与面板期望的不一致（面板固定查找 `bin/xray-linux-<arch>`）。安装脚本会做兼容重命名；若手工安装，执行：

```bash
cd /usr/local/x-ui/bin
mv xray xray-linux-amd64        # arm64 机器改为 xray-linux-arm64
chmod +x xray-linux-*
systemctl restart x-ui
systemctl status x-ui --no-pager
pgrep -a xray
```

**面板能开但核心启动即退出**：先跑 `cd /usr/local/x-ui && ./bin/xray-linux-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') run -c bin/config.json` 看具体报错。

## 已知限制与路线图

- **多节点下发未实现**：3X-UI 的 node-sync 契约只同步入站/客户端，出站与路由无法安全下发；需要扩展上游 API，列为独立里程碑。
- **入站协议仅 VLESS**；`dedicated` 模式当前回退为分片布局。
- 未在真实商业 SOCKS5 供应商上做 200 条实网压测（e2e 使用本地假 SOCKS5）。
- 前端浏览器端到端未纳入 CI（组件/单测 + 构建已覆盖）。

## 许可证与致谢

- 本项目遵循 **GPL-3.0**，完整文本见 [LICENSE](LICENSE)。
- 上游：[MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)；代理内核：[XTLS/Xray-core](https://github.com/XTLS/Xray-core)。
- 模块设计与实现记录：[docs/chain-proxy.md](docs/chain-proxy.md)。

---

## English overview

**3X-UI AuTo · ChainForge** is a fork of 3x-ui (v3.11.0) that turns a pasted list of SOCKS5
proxies into ready-to-sell chained nodes:

- import up to 5000 proxies with a dry-run preview, dedupe and optional private-address policy;
- generate one VLESS inbound shard per N clients, one client per proxy, one `socks` outbound per
  node and a `user`-keyed routing rule — injected at config-generation time, never written into
  your Xray template;
- verify activation against the real core router (`TestRoute`) and fail closed to the blackhole
  outbound on mismatch;
- probe TCP/SOCKS5/HTTP egress, auto-failover to a spare proxy, keep the outbound tag stable;
- bulk enable/disable/delete/rebind, export share links, per-node detail with QR codes;
- credentials encrypted with row-bound AES-256-GCM; `x-ui encrypt-tokens` rotates both node tokens
  and proxy passwords.

Install on Linux (after a release exists):

```bash
curl -fsSL https://raw.githubusercontent.com/Fyzzp0o0/3X-UI_AuTo/main/install-chain.sh -o install-chain.sh
sudo bash install-chain.sh
```

Build from source: `make dist-stub && (cd frontend && npm ci && npm run build) && go build -o x-ui .`

License: **GPL-3.0**. Upstream: [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui).
