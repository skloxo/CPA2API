# 🌟 CPA2API Integration & Operations Master Specification

> **Document Status**: 🟢 Active / Production-Ready  
> **Skill Path**: `skills/cpa2api-skill/SKILL.md`  
> **Core Focus**: High-fidelity OpenAI-compatible translation, robust OAuth/Session management, high-performance hot-reload capability, and unified operations.

---

> [!IMPORTANT]
> **指挥家与代理铁律 (Agent Command Line Order)**:  
> Before any future Agent edits or executes commands in the CPA2API / CPA codebase, they **MUST** read this skill specification file in its entirety. Do not break the "Canonical representation -> Translation" architecture.  
> 在任何 AI 代理修改或操作 CPA 及其派生代码库之前，**必须**完整阅读本文档。禁止破坏“规范模型表示 -> 适配翻译器”的基础架构设计。

---

## 🤖 STARTUP DIRECTIVE FOR FUTURE AI AGENTS

If you are an AI coding assistant (like Antigravity or a successor system) tasked with modifying, refactoring, or extending the CLIProxyAPI (CPA) / CPA2API codebase, **you must inject the following command directive into your startup sequence.**

Copy and paste the prompt below into your context instructions:

```markdown
================================================================================
🤖 AI AGENT STARTUP DIRECTIVE - READ THIS BEFORE TOUCHING CODE
================================================================================
You are about to edit the CPA2API / CPA Go codebase. You MUST strictly adhere to 
the Technical Specification and Specs set forth in:
`skills/cpa2api-skill/SKILL.md`

Please verify the following rules before starting:
1. DO NOT touch the production services running on Ports 8317 and 18317. 
   All development, testing, and debugging must occur on Dev Ports (9317 / 19317).
2. UNDERSTAND the "Clean-Pristine" Display Name Rule: Do not map Provider Model IDs 
   to friendly names. Preserve the provider's upstream original identifiers in all list views.
3. ENFORCE credential safety: Ensure new credentials or edits write to disk with 
   file permission `0600`. Preserve metadata when JSON Patching.
4. OBSERVE the config reload debounce parameters (150ms). Do not rewrite files 
   in rapid loops, which triggers "Watch-Reload Session Blowup".
5. IF you are adding a new provider, you MUST register it across all 7 points 
   detailed in the "First-Class Provider Alignment Checklist".

To confirm you have read this, start your first thought sequence with:
"Acknowledged. I have read the CPA Integration Skill Spec, and will strictly 
conform to the 7-Point checklist and the Clean-Pristine rule."
================================================================================
```

---

## 🛠️ PART I: Developer Specification & Integration Architecture

### 🏛️ 1. CPA Core Architecture Map (核心架构拓扑)

The CLIProxyAPI (CPA) codebase operates on a high-concurrency, decoupled pipeline designed for rapid request synthesis, dynamic failover, and zero-downtime hot reloading. Below is the technical structural blueprint illustrating the request lifecycle and orchestrating components, featuring Qwen as a primary example of a provider plugin integrated under the CPA framework.

```mermaid
graph TD
    %% Styling and Color Palettes (Curated harmoniously)
    classDef client fill:#e0f7fa,stroke:#00acc1,stroke-width:2px;
    classDef core fill:#e8f5e9,stroke:#43a047,stroke-width:2px;
    classDef watch fill:#fff3e0,stroke:#fb8c00,stroke-width:2px;
    classDef run fill:#f3e5f5,stroke:#8e24aa,stroke-width:2px;
    classDef upstream fill:#ffebee,stroke:#e53935,stroke-width:2px;

    %% Elements
    Client["Client / OpenClaw / Hermes<br>(OpenAI SDK API Format)"]:::client
    Handlers["HTTP Handlers<br>(internal/api/handlers)"]:::core
    Conductor["Conductor / Service Manager<br>(sdk/cliproxy/service.go)"]:::core
    Watchers["Watchers / Config Hot-Reloader<br>(internal/watcher)"]:::watch
    Registry["Model Registry & Discoverer<br>(internal/registry)"]:::core
    Translator["Provider Translator (e.g. Qwen)<br>(internal/translator/qwen)"]:::run
    Executor["Provider Executor (e.g. Qwen)<br>(internal/runtime/executor)"]:::run
    AuthManager["Provider Auth / SSO / Token<br>(internal/auth/qwen)"]:::run
    QwenAPI["chat.qwen.ai Upstream<br>(Qwen Web Interface)"]:::upstream

    %% Connections
    Client -->|HTTPS /v1/chat/completions| Handlers
    Handlers -->|Orchestrate Request| Conductor
    Watchers -.->|Hot Reload / Watch Files| Conductor
    Watchers -.->|Update Available Clients| Registry
    Registry -->|Provide Model Definitions| Conductor
    Conductor -->|Select Auth Channel & Route| Translator
    Translator -->|1. Request translation<br>2. Anti-hallucination sieve<br>3. Suffix feature mapping| Executor
    AuthManager -->|Provide JWT Access Token| Executor
    Executor -->|Socks5/HTTP Proxy + ssxmod Browser Fingerprints| QwenAPI
    
    %% Config mapping and persistence
    Watchers -.->|Scan config.yaml & auths/*.json| AuthManager
```

#### 🧩 Core Component Roles (组件职责解析)

*   **HTTP Handlers (`internal/api/` & `internal/api/handlers/`)**: Standardized OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`) using the **Gin** HTTP framework. Bypasses requests to the Conductor.
*   **Conductor (`sdk/cliproxy/service.go`)**: The traffic cop of the system. Orchestrates account scheduling, concurrency limits, load-balancing (Round-Robin or Fill-First), session affinity, and overall executor routing.
*   **Watchers (`internal/watcher/`)**: Low-latency `fsnotify`-driven watchers monitoring `config.yaml` and credential files (`auths/*.json`). Implements a strict **150ms debounce window** to consolidate system I/O writes into unified hot-reload cycles.
*   **Registry (`internal/registry/`)**: Maintains structural definitions of active model metadata. Supports dynamic model discovery via upstream requests alongside static local definitions.
*   **Translators (`internal/translator/<provider>/`, e.g. `qwen/`)**: Performs deep translation of payload states for provider plugins (e.g., Qwen):
    1.  Translates standard chat messages (System/User/Assistant/Tool) into upstream schemas.
    2.  Resolves OpenAI Tool definitions to the specific provider's tool-calling formats.
    3.  Runs the **34-rule Anti-Hallucination Engine** (`anti_hallucination.go`) to prevent recursive AI breakdown.
*   **Executors (`internal/runtime/executor/<provider>_executor.go`, e.g. `qwen_executor.go`)**: Runs the physical network pipeline for each provider plugin. Manages connection sessions, handles low-level HTTP headers, injects browser-grade cookies/User-Agents, and bypasses WAF limitations.
*   **Auth Manager (`internal/auth/<provider>/`, e.g. `qwen/`)**: Conducts standard automated credential and token management (e.g., email+password sign-in sequences). Converts credentials to JWT tokens and saves them locally under strict file permission mandates (`0600`).

---

### 🔄 2. Model Name Lifecycle & Evaluation Pipeline

Adding support for dynamic, adaptive, and highly flexible model routing requires understanding the lifecycle of a requested model name. Qwen acts as one of the key provider plugins under the broader CPA framework.

#### 🗺️ Model Lifecycle Flow

```mermaid
flowchart TD
    %% Style definitions
    classDef startEnd fill:#cfd8dc,stroke:#37474f,stroke-width:2px;
    classDef process fill:#fff9c4,stroke:#fbc02d,stroke-width:2px;
    classDef check fill:#e1bee7,stroke:#8e24aa,stroke-width:2px;
    
    A["Client /v1/chat/completions Request<br>(requestedModel: 'qwen3-max-thinking')"]:::startEnd --> B["1. Parse Suffix<br>(thinking.ParseSuffix)"]:::process
    B --> C["Result:<br>baseModel = 'qwen3-max'<br>suffix = '-thinking'"]:::process
    
    C --> D["2. Apply OAuth Model Alias<br>(sdk/cliproxy/auth/oauth_model_alias.go)"]:::process
    D --> E{"Does Alias exist<br>for current channel?"}:::check
    E -- Yes --> F["Replace baseModel with config value<br>(e.g. 'qwen3-max' -> 'qwen3-max-2025')"]:::process
    E -- No --> G["Retain baseModel<br>(baseModel = 'qwen3-max')"]:::process
    
    F --> H["3. Re-assemble Upstream ID"]:::process
    G --> H
    
    H --> I["If suffix is thinking/search,<br>inject feature_config parameter<br>(thinking_enabled = true)"]:::process
    I --> J["4. Excluded Models Filter<br>(oauth-excluded-models config)"]:::process
    J --> K{"Is Model Excluded?"}:::check
    K -- Yes --> L["Abort request with 400 Bad Request"]:::startEnd
    K -- No --> M["5. Execute Upstream Chat API"]:::startEnd
```

#### 💎 The "Clean-Pristine" Rule for Display Names (干净原生原则)

> [!NOTE]
> **Bilingual Insight (双语解析)**:  
> When exposing models to clients via `/v1/models` or populating administrative Dashboards (such as `CPA2API-Manager` running on Port 19317), **Qwen original IDs must be preserved exactly as they are** instead of being mapped to generic/friendly names (e.g. "Qwen 3.5 Max").  
> 当将模型暴露给客户端或在管理后台展示时，必须严格保留 Qwen 原始的上游 ID（例如 `qwen3.5-plus`），禁止将其映射为“通俗友好”的显示名称。

*   **Why?** This prevents cascading developer confusion regarding capabilities. Upstream web channels constantly roll out specialized versions (e.g., `-thinking`, `-search`, `-deep-research`). Forcing the Display Name to equal the exact Model ID ensures transparent verification of executing models.
*   **Code Implementation**: In the management endpoint Handler (`GetAuthFileModels` inside `internal/api/handlers/management/auth_files.go`):
    ```go
    displayName := m.DisplayName
    if isQwen || m.Type == "qwen" || m.OwnedBy == "qwen" {
        displayName = m.ID // Force Clean-Pristine Upstream Display Name
    }
    ```

---

### 📋 3. First-Class Provider Alignment Checklist

Adding any new API Provider plugin (e.g., Gemini, Claude, Kimi, Qwen, or future engines) into the CPA Core requires precise registration across multiple files. The following checklist details the exact Go source code modifications required to grant first-class status, illustrated here with the Qwen provider integration.

| Step | Target File Path | Action / Switch-Case Registration | Purpose |
|:---:|:---|:---|:---|
| **1** | `internal/constant/constant.go` | Define a logical constant: `const ProviderQwen = "qwen"` | Avoid string typos across packages |
| **2** | `sdk/cliproxy/auth/oauth_model_alias.go` | Add case to `OAuthModelAliasChannel` switch:<br>`case "gemini-cli", ..., "qwen": return provider` | Enforces alias routing matching rules |
| **3** | `sdk/auth/refresh_registry.go` | Register refresh callback in `init()`:<br>`registerRefreshLead("qwen", func() Authenticator { return NewQwenAuthenticator() })` | Enables the automatic token-refresh daemon |
| **4** | `sdk/cliproxy/service.go` | Register the Authenticator in `newDefaultAuthManager()`:<br>`sdkAuth.NewQwenAuthenticator()` | Wire authenticator into service initialization |
| **5** | `sdk/cliproxy/service.go` | Add dynamic runtime executor binder to `ensureExecutorsForAuth`:<br>`case "qwen": s.coreManager.RegisterExecutor(executor.NewQwenExecutor(s.cfg))` | Connects target accounts to execution runtimes |
| **6** | `sdk/cliproxy/service.go` | Bind executor to base home config fallback: `s.coreManager.RegisterExecutor(executor.NewQwenExecutor(s.cfg))` | Guarantees runtime availability in cluster modes |
| **7** | `internal/translator/init.go` | Add empty import to trigger package registry:<br>`_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/qwen"` | Registers the standard -> custom message translator |

---

### 🕳️ 4. Real-World Pitfalls & Golden Lessons Learned

#### 💥 4.1 The "Watch-Reload Session Blowup" (配置重载导致会话爆作)

> [!WARNING]
> **The Critical Issue**:  
> In early revisions, saving a setting, adding a new credential account, or updating the config via the management panel caused a global reload trigger. Because the session keys or cryptographic token signatures were dynamically derived or bound directly to the config file's in-memory pointer lifecycle, **every hot reload invalidated the management authentication state, causing global logouts of all frontend dashboards (CPA2API-Manager)**.

##### 🕵️ Structural Root Causes:
1.  **High-CPU Bcrypt Verification**: The server validated HTTP management keys by directly calling `bcrypt.CompareHashAndPassword` on `RemoteManagement.SecretKey` for **every single request** (including the frequent 1-second usage polling and log querying). This caused massive CPU spikes, resource exhaustion, and high request latency.
2.  **State Reset on Watcher Events**: The File System Watcher (`internal/watcher`) monitors `config.yaml` using `fsnotify`. When it caught a modification, it called `reloadConfig()`, which historically re-instantiated several handler states. If the token session signing key was not static, users were booted immediately.

##### 🛡️ Engineering Fixes & Best Practices:
*   **Session Token Caching**: Implement an in-memory signature cache. Perform the heavy CPU `bcrypt` calculation **only once** when a new key is first provided or the configuration is modified. Subsequent requests should validate against a fast, thread-safe in-memory cache map.
*   **Debounce File Events**: Files are often written in successive bursts by OS text editors. The Watcher relies on a strict `150ms` debounce timer (`configReloadDebounce`) before triggering. This prevents cascading reloads and potential file lock collisions.
*   **Session Isolation**: Decouple HTTP management authentication tokens from the active runtime Provider Executors lifecycle. Config hot-reloading must refresh routing and models, but **never** regenerate security context keys.

---

#### 💾 4.2 Credential Persistence in `QwenTokenStorage` & JSON Patching

To manage provider credentials (such as Qwen) seamlessly, the system uses a strict storage lifecycle. Qwen relies on JWT Access Tokens that expire after a relatively short window (6 hours). Therefore, **username, password, and proxy configurations must survive token refreshes and UI patching**.

```json
// Example of strict 0600-permission Qwen Credential File (qwen-test.json)
{
  "access_token": "eyJhbGciOi...",
  "email": "developer@qwen.ai",
  "password": "SafeAndSecurePasswordHashOrText",
  "proxy_url": "socks5://127.0.0.1:7897",
  "expired": "2026-05-23T19:38:56Z",
  "type": "qwen",
  "priority": 10,
  "note": "Developer Primary Account"
}
```

##### 🛡️ Golden Rules of Credential Persistence:
1.  **JSON Patching Merges, Doesn't Overwrite**: When the user edits fields (such as updating a proxy URL or account priority) via a PATCH call to `/v0/management/auth-files/qwen-test.json`, the management handler (`PatchAuthFileFields` inside `auth_files.go`) reads the existing structure, parses it into map entities, updates *only* the specific fields, and calls `SaveTokenToFile`. This ensures that active login tokens (`access_token` and `expired` stamps) are preserved!
2.  **Password Retention for Refresh Daemon**: The `password` and `proxy_url` fields are explicitly tagged with `omitempty` in `QwenTokenStorage`. During automated refreshing, the `sdk/auth` daemon retrieves the username/password and makes a headless login request via the defined proxy. If the password was wiped out during a PATCH edit, the account would fall into permanent cooldown.
3.  **Strict Security Permissions**: Credential files are written using OS file modes `0600` (Read/Write for Owner only) and parent directories at `0700`.
    ```go
    // Absolute compliance with Go file locking rules:
    f, err := os.OpenFile(authFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
    ```

---

#### 🛠️ 4.3 Tool Name Suffix Obfuscation (工具名称精准混淆设计)

> [!WARNING]
> **Tool Conflict and Obfuscation Limits**:  
> The Qwen Web API actively intercepts specific tool names, causing built-in conflict blocks when client agents attempt to pass standard tools. However, blindly suffixing all tools with "X" (e.g., renaming `exec_command` to `exec_commandX`) backfires when invoking custom tools, triggering upstream Qwen errors such as `"Tool does not exist"`.

##### 🛡️ Design Specifications & Rules:
1.  **Confine Obfuscation to Conflicting Keywords**: Tool name obfuscation must be restricted *strictly* to the predefined list of conflicting keywords mapped inside `obfuscationMap`.
2.  **Original Name Pass-Through**: Any other custom tools or MCP (Model Context Protocol) tools must pass through with their original names intact.
3.  **Code Consistency**: Maintain a centralized map and matching logic to ensure that translation and downstream parsing remain in lockstep.

---

#### 💬 4.4 Stateless Multi-Turn Chat Sessions & Upstream Context Limit (无状态多轮会话与上游上下文极限)

> [!CAUTION]
> **Stateful Session Pitfalls**:  
> The Qwen Web API uses `chat_id` statefully. Reusing `chat_id` across turns while sending the full message history causes context duplication, leading to exponential prompt growth and `500 Internal Server Errors`.

##### 🛡️ Alignment Requirements & Truncation Rules:
1.  **Force Stateless Mode**: We force stateless operations by generating a fresh `chat_id` for every single request, aligning with industry standard reverse-proxies like `yujunzhixue` and `rfym21`.
2.  **Clean Dialogue Joining**: Plaintext dialog history must be joined cleanly using `\n\n` with explicit prefixes:
    *   `System:` for system instructions
    *   `Human:` for user messages
    *   `Assistant:` for assistant responses
3.  **Upstream Context Limit (25K-30K Tokens)**: The Qwen Web backend enforces a hard limit of **25K-30K tokens** per chat session.
4.  **Client-Side History Truncation**: Client agents must implement proactive history budget trimming and truncation (e.g., utilizing character budgets similar to `yujunzhixue`, and slicing head+tail tool outputs) to guarantee the overall prompt remains comfortably below this token limit.
5.  **Tool Output Truncation (Server-Side)**: To prevent massive tool result payloads from blowing up the context window, a server-side truncation limit is enforced in `foldToolMessages`. If any individual tool result exceeds a **6,000 character ceiling**, it is automatically compacted. The truncation preserves the first **3,000 head characters** and the last **1,000 tail characters**, inserting a truncation indicator in the middle: `\n... [TRUNCATED %d CHARS] ...\n`.

---

#### 🕸️ 4.5 Core Developer Traps & Unique Rules (核心开发陷阱与特有规则)

To ensure the stability and security of the integration pipeline, developers must be vigilant about the following engineering traps:

1. **Tool Suffix Obfuscation Bug**:
   * **Trap**: Upstream Web API filters or intercepts specific tool keywords. Attempting to globally obfuscate all tools (e.g., suffixing every tool name with "X") causes standard or custom MCP tools to fail with `"Tool does not exist"` or configuration matching errors.
   * **Rule**: Tool name suffix obfuscation must be confined *strictly* to the predefined list of conflicting keywords defined in the `obfuscationMap`. Any non-conflicting tools must pass through with their original names intact.

2. **Stateless Multi-Turn Session Decoupling**:
   * **Trap**: Reusing the stateful upstream `chat_id` across turns while concurrently sending full chat histories leads to context duplication, exponential prompt growth, and HTTP 500 errors.
   * **Rule**: Force stateless execution by generating a unique, random `chat_id` for every request. Ensure dialogue context is formatted and appended cleanly on the client side with explicit delimiters (`System:`, `Human:`, `Assistant:`) to prevent upstream state retention issues.

3. **Keep-Alive SSE Heartbeats**:
   * **Trap**: Multi-step reasoning or long web searches on the upstream side can take 60 seconds or more. Without active traffic, intermediate HTTP gateways, reverse proxies (like nginx), or client SDKs will trigger read timeouts and sever the connection.
   * **Rule**: Inject empty keep-alive Server-Sent Event (SSE) delta chunks (e.g., space characters or empty data objects) periodically during long reasoning/searching states to keep the TCP/HTTP channel alive.

4. **VLM Image Base64 OSS-Upload Translation**:
   * **Trap**: Passing huge base64-encoded image payloads directly within standard message blocks can trigger upstream size limits and connection aborts.
   * **Rule**: For Vision-Language Model (VLM) requests, convert the incoming base64 image data, upload it to a temporary or persistent Object Storage Service (OSS), and translate the payload structure to point to the OSS URL instead of the inline base64 string.

5. **Temp Folder Path Permission Sandbox Trap**:
   * **Trap**: Operating systems, WSL environments, or Docker containers often restrict write operations to standard `/tmp` directories, causing permissions errors during code execution or file generation.
   * **Rule**: Never hardcode default system temp directories. Always resolve paths to workspace-approved scratch folders (like `<appDataDir>\scratch` or subdirectories inside the active workspace) where the running process is guaranteed to have read and write permissions.

#### 4.5.6 Versioning Policy for Skloxo Fork (版本命名与迭代规则)

To maintain clarity and tracking across the private fork, we use a structured versioning format:
*   **Leading Version Prefix**: The leading version prefix (e.g., `v7.1.19` or `v7.1.20` for the backend, `v1.3.3` for the frontend manager) maps directly to the corresponding release of the upstream repository. This prefix is only incremented when we merge updates from upstream.
*   **Trailing Suffix Patch**: The trailing suffix (e.g., `-s.1`, `-s.2`) maps to our own custom modifications, features (such as Qwen integration), and patches. This suffix is incremented (`-s.1` -> `-s.2` -> `-s.3`) whenever we implement, update, or deploy our custom additions.

#### 4.5.7 Frozen Version Registry & Code Sealing Records (版本封板与归档记录)

The following versions have been fully validated via E2E regression testing (100% PASS rate) and officially frozen for OpenClaw integration:

- **Backend Engine (CPA2API)**: `v7.2.2-s.4` (May 2026) [FROZEN]
  - **Code Repository Path**: `/home/skloxo/aho/openclaw/project/qwen2api/CPA2API`
  - **Staging/Dev Port**: `9317` (cpa2api-dev container)
  - **Production Port**: `8317` (cli-proxy-api container)
  - **Validated Core Features**:
    - Qwen dynamic models discovery & excluded models filtering.
    - System info horizontal alignment.
    - Integrated frontend manager configuration page.
  - **Verification Status**: E2E regression test PASS.

- **Frontend Manager (CPA2API-Manager)**: `v1.3.3-s.1` [FROZEN]
  - **Code Repository Path**: `/home/skloxo/aho/openclaw/project/qwen2api/CPA2API-Manager`
  - **Staging Port**: `19317`
  - **Production Port**: `18317`
  - **Validated Core Features**: Localized proxy status hints, dynamic model fallback listing.

- **Associated Engineering Walkthroughs & Artifacts**:
  - **E2E Test Report**: `/home/skloxo/aho/openclaw/project/qwen2api/CPA2API-TEST-REPORT.md`
  - **Historical Walkthrough Logs**: `walkthrough.md` (fully archived)
  - **Archived Tasks list**: `task.md` (100% completed and sealed)

---

### 🌿 5. 第二大版本 (v7.2.0-s.1) 渐进式集成开发规程 (Monorepo & Occam's Razor Integration)

为了指导未来代理及架构师在第二大版本中以极简、高聚合、无痛同步的方式进行迭代，特制定以下开发规程：

#### 🌿 5.1 单仓合并规范 (Monorepo Subtree Protocol)
*   **规程**：将独立前端仓库 `CPA2API-Manager` 通过 **Git Subtree** 合并至后端仓库的 `web/` 目录中。
    *   **优点**：主仓库中保留完整的源文件，消除 Submodule 带来的初始化断层，同时可以通过一行 Git 命令拉取上游前端仓库的更新：
        `git subtree pull --prefix=web/ https://github.com/router-for-me/CLIProxyAPI-Manager.git main --squash`
*   **编译输出绑定**：在主项目中新增统一编译脚本。在编译前端时，Vite 的 `viteSingleFile` 插件会将代码压缩为 `web/dist/index.html`。脚本必须将其直接复制到 `CPA2API` 本地的 `static/management.html` 目录中。Gin 后端通过 `/management.html` 路由无缝静态分发该文件，彻底废弃老旧、冗余的旧 UI。

#### 🌿 5.2 极限探针原则 (Empirical Context-Limit Probe Protocol)
*   **规程**：对于旗舰模型如 `qwen3.7-max`，**绝对禁止**盲目硬编码官方宣称的 1M 上下文和大额思考长度进行配置注册。
*   **执行步骤**：
    1.  必须先在 `test/verify_max_limits.go` 中编写分档位（8K, 16K, 32K, 64K, 128K+）压力测试脚本。
    2.  在 Staging 开发环境对 Qwen 反代上游执行基准探针，记录真实的最大可用 Payload 截断和报错边界。
    3.  依据**实测稳定的极限值**在 `internal/registry` 中注册模型配置，杜绝虚假宣称导致客户端直接超时报错。

#### 🌿 5.3 奥卡姆剃刀：高度复用原生 Go 调度与刷新模块
*   **规程**：杜绝为 Qwen 单独编写重复的后台 cron 探针、账号轮询或自研数据库表自愈状态机。
*   **调度状态映射**：直接复用 `sdk/cliproxy/auth/scheduler.go` 中的原生调度状态：
    *   正常账号标记为 `scheduledStateReady`。
    *   限流账号直接标记为 `scheduledStateCooldown` 并设定冷却时限。
    *   待激活（通过 Attributes 携带）与封禁账号永久置为 `scheduledStateDisabled`。
*   **刷新机制（保安与医生）**：
    *   直接将 Qwen 账号注册到 `sdk/cliproxy/auth/auto_refresh_loop.go` 现有的最小堆刷新循环中（原生“保安”）。
    *   在 `sdk/auth/qwen.go` 中实现 Authenticator 的 `Refresh` 接口（原生“医生”），在其中封装 Playwright/headless-browser 重登流程。一旦 Token 被“保安”检测为过期，自动触发“医生”刷新凭证并热替换磁盘文件，由文件监听器将其动态加载回内存，实现无感热更替。

#### 🌿 5.4 通用并发槽位与优先级排队机制
*   **规程**：并发槽位及排队限流机制应作为 **`scheduler.go` 的通用扩展功能**，允许任何提供商使用，而非 Qwen 专属。
*   **设计逻辑**：
    *   在 `Auth` 结构体中支持配置可扩展的 `max_concurrency` 并发值。
    *   使用基于 `chan struct{}` 信号量进行 thread-safe 的并发计数管理。
    *   并发超限的请求进入全局优先级排队等待队列。在超时（30s）后才返回 `429 Too Many Requests`，保障网关吞吐量的稳定性。

---

## 🚀 PART II: CLIProxyAPI Operations & User Manual

### 1. 项目简介

**CLIProxyAPI** 是一个 AI 代理服务，将 Gemini CLI、ChatGPT Codex、Claude Code 等 CLI 工具包装为 OpenAI/Gemini/Claude/Codex Grok 兼容的 API 服务，支持：

- 多提供商统一接入（Gemini、Claude、Codex、Antigravity、Kimi、xAI、OpenAI 兼容等）
- 多账户负载均衡与配额管理
- OAuth 认证与 API Key 认证
- WebUI 管理面板
- 热重载配置
- Home 控制面板集群模式
- 多种存储后端（本地文件、PostgreSQL、S3、Git）

**主项目**: https://github.com/router-for-me/CLIProxyAPI  
**官方文档**: https://help.router-for.me/  
**WebUI 仓库**: https://github.com/router-for-me/Cli-Proxy-API-Management-Center

---

### 2. 快速开始

#### 2.1 最快上手（Docker）

```bash
# 1. 创建目录
mkdir -p ~/cliproxyapi/{config,auths,logs}
cd ~/cliproxyapi

# 2. 下载配置模板
wget https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/main/config.example.yaml -O config/config.yaml

# 3. 编辑配置
vim config/config.yaml

# 4. 创建 docker-compose.yml
cat > docker-compose.yml <<EOF
services:
  cli-proxy-api:
    image: eceasy/cli-proxy-api:latest
    container_name: cli-proxy-api
    ports:
      - "8317:8317"
      - "8085:8085"
    volumes:
      - ./config/config.yaml:/CLIProxyAPI/config.yaml
      - ./auths:/root/.cli-proxy-api
      - ./logs:/CLIProxyAPI/logs
    restart: unless-stopped
EOF

# 5. 启动
docker compose up -d
```

#### 2.2 基础配置示例

```yaml
port: 8317
auth-dir: "~/.cli-proxy-api"

# API 密钥（客户端访问用）
api-keys:
  - "your-api-key"

# 管理配置
remote-management:
  allow-remote: true
  secret-key: "your-management-key"
  disable-control-panel: false

# Gemini API Key
gemini-api-key:
  - api-key: "AIzaSy...your-key"

# 请求重试
request-retry: 3

# 配额超限处理
quota-exceeded:
  switch-project: true
  switch-preview-model: true
```

#### 2.3 启动后验证

```bash
# 检查服务状态
curl http://localhost:8317/

# 列出可用模型
curl -H "Authorization: Bearer your-api-key" http://localhost:8317/v1/models

# 访问 WebUI
# 浏览器打开 http://localhost:8317/management.html
```

---

### 3. 部署方式

#### 3.1 Docker 部署（推荐）

**docker-compose.yml 完整配置**：

```yaml
services:
  cli-proxy-api:
    image: ${CLI_PROXY_IMAGE:-eceasy/cli-proxy-api:latest}
    container_name: cli-proxy-api
    environment:
      DEPLOY: ${DEPLOY:-}
    ports:
      - "8317:8317"    # 主服务端口
      - "8085:8085"    # OAuth 回调端口
      - "1455:1455"    # Codex OAuth 回调
    volumes:
      - ${CLI_PROXY_CONFIG_PATH:-./config.yaml}:/CLIProxyAPI/config.yaml
      - ${CLI_PROXY_AUTH_PATH:-./auths}:/root/.cli-proxy-api
      - ${CLI_PROXY_LOG_PATH:-./logs}:/CLIProxyAPI/logs
    restart: unless-stopped
```

**环境变量（.env）**：
```bash
CLI_PROXY_IMAGE=eceasy/cli-proxy-api:latest
CLI_PROXY_CONFIG_PATH=./config.yaml
CLI_PROXY_AUTH_PATH=./auths
CLI_PROXY_LOG_PATH=./logs
```

#### 3.2 二进制部署

```bash
# 下载预编译二进制
wget https://github.com/router-for-me/CLIProxyAPI/releases/download/v1.x.x/CLIProxyAPI-linux-amd64.tar.gz
tar -xzf CLIProxyAPI-linux-amd64.tar.gz
cd CLIProxyAPI

# 准备配置
cp config.example.yaml config.yaml
vim config.yaml

# 运行
./CLIProxyAPI
```

**Systemd 服务**：
```ini
[Unit]
Description=CLIProxyAPI Service
After=network.target

[Service]
Type=simple
User=your-user
WorkingDirectory=/path/to/CLIProxyAPI
ExecStart=/path/to/CLIProxyAPI/CLIProxyAPI
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

#### 3.3 源码编译

```bash
git clone https://github.com/router-for-me/CLIProxyAPI.git
cd CLIProxyAPI
go mod download

# 带版本信息编译
VERSION=$(git describe --tags --always --dirty)
COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

go build \
  -ldflags="-s -w \
    -X 'main.Version=${VERSION}' \
    -X 'main.Commit=${COMMIT}' \
    -X 'main.BuildDate=${BUILD_DATE}'" \
  -o CLIProxyAPI ./cmd/server/
```

---

### 4. 配置详解

#### 4.1 基础配置

```yaml
port: 8317                    # 服务端口
auth-dir: "~/.cli-proxy-api"  # 认证文件目录
debug: false                  # 调试模式
request-log: false            # 请求日志
logging-to-file: true         # 日志写入文件
```

#### 4.2 远程管理配置

```yaml
remote-management:
  allow-remote: false          # 远程管理开关
  secret-key: ""               # 管理密钥（必须设置才能使用 WebUI）
  disable-control-panel: false # 是否禁用 WebUI
  panel-github-repository: "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
```

**重要**：
- `api-keys` 是客户端访问代理的密钥
- `secret-key` 是管理 WebUI 的密钥，两者不同
- 配置文件支持热重载，修改即时生效

#### 4.3 代理与重试

```yaml
proxy-url: "socks5://user:pass@proxy:1080/"  # 全局代理
request-retry: 3                              # 重试次数（403/408/500/502/503/504）
disable-cooling: false                        # 禁用冷却期
```

#### 4.4 配额管理

```yaml
quota-exceeded:
  switch-project: true        # 配额耗尽时自动切换账号
  switch-preview-model: true  # 切换到预览模型（Gemini 独占）
```

#### 4.5 使用统计

```yaml
usage-statistics-enabled: true  # 启用使用统计
```

---

### 5. AI 提供商接入

#### 5.1 Gemini API Key

```yaml
gemini-api-key:
  - api-key: "AIzaSy...01"
    base-url: "https://generativelanguage.googleapis.com"
    headers:
      X-Custom-Header: "custom-value"
    proxy-url: "socks5://proxy.example.com:1080"
```

#### 5.2 Claude API Key

```yaml
claude-api-key:
  - api-key: "sk-atSM..."
    base-url: "https://api.anthropic.com"  # 官方可不填
    proxy-url: "socks5://proxy.example.com:1080"
    models:
      - name: "claude-3-5-sonnet-20241022"
        alias: "claude-sonnet-latest"
```

#### 5.3 Codex API Key

```yaml
codex-api-key:
  - api-key: "sk-..."
    base-url: "https://api.openai.com"
```

#### 5.4 OpenAI 兼容提供商

```yaml
openai-compatibility:
  - name: "openrouter"
    base-url: "https://openrouter.ai/api/v1"
    api-key-entries:
      - api-key: "sk-or-v1-..."
        proxy-url: "socks5://proxy.example.com:1080"
    models:
      - name: "moonshotai/kimi-k2:free"
        alias: "kimi-k2"
```

#### 5.5 OAuth 认证

支持的 OAuth 提供商：
- **Gemini CLI** — Google OAuth
- **Claude Code** — Anthropic OAuth（PKCE）
- **Codex** — OpenAI OAuth
- **Antigravity** — 反重力
- **Kimi** — Moonshot OAuth

OAuth 认证通过 WebUI 或管理 API 发起，需要本地浏览器参与授权流程。

---

### 6. 管理与监控

#### 6.1 管理 API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v0/management/health` | GET | 健康检查 |
| `/v0/management/config` | GET | 获取配置 |
| `/v0/management/usage` | GET | 使用统计 |
| `/v0/management/usage/export` | GET | 导出统计数据 |
| `/v0/management/accounts` | GET | 账号状态 |

**请求头**：`X-Management-Key: your-secret-key`

#### 6.2 健康检查

```bash
# 基础检查
curl -f http://localhost:8317/

# 管理 API 检查
curl -H "X-Management-Key: your-key" http://localhost:8317/v0/management/health
```

**Docker 健康检查**：
```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8317/"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 40s
```

#### 6.3 日志管理

```yaml
logging-to-file: true
logs-max-total-size-mb: 1024
error-logs-max-files: 10
request-log: false  # 仅深度调试时启用
```

---

### 7. WebUI 管理面板

#### 7.1 快速启用

```yaml
remote-management:
  allow-remote: true
  secret-key: "MGT-your-secret-key"
  disable-control-panel: false
```

访问：`http://YOUR_SERVER_IP:8317/management.html`

#### 7.2 功能概览

- **仪表盘**：连接状态、版本信息、模型概览
- **基础设置**：调试、代理、重试、配额回退
- **API Keys**：管理代理密钥
- **AI 提供商**：Gemini/Codex/Claude/Vertex/OpenAI 兼容配置
- **认证文件**：上传/下载/删除凭据、OAuth 排除模型、模型别名
- **OAuth**：发起 OAuth 流程、轮询状态
- **配额管理**：各提供商配额上限与使用情况
- **配置文件**：配置文件在线编辑 config.yaml
- **日志**：增量拉取、搜索、下载错误日志
- **系统信息**：模型列表分组展示

#### 7.3 注意事项
- OAuth 认证仅支持 localhost 实例
- 远程服务器需 `allow-remote: true`
- 管理密钥与 API Keys 是两套独立密钥

---

### 8. Redis 用量队列

#### 8.1 概述

CLIProxyAPI 在 HTTP API 相同的 TCP 端口（默认 8317）上提供最小化的 Redis RESP 接口，用于拉取最近的单次请求用量记录。

#### 8.2 启用条件
- Management 必须启用（与 `/v0/management` 相同条件）
- 使用统计必须启用：`usage-statistics-enabled: true`

#### 8.3 认证
使用 Management Key 认证：
- `AUTH <password>`
- `AUTH <username> <password>`（忽略 username）

#### 8.4 支持的命令

| 命令 | 说明 |
|------|------|
| `AUTH` | 认证 |
| `LPOP <key> [count]` | 从左侧弹出记录 |
| `RPOP <key> [count]` | 从右侧弹出记录 |

#### 8.5 使用示例

```bash
# 弹出一条记录
redis-cli -h 127.0.0.1 -p 8317 -a "<MANAGEMENT_KEY>" --no-auth-warning --raw LPOP queue

# 最多弹出 50 条
redis-cli -h 127.0.0.1 -p 8317 -a "<MANAGEMENT_KEY>" --no-auth-warning --raw RPOP queue 50
```

---

### 9. 客户端配置

#### 9.1 Claude Code
```bash
export ANTHROPIC_BASE_URL=http://localhost:8317
export ANTHROPIC_API_KEY=your-api-key
claude
```

#### 9.2 Codex
```bash
export OPENAI_BASE_URL=http://localhost:8317/v1
export OPENAI_API_KEY=your-api-key
codex
```

#### 9.3 Gemini CLI
```bash
export GEMINI_API_BASE=http://localhost:8317
export GEMINI_API_KEY=your-api-key
gemini
```

---

### 10. Docker 部署

#### 10.1 使用 Docker Compose（推荐）

```yaml
services:
  cli-proxy-api:
    image: eceasy/cli-proxy-api:latest
    container_name: cli-proxy-api
    ports:
      - "8317:8317"
      - "8085:8085"
    volumes:
      - ./config.yaml:/CLIProxyAPI/config.yaml
      - ./auths:/root/.cli-proxy-api
      - ./logs:/CLIProxyAPI/logs
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8317/"]
      interval: 30s
      timeout: 10s
      retries: 3
```

---

### 11. 故障排查

#### 11.1 常见问题速查

| 问题 | 可能原因 | 解决方案 |
|------|----------|----------|
| 服务无法启动 | 端口占用/配置错误 | `lsof -i :8317`，检查配置语法 |
| 401 Unauthorized | API Key 错误 | 检查 `api-keys` 配置 |
| 404 Model Not Found | 模型被排除/前缀错误 | `curl /v1/models` 查看可用模型 |
| 429 Too Many Requests | 配额耗尽 | 启用 `switch-project: true` |
| WebUI 无法访问 | 管理未启用 | 设置 `secret-key` 和 `allow-remote: true` |
| OAuth 失败 | 令牌过期/目录权限 | 检查 `auth-dir` 权限，重新认证 |

---

### 12. 核心运维避坑、易错点与独家运维规范

#### 12.1 环境隔离与生产保护
*   **规范**：严格区分生产环境与开发/测试环境。
    *   **生产环境端口**：`8317`（CPA 后端）与 `18317`（cpa-manager 面板）。**绝对禁止**任何未经授权的直接操作与测试。
    *   **开发测试端口**：`9317` 与 `19317`。所有的代码调试、Provider 集成测试必须在开发端口进行。
*   **原则**：对生产环境“只读”分析，修改需经主人确认。

#### 12.2 配置文件保护（config.yaml Protection Guard）
*   **陷阱**：直接编辑或覆盖 `/home/skloxo/cpa-official/config/config.yaml` 极易导致缩进错误或字段丢失，从而使 CPA 无法拉起并被容器管理器驱逐（Eviction）。
*   **规范**：禁止手动或通过脚本（如 `sed`/`echo`/`cat`）直接修改主配置文件。任何配置变更必须：
    1.  在开发环境进行语法与功能验证。
    2.  提出具体的修改方案并等待主人确认。
    3.  使用 Manager 管理面板提供的配置编辑器进行修改，或由主人手动修改。

#### 12.3 WSL 时间同步漂移陷阱 (WSL Time Drift)
*   **规范**：若发现日志时间滞后或认证异常，应在 WSL 中立即运行以下命令强制同步宿主机硬件时钟：
    ```bash
    sudo hwclock -s
    ```

#### 12.4 Docker Alpine 挂载权限陷阱
*   **规范**：确保宿主机上所有凭据文件权限设置为 `0600`（所有者可读写），且所属目录权限为 `0700`。
    ```bash
    chmod 600 auths/*.json
    chmod 700 auths/
    ```

#### 12.5 Docker Named Volume SQLite 备份规范
*   **规范**：在进行任何可能影响存储卷的操作前，必须执行以下临时容器命令，安全地对 `cpa-manager-data` 命名卷中的 SQLite 数据库及其他数据进行打包备份：
    ```bash
    docker run --rm -v cpa-manager-data:/volume -v $(pwd):/backup alpine tar -czf /backup/cpa-manager-database-backup.tar.gz -C /volume .
    ```

#### 12.6 升级停机队列缓冲保护协议 (Downtime Queue Buffer Protection)
*   **规范**：在对后端进行升级或维护时，可在配置中将 `redis-usage-queue-retention-seconds` 临时增大（如 3600 秒），升级完毕、积压数据消费完成后，再恢复为 60 秒。

#### 12.7 Docker-to-WSL 环回连接陷阱
*   **规范**：混合网络拓扑下，如果 Docker 容器需要连接宿主机/WSL 上的 CPA 后端，需将 localhost/127.0.0.1 替换为 WSL 的虚拟网关地址（如 `172.23.0.1:8317`）。

#### 12.8 第二大版本 (v7.2.0-s.1) 极简无头浏览器与单文件部署规范
*   **无头浏览器依赖项**：若采用原生或主机进程部署，必须确保在宿主机/WSL 环境中预先安装所有必要的浏览器运行依赖库：
    ```bash
    npx playwright install --with-deps chromium
    ```
*   **Vite SingleFile 部署规则**：合流后的前端项目必须引入并启用 `vite-plugin-singlefile` 插件。前端打包与后端资源同步必须严格执行以下构建流水线：
    1. 在 `web/vite.config.ts` 中正确配置了 SingleFile 编译插件。
    2. 在 `web/` 子目录下执行依赖安装与打包构建：
       ```bash
       cd web && npm install && npm run build
       ```
    3. 将生成的单体 HTML 文件拷贝覆盖到后端静态资产目录：
       ```bash
        cp dist/index.html ../static/management.html
        ```

---

## 🌿 7. CPA2API Development Lifecycle SOP (标准操作规程)

This Standard Operating Procedure (SOP) defines the mandatory lifecycle stages that every developer or agent must follow when developing, deploying, and handing off changes for CPA2API and its management dashboard.

### 📋 Stage 1: Onboarding (开发准备与上下文加载)
*   **Load MEMORY.md**: Before modifying any code, the agent or developer must load the workspace `MEMORY.md` to acquire the context of past lessons, operational histories, and active parameters.
*   **Ingest SKILL.md**: Read this specification (`skills/cpa2api-skill/SKILL.md`) in its entirety to understand integration architecture, the 7-Point Alignment Checklist, and the Clean-Pristine Display Name Rule.
*   **Confirmation Thought Prefix**: In the initial thought block, the agent MUST prepend the confirmation message:
    > "Acknowledged. I have read the CPA Integration Skill Spec, and will strictly conform to the 7-Point checklist and the Clean-Pristine rule."

### 💻 Stage 2: Coding & Development (编码与本地测试)
*   **Staging/Dev Ports**: All code development, testing, and debugging must target port `9317` (Backend) and `19317` (Frontend Manager). Never touch production ports (`8317` / `18317`) during coding.
*   **Compile Verification**: Run command verification after changes to ensure compilation succeeds:
    ```bash
    go build -o test-output ./cmd/server && rm test-output
    ```
*   **Frontend Monorepo Build**: If updating frontend code, run the Vite single-file pipeline inside the `web/` directory:
    ```bash
    cd web && npm run build && cp dist/index.html ../static/management.html
    ```
*   **config.yaml Protection Guard**: Never modify the configuration file `config.yaml` directly. Changes to keys must be verified for syntax in the development environment and committed via the dashboard management console or approved by the user.

### 🧪 Stage 3: Dev Verification (开发验证)
*   **Recreate Container**: Rebuild and recreate the dev container (`cpa2api-dev` / `cpa2api-manager-dev`) to ensure clean state initialization.
*   **Port Verification**: Test local ports (`9317` / `19317`) using `curl` and verify response schema compliance.
*   **Card Rendering Check**: Inspect console outputs and test UI actions (e.g. model aliases, proxy hints) to confirm that elements are working as intended.

### 🚀 Stage 4: Prod Release & Deployment (生产发布与数据安全)
*   **Mount Static Files**: Ensure the single-file built HTML in `/static/management.html` is correctly routed by the production server binary.
*   **No-Cache Build**: Rebuild the production container without using cached layers to avoid outdated assets:
    ```bash
    docker compose build --no-cache cli-proxy-api
    ```
*   **Force-Recreate Container**: Redeploy the services with force-recreate to apply all environment variables and mount points:
    ```bash
    docker compose up --force-recreate -d cli-proxy-api
    ```
*   **Data Safety Checks**: Ensure persistent databases (`cpa-manager-data`) are backed up via temporary alpine tarball command before executing recreation.

### 🏷️ Stage 5: Tag & Release (版本归档与发布)
*   **Redaction & Desensitization Rule**:
    - **NEVER** commit `config.yaml`, `config-dev.yaml`, `auths/*.json`, `*.sqlite`, or `*.log` to Git.
    - Always redact/use fake placeholders (e.g. `sk-xxxx`, `eyJhbGciOi`) for secrets in documents and code comments.
    - Check `git status` and `git diff` before pushing to ensure no sensitive data is leaked.
*   **Tag Version**: Tag the validated commit with the defined custom patch format (e.g. `v7.2.2-s.4` for backend, `v1.3.3-s.1` for frontend).
*   **Push Tags**: Push tags to the upstream repository:
    ```bash
    git push origin <tag_name>
    ```
*   **Create GitHub Release**: Generate a release notes draft via GitHub CLI detailing changes, testing results, and migration path:
    ```bash
    gh release create <tag_name> --title "<tag_name> Release" --notes "Release Notes Here"
    ```

### 🤝 Stage 6: Handoff (项目交付与文档同步)
*   **Update Walkthrough**: Document the exact changes, E2E tests, and implementation results inside `walkthrough.md`.
*   **Sync Skills**: Synchronize the `skills/cpa2api-skill/` directory from the repository to the global openclaw workspace (`/home/skloxo/aho/openclaw/skills/cpa2api-skill/`) and the home config directory (`/home/skloxo/.openclaw/skills/cpa2api-skill/`).
*   **Write Handover Doc**: Create or update the `handover_documentation.md` containing active ports, volume mounts, release hashes, and a summary of completed deliverables.
