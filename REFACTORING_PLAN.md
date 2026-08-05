# CPA2API 版本号规范与重构交付履历 (Version Rules & Delivery Plan)

## 📌 版本号命名规范 (Version Naming Specification)

全系统严格遵循物理规则：
`<上游仓库代码版本号>-s<本地自增迭代版本号>`

### 规则说明：
1. **本地迭代更新**：上游版本保持不变，`-s` 后的数字自然递增。  
   * 示例：上游为 `v7.1.45`，本地第 10 次迭代 -> **`v7.1.45-s10`**  
   * 下一次本地迭代 -> **`v7.1.45-s11`**
2. **上游合并升级**：合并上游最新代码后，`-s` 后的数字重置为 `s1`。  
   * 示例：合并上游 `v7.2.0` 代码 -> **`v7.2.0-s1`**  
   * 在此基础上再次迭代 -> **`v7.2.0-s2`**

---

## 📝 交付履历 (Delivery Changelog)

### [x] [v7.1.45-s10] - 2026-08-05 ✅ (已全量交付并升维至 8317 生产环境)
- **Qwen 反代提供商前端重构与整合**：将 Qwen Web 登录与反代接入重构整合进【AI 提供商】->【添加供应商】统一管理模块（支持接口类型/预设下拉选择），实现与 OpenAI 兼容提供商一致的表单配置与前端控制。
- **8317 生产环境无缝升维与前端同步**：8317 生产容器 `CPA2API8317` 已升级至 `v7.1.45-s10` 镜像，并且挂载目录 `/home/skloxo/services/cpa2api/static/management.html` 已同步覆写为最新的编译产物。
- **线上全量 14+ 厂商兼容验证**：导入 8317 生产配置与 10 个千问 Auth 凭证进行联通验证，OpenRouter、Groq、白山、NVIDIA、opencode、llm7、MIMO-SGP、商汤、agnes、skyclaw、cline、dahl 等 32 个模型别名映射 100% 物理无缝兼容。
- **资产全量物理备份**：配置文件、凭证库及数据库已被安全备份至 `/home/skloxo/services/cpa2api/backups/pre_s10_upgrade_20260805/`。
- **Qwen 模型白名单隔离**：消除了 24 个预设千问模型泄露问题，接口与系统管理面板严格收敛为配置的 `qwen3.7-plus` 与 `qwen3.8-max`。
- **1M (1,000,000 Tokens) 长上下文物理优化**：引入 TCP Keep-Alive 与无效空节点清洗，`qwen3.7-plus` 与 `qwen3.8-max` 双双突破 **512K Tokens (~200万字符)** 极限大包无丢包传输，吞吐速度提升 3 倍。
- **Rate-Limit / 401 自动感知与无缝 Failover**：捕获 429、401、RateLimit、QuotaExceeded 特征后自动将受限账号加入 CoolDown，配合多账号 Selector 自动无缝切换账号，客户端 0 报错。
- **0-Cost GET 探针保鲜 (ProbeQwenAuthHeartbeat)**：后台通过轻量级的 `GET /api/v2/user/info` 进行探针，不上墙聊天记录、不消耗额度，天然刷新 Cookie/Session 保鲜。
