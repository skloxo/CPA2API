# CPA2API (Unified CLIProxyAPI with first-class Qwen Web support for Agentic AI)

CPA2API 是一个专为 Agentic AI 设计的高性能、高可用 API 网关与代理适配器。它能够无缝地将上游 Qwen 平台的强大对话能力转换为标准的 OpenAI `/v1/chat/completions` 接口规范，为各类 AI 智能体（Agents）及客户端应用提供无状态、高度可靠的后端支撑层。

## 🚀 核心特性

*   **支持 Qwen 流式与非流式生成**：针对流式输出进行了深度优化，确保极低的首字延迟与平滑的流式响应体验。
*   **流式并行工具调用解析与 JSON 修复引擎**：支持自定义 XML 标签机制（`custom_tool_call`），在流式传输过程中实时解析并行工具调用，并提供自动 JSON 容错与修复能力，确保 Agent 动作的高可靠性。
*   **无状态聊天会话上下文管理**：优雅解耦多轮对话的上下文映射与缓存，有效避免上游历史开销累积以及跨账号上下文污染。
*   **多模态图片上传转换**：支持多模态视觉模型的图片及素材的高效上传与适配，兼容主流视觉语言模型（VLM）的数据格式要求。
*   **Keep-alive SSE 心跳机制**：在深度思考或耗时搜索阶段，定期发送轻量级 SSE 保持心跳，防止中间网络代理（如 Nginx、CDN）以及 HTTP 客户端触发读取超时。
*   **工具响应输出预算截断**：智能预算工具返回的文本大小，采用首尾保留的截断策略，防止工具输出过长导致上下文爆满或超出 Token 限制。

## 🏷️ 版本号规范

本项目采用清晰的版本控制机制，便于追踪上游变更与本地定制：
*   **格式**：`v[UpstreamVersion]-[SkloxoPatchVersion]`
*   **示例**：`v7.1.20-s.1`
    *   **前缀 `v7.1.20`**：映射并同步上游 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 的正式发行版。
    *   **后缀 `-s.1`**：代表由 Skloxo 维护的专属定制补丁/优化版本（如 Qwen 自定义 XML 标签绕过、高级流式心跳等特性的演进版本）。

## 📦 部署与运行指南

### 1. 使用 Docker Compose 部署

我们推荐使用 Docker Compose 进行一键部署与容器化管理。以下是标准的配置示例：

```yaml
services:
  cli-proxy-api:
    image: eceasy/cli-proxy-api:v7.1.20-s.1
    container_name: cli-proxy-api
    network_mode: host
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./auths:/root/.cli-proxy-api
      - ./logs:/app/logs
    restart: unless-stopped
```

### 2. 启动服务

在包含 `docker-compose.yml` 的目录下执行以下命令以启动服务：

```bash
docker compose up -d
```

启动后，服务将根据 `config.yaml` 的配置监听相应端口，为您提供标准 OpenAI 兼容的 API 服务。

## ⚖️ 免责声明

> [!CAUTION]
> **CPA2API 仅供学术研究、个人学习以及技术验证目的使用，严禁用于任何商业用途。**
> 
> 本项目中所实现的代理及接口转换机制仅作演示与测试。使用者在使用本工具时，必须自行确保其行为完全符合相关服务提供商的使用条款、服务协议以及当地法律法规。开发者对于因使用本软件而导致的任何服务中断、账号封禁，或任何直接、间接的损失及法律责任，均不承担任何责任。
