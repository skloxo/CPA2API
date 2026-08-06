# CPA2API 原子化重构与迭代任务卡片库 (Task Cards Tracker)

> **版本基准**: `v7.1.45-s13`  
> **文档说明**: 本文档将 CPA2API 后续的系统重构、性能优化、健壮性增强与监控运维拆解为独立、原子化的任务卡片（Task Cards）。每个任务卡片包含独一无二的 ID、优先级、依赖边界、验收标准与验证命令。

---

## 📋 任务卡片看板概览 (Task Cards Board)

| 任务卡片 ID | 任务名称 | 领域 | 优先级 | 状态 | 依赖卡片 |
| :--- | :--- | :--- | :---: | :---: | :---: |
| **CARD-101** | `usage.sqlite` WAL 自动截断与历史日志定期清理 | 数据库 / 存储 | P1 | ⏳ 待开始 | 无 |
| **CARD-102** | Qwen WAF Cookie 极长连接心跳保鲜与后台静默续期 | 认证 / 协议 | P1 | ⏳ 待开始 | 无 |
| **CARD-103** | 错误码差异化退避与动态 Cooldown 冷却惩罚 | 路由 / 负载均衡 | P2 | ⏳ 待开始 | 无 |
| **CARD-104** | 提供商一键测速 (TTFT / TPS) 与健康度探针 | 前端 / 监控 | P2 | ⏳ 待开始 | 无 |
| **CARD-105** | 网络分流代理/直连可视化拓扑图 | 前端 / 运维 | P3 | ⏳ 待开始 | CARD-104 |
| **CARD-106** | 开发态 Air (Go Live Reload) 与 Vite Dev Server 无缝代理集成 | 开发体验 | P3 | ⏳ 待开始 | 无 |

---

## 📌 任务卡片详情 (Task Card Details)

### 💳 [CARD-101] `usage.sqlite` WAL 自动截断与历史日志定期清理

- **优先级**: P1 (高)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: 无
- **影响范围**: `internal/usageservice/store/`
- **问题背景**:
  目前 `usage.sqlite` 数据库体积已达到 46MB。随着日均数万级请求的调用，WAL 模式日志文件（`-wal` / `-shm`）不断增大，缺乏自动截断与历史失效日志清理机制，长时间运行可能增加磁盘 I/O 开销与空间占用。
- **原子化范围与实现计划**:
  1. 在 `usageservice/store` 中增加定时清理任务，默认每 24 小时自动执行 `PRAGMA wal_checkpoint(TRUNCATE);`；
  2. 增加配置文件项 `usage-retention-days: 30`（默认保留最近 30 天日志），自动清理过期的请求记录。
- **验收标准 (Acceptance Criteria)**:
  - 启动服务后自动触发 WAL 截断，`usage.sqlite-wal` 体积保持在 1MB 以内；
  - 数据库能根据 `usage-retention-days` 自动擦除超过指定天数的旧记录。

---

### 💳 [CARD-102] Qwen WAF Cookie 极长连接心跳保鲜与后台静默续期

- **优先级**: P1 (高)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: 无
- **影响范围**: `internal/auth/qwen/`
- **问题背景**:
  虽然已有 `GET /api/v2/user/info` 轻量级心跳探针，但在极长连接（如持续 2 小时以上的超长对话生成）场景下，阿里云 WAF 的 `acw_tc` Cookie 或临时握手凭证仍有概率被上游刷新，导致后续长文本输出被截断。
- **原子化范围与实现计划**:
  1. 在 `QwenTokenStorage` 中引入 Cookie 过期倒计时与自动滑动刷新（Sliding Expiration）；
  2. 当 `acw_tc` 存活时间达到临界阈值（例如 30 分钟）时，后台开启协程静默发送轻量握手包刷新 Cookie 并自动落盘。
- **验收标准 (Acceptance Criteria)**:
  - 连续模拟 3 小时 Qwen 请求，`acw_tc` Cookie 能自动静默续期，零 401 / 403 抛错。

---

### 💳 [CARD-103] 错误码差异化退避与动态 Cooldown 冷却惩罚

- **优先级**: P2 (中)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: 无
- **影响范围**: `internal/routing/` / `internal/api/`
- **问题背景**:
  当前当某个 API Key 在多 Key 池内触发错误时，目前的退避冷却机制未对错误类型进行差异化划分，401 无效凭证与 429 限流退避混在一起。
- **原子化范围与实现计划**:
  1. 遇到 `401 Unauthorized` ➔ 判定为凭证失效，立即标注 `Disabled` 并触发告警；
  2. 遇到 `429 Too Many Requests` ➔ 实施指数退避（5s ➔ 15s ➔ 60s），冷却期结束后自动解封恢复并发。
- **验收标准 (Acceptance Criteria)**:
  - 测试 401 密钥直接被禁用；测试 429 密钥在指定冷却时间后自动重回可用池。

---

### 💳 [CARD-104] 提供商一键测速 (TTFT / TPS) 与健康度探针

- **优先级**: P2 (中)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: 无
- **影响范围**: `web/src/pages/AiProvidersPage.tsx` / `internal/api/`
- **问题背景**:
  在【AI 提供商】界面中，缺乏直观各厂商实时响应速度（TTFT 首包延迟）与吐字速率（TPS）的测速数据。
- **原子化范围与实现计划**:
  1. 在 React 控制面板【AI 提供商】卡片顶部增加【一键测速】按钮；
  2. 发起后端测速请求，衡量 TTFT（ms）与 TPS（Tokens/s）并实时展示性能 Badge。
- **验收标准 (Acceptance Criteria)**:
  - 点击测速后，界面毫秒级展示厂商响应延迟与吞吐速率。

---

### 💳 [CARD-105] 网络分流代理/直连可视化拓扑图

- **优先级**: P3 (低)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: CARD-104
- **影响范围**: `web/src/pages/AiProvidersPage.tsx`
- **问题背景**:
  虽然已实现了国内厂商（`MiMo-CN`、`商汤`、`白山`、`龙猫`）直连、海外厂商走代理的分流，但在界面上缺乏可视化的路由拓扑指示。
- **原子化范围与实现计划**:
  1. 在每个提供商卡片上标记网络路由节点图标（`⚡ 国内直连` 或 `🌐 Clash 代理`）；
  2. 鼠标悬停可查看对应的代理 URL 与物理 RTT 延迟。
- **验收标准 (Acceptance Criteria)**:
  - 界面直观区分网络走路线路，直连与代理一目了然。

---

### 💳 [CARD-106] 开发态 Air (Go Live Reload) 与 Vite Dev Server 无缝代理集成

- **优先级**: P3 (低)
- **状态**: ⏳ 待开始 (Pending)
- **依赖**: 无
- **影响范围**: `scripts/` / `internal/`
- **问题背景**:
  虽然已有 `scripts/dev_build.sh` 一键自动构建，但在极高频修改代码调试时，实时热更新（HMR）能提供更高效的开发体验。
- **原子化范围与实现计划**:
  1. 引入 `.air.toml` 配置文件，实现修改 Go 后端代码 0.2 秒热重启；
  2. 配置 Vite 代理映射，使得修改 React 前端代码时无需编译直接 HMR 生效。
- **验收标准 (Acceptance Criteria)**:
  - 开发者保存代码后，服务端与网页端自动增量刷新。
