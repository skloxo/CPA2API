# CPA2API 开发者文档与架构知识总索引 Hub (Consolidated Knowledge Index)

> **项目版本**: `v7.1.45-s13`  
> **文档定位**: 本文档收拢归拢了 CPA2API 项目及 CPA 架构下的全部辅助开发文档、规格说明、部署运维指南与开发规范。所有的开发与运维操作均可从本文档快速索引导航。

---

## 🧭 一、 核心架构与主规格 Hub (Core Architecture & Master Specs)

| 文档名称 | 物理路径 | 核心内容与适用场景 |
| :--- | :--- | :--- |
| **主架构与运维集成规范** | [`skills/cpa2api-skill/SKILL.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/skills/cpa2api-skill/SKILL.md) | **绝对最高规范**：CPA 核心设计原则、Clean-Pristine 纯净显示规则、7-Point 提供商对齐清单、Session 重载避坑指南。 |
| **版本号规范与交付履历** | [`REFACTORING_PLAN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/REFACTORING_PLAN.md) | 物理版本号命名公理（`<基座>-s<递增>`）、全版本交付 Changelog、重构履历历史记录。 |
| **原子化任务卡片库** | [`docs/TASK_CARDS.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/TASK_CARDS.md) | 未来改进与重构计划的原子化任务卡片（CARD-101 ~ CARD-106），包含依赖边界与验收指令。 |

---

## 🛠️ 二、 部署运维与开发构建 Hub (Deployment & Build Automation)

| 文档/脚本名称 | 物理路径 | 核心内容与适用场景 |
| :--- | :--- | :--- |
| **服务部署与运维指南** | [`docs/deployment.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/deployment.md) | Systemd 服务配置、端口规划（8317 生产 / 9317 开发）、路径挂载与日志轮转说明。 |
| **一键开发构建脚本** | [`scripts/dev_build.sh`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/scripts/dev_build.sh) | **一键自动化 Dev Loop**：自动完成 `Vite 打包 -> 产物同步 -> Go 编译(版本号注入) -> Systemd 重启` 闭环。 |
| **系统配置文件** | [`config.yaml`](file:///home/skloxo/aho/cpa2api/config.yaml) | 当前生产 8317 服务的全局配置，包含全量 32 厂商、55 Key 凭证、国内直连与代理分流设定。 |

---

## 📚 三、 Go SDK 接入与高级功能 Hub (SDK & Advanced Features)

| 文档名称 | 物理路径 | 核心内容与适用场景 |
| :--- | :--- | :--- |
| **SDK 接入基础指南 (中文)** | [`docs/sdk-access_CN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/sdk-access_CN.md) | Go 语言场景下快速引入 CPA2API SDK 进行 API 调用与初始化。 |
| **SDK 使用全量参考 (中文)** | [`docs/sdk-usage_CN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/sdk-usage_CN.md) | OpenAI、Claude、Gemini、Qwen 协议转译的具体调用示例与代码段。 |
| **SDK 高级特性与配置 (中文)** | [`docs/sdk-advanced_CN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/sdk-advanced_CN.md) | 渠道优先级、自定义 Header 映射、多 Key 轮换策略与代理设置。 |
| **SDK 文件变更监听 (中文)** | [`docs/sdk-watcher_CN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/sdk-watcher_CN.md) | 配置文件与凭证 JSON 文件的动态 Watch 热重载使用指南。 |

---

## 📐 四、 开发规范与网络测试 Hub (Standards & Testing)

| 文档名称 | 物理路径 | 核心内容与适用场景 |
| :--- | :--- | :--- |
| **内部开发规范与约束** | [`docs/internal/DEVELOPMENT_STANDARDS.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/internal/DEVELOPMENT_STANDARDS.md) | 代码风格、Go 类型约束、错误处理逻辑与模块高内聚设计规范。 |
| **Web 代理与网络测试标准** | [`docs/testing/web_proxy_testing_standard.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/testing/web_proxy_testing_standard.md) | 代理连通性测试、WAF 伪装验证与并发基准测试规范。 |

---

## 📌 五、 开发者快速工作流 (Developer Quick Workflow)

1. **新改动开发**：运行 `./scripts/dev_build.sh` 一键编译与部署；
2. **打 Tag 与提交**：遵循版本公理（如 `v7.1.45-s14`），更新 [`REFACTORING_PLAN.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/REFACTORING_PLAN.md) 记录履历；
3. **任务推进**：按 [`docs/TASK_CARDS.md`](file:///home/skloxo/aho/openclaw/project/CPA/CPA2API/docs/TASK_CARDS.md) 领取卡片，验收后打钩交付。
