# 🖥️ CPA2API-Manager

<div align="center">

**专为 CPA2API 设计的高颜值、企业级智能体网关管理面板与遥测数据看板**

[![React](https://img.shields.io/badge/React-18+-61DAFB?style=for-the-badge&logo=react&logoColor=black)](https://react.dev)
[![Vite](https://img.shields.io/badge/Vite-5+-646CFF?style=for-the-badge&logo=vite&logoColor=white)](https://vitejs.dev)
[![TypeScript](https://img.shields.io/badge/TypeScript-5+-3178C6?style=for-the-badge&logo=typescript&logoColor=white)](https://www.typescriptlang.org)
[![Docker](https://img.shields.io/badge/Docker-seakee/cpa--manager-blue?style=for-the-badge&logo=docker&logoColor=white)](https://hub.docker.com/r/seakee/cpa-manager)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg?style=for-the-badge)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=for-the-badge)](CONTRIBUTING.md)
[![Version](https://img.shields.io/badge/Version-v1.3.3--s.1-orange?style=for-the-badge)](#)

</div>

---

## 📖 项目简介

**CPA2API-Manager** 是专为 **CPA2API**（OpenAI 兼容智能体代理网关）深度定制的专业级可视化管理面板与高并发遥测数据看板。

采用现代前端技术栈（**React 18 + Vite 5 + TypeScript + Vanilla CSS / Tailwind**）倾力打造，CPA2API-Manager 拥有极具奢华感的高级暗黑玻璃拟态（Glassmorphism）交互设计、流畅的响应式微动画，并集成了渠道评分调度、无侵入图形配置编辑器、极致遥测分析以及异构模型重映射回退等功能。

---

## ✨ 核心特性

*   **📊 实时遥测与数据看板**：内置超低延迟的图表统计，可视化呈现请求吞吐量、首字延迟（TTFT）、调用成功率以及精细化的 Token 计费消耗指标。
*   **⚖️ 渠道优先级与打分调度**：提供直观的拖拽和数字评分面板，支持多渠道/账号优先级动态配置、轮询权重配比、故障熔断重试等机制，为智能体业务高可用保驾护航。
*   **⚙️ 本地化可视化配置编辑**：提供安全友好的 JSON/YAML 交互编辑器，支持热重载，无需在 SSH 中手动编辑复杂的 `config.yaml` 即可完成高级路由与过滤器修改。
*   **🔄 异构模型智能路由与回退**：支持 Gemini/Qwen 等多厂商异构模型的灵活别名注册与智能映射，无缝向后兼容，并在单一上游故障时自动执行平滑降级和灾备回退。
*   **🎨 奢华现代设计语言**：遵循现代 UI/UX 最佳实践，支持自适应系统暗黑模式、流光渐变边框、柔和毛玻璃滤镜与细腻的悬停反馈，打造极致的高端使用体验。

---

## 🏷️ 版本说明

本项目采用清晰的版本控制机制，便于追踪上游变更与本地定制：
*   **当前版本**：`v1.3.3-s.1`
    *   **主版本前缀 `v1.3.3`**：代表与主控面板核心功能版本完全对齐。
    *   **定制后缀 `-s.1`**：代表由 Skloxo 维护的专属定制分支，包含优化的账号优先级调度算法、增强的本地化 UI 编辑以及与 Qwen 自定义标签网关的无缝对接。

---

## 🛠️ 本地开发与构建指南

### 1. 准备开发环境
确保您的本地已安装 [Node.js](https://nodejs.org) v18+ 并且具备包管理器（如 npm / pnpm / yarn）。

### 2. 克隆项目与安装依赖
```bash
# 进入前端工作空间目录
cd /home/skloxo/aho/openclaw/project/qwen2api/CPA2API-Manager

# 安装依赖项
npm install
```

### 3. 运行本地开发服务器
```bash
npm run dev
```
开发服务器启动后，通常会在控制台输出：
```text
  VITE v5.x.x  ready in xxx ms

  ➜  Local:   http://localhost:5173/
  ➜  Network: use --host to expose
```
打开浏览器访问 [http://localhost:5173](http://localhost:5173) 即可实时调试和预览。

### 4. 代码规范检查与格式化
在提交代码之前，请运行以下指令以保证代码质量符合规范：
```bash
# 运行 ESLint 进行静态代码检查
npm run lint

# 使用 Prettier 格式化样式与布局
npm run format
```

### 5. 构建生产包
当开发完毕需要打包部署时，运行编译脚本：
```bash
npm run build
```
编译成功后，将在 `dist/` 目录下生成高压缩的静态网页资源，可直接使用 Nginx 进行分发，或嵌入到 Go 后端服务中直接读取。

---

## 📦 Docker 容器部署指南

CPA2API-Manager 支持以纯静态模式，或者搭配独立的 Node 服务与 SQLite 数据库实现完整的请求审计遥测。

### 1. 遥测服务器配置 `docker-compose.usage.yml`
```yaml
version: '3.8'

services:
  cpa-manager:
    image: seakee/cpa-manager:v1.3.3-s.1
    build:
      context: .
      dockerfile: Dockerfile.usage-service
    restart: unless-stopped
    ports:
      - "18317:18317"
    environment:
      - PORT=18317
      - DB_PATH=/app/data/usage.db
    volumes:
      - cpa-manager-data:/app/data
      - ./usage-service/config:/app/config
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:18317/health"]
      interval: 30s
      timeout: 10s
      retries: 3

volumes:
  cpa-manager-data:
    external: true
```

### 2. 后台启动遥测面板
```bash
docker compose -f docker-compose.usage.yml up -d
```

---

## 🤝 贡献规范

欢迎提交 Issue 或 Pull Request！我们追求高内聚低耦合的代码设计，并在 [CONTRIBUTING.md](CONTRIBUTING.md) 中详细列出了 React/Vite 组件命名规则、ESLint 校验与 CSS 变量的配置说明，请务必在提交修改前通读。

---

## ⚖️ 免责声明

> [!CAUTION]
> **CPA2API-Manager 仅供学术研究、个人学习以及技术验证目的使用，严禁用于任何商业用途。**
> 
> 使用者在使用本软件时，必须自行确保其行为完全符合相关服务提供商的使用条款、服务协议以及当地法律法规。开发人员对于使用者因违规接入、非法调度或滥用所引发的任何账号封禁、系统停机、数据丢失或法律责任均不承担任何连带责任。
