# CPA2API 向后兼容性报告

生成时间: 2026-06-12

## 1. 配置兼容性 (config.yaml)

### 状态: ✅ 向后兼容

**检查项:**

- **新增字段默认值**: 所有新增字段都有默认值，在 `LoadConfigOptional()` 函数中设置（config.go:646-657）
- **删除字段处理**: 使用 `yaml.Unmarshal` 时，未知字段会被忽略而非报错
- **字段类型变更**: 字段类型保持稳定，变更通过 `omitempty` 标签处理

**关键发现:**

1. 默认值在反序列化前设置，确保缺失字段使用默认值
2. 配置结构使用 `yaml:",inline"` 嵌套，保持灵活性
3. 旧版迁移代码已禁用（config.go:666-680），避免启动时修改配置文件
4. 所有配置项都有合理的默认值，新版本启动不会失败

**示例:**
```yaml
# 旧配置文件可以省略新字段，仍能正常加载
host: ""
port: 8317
# 新字段如 commercial-mode, logging-to-file 等会使用默认值
```

## 2. 凭证兼容性 (auths/*.json)

### 状态: ✅ 向后兼容

**检查项:**

- **旧格式凭证读取**: 每个 provider 的 token 存储结构保持稳定
- **新格式凭证兼容**: 新字段使用 `omitempty` 标签，旧代码可忽略新字段
- **类型字段**: 使用 `type` 字段区分不同 provider（如 `"gemini"`, `"claude"`）

**关键发现:**

1. `GeminiTokenStorage` 结构包含 `Token`, `ProjectID`, `Email` 等核心字段
2. 新字段如 `Metadata` 使用 `json:"-"` 标签，不影响序列化
3. JSON 结构向后兼容，旧版本可以读取新格式文件
4. 没有发现需要迁移的破坏性变更

**示例结构:**
```json
{
  "token": {...},
  "project_id": "xxx",
  "email": "user@example.com",
  "type": "gemini"
}
```

## 3. 数据库兼容性 (usage.sqlite)

### 状态: ✅ 向后兼容

**检查项:**

- **Schema 迁移处理**: 使用 `CREATE TABLE IF NOT EXISTS` 创建表
- **新增表/列**: 使用 `IF NOT EXISTS` 和 `ALTER TABLE ... ADD COLUMN`
- **旧数据读取**: 新代码可以读取旧格式数据

**关键发现:**

1. 初始化语句使用 `CREATE TABLE IF NOT EXISTS`（store.go:193-224）
2. `ensureUsageEventSnapshotColumns()` 函数安全添加新列（store.go:269-319）
3. 新列添加时使用 `ALTER TABLE ... ADD COLUMN`，不会删除或修改现有数据
4. 索引创建使用 `CREATE INDEX IF NOT EXISTS`
5. 支持从备份文件自动恢复数据库

**迁移示例:**
```sql
-- 安全添加新列
ALTER TABLE usage_events ADD COLUMN auth_project_id_snapshot text;
ALTER TABLE usage_events ADD COLUMN requested_model text;
ALTER TABLE usage_events ADD COLUMN resolved_model text;
```

## 4. 日志格式兼容性

### 状态: ✅ 向后兼容

**检查项:**

- **日志格式变更**: 格式保持稳定：`[timestamp] [request_id] [level] [file:line] message`
- **日志级别变更**: 级别名称标准化（`warning` → `warn`）
- **字段顺序**: 使用 `logFieldOrder` 定义字段显示顺序

**关键发现:**

1. 日志格式在 `LogFormatter.Format()` 中定义（global_logger.go:36-81）
2. 格式结构：`[时间戳] [请求ID] [级别] [文件:行号] 消息 [字段]`
3. 字段按固定顺序显示，确保可解析性
4. 没有发现破坏性格式变更

**格式示例:**
```
[2025-12-23 20:14:04] [a1b2c3d4] [debug ] [manager.go:524] Use API key sk-9...0RHO for model gpt-5.2 provider=claude
```

## 5. 已知不兼容项

### 无重大不兼容项

**注意事项:**

1. **配置迁移**: 旧版迁移代码已禁用，需要手动更新配置文件中的已弃用字段
2. **日志级别**: `warning` 级别在内部转换为 `warn`，但对外显示一致
3. **数据库列**: 新列添加后，旧版本代码可能无法读取这些列（但不会报错）

## 6. 兼容性保证

### 升级路径

- **从旧版本升级**: ✅ 支持，无需手动迁移
- **从新版本降级**: ⚠️ 部分支持（新字段会被忽略）
- **数据迁移**: ✅ 自动处理，无需手动操作

### 建议

1. 升级前备份 `auths/` 目录和 `usage.sqlite` 文件
2. 检查 `config.yaml` 中是否有已弃用的配置项
3. 监控日志输出格式变化（如有自定义日志解析）

## 7. 测试建议

```bash
# 验证配置兼容性
go test ./internal/config/...

# 验证数据库兼容性
go test ./internal/usageservice/...

# 验证日志兼容性
go test ./internal/logging/...
```

## 结论

CPA2API 项目在向后兼容性方面做得很好：
- 配置系统支持平滑升级
- 凭证格式保持稳定
- 数据库 schema 使用渐进式迁移
- 日志格式保持可解析性

**整体兼容性评级: A (优秀)**
