#!/usr/bin/env node
/**
 * CPA (cpa2api) MCP Server
 * 统一运维管理：状态/健康/配置/升级
 * 版本: 2.0.0 | 更新: 2026-05-22
 * 变更: 合并 auto-upgrade.sh 脚本到 MCP 工具，统一维护
 */

const http = require('http');
const https = require('https');
const { execSync } = require('child_process');
const fs = require('fs');

const CPA_BASE = process.env.CPA_BASE_URL || 'http://localhost:8317';
const CPA_MGMT = process.env.CPA_MGMT_URL || 'http://localhost:18317';
const CPA_KEY = process.env.CPA_API_KEY || '';
const CPA_MGR_REPO = 'seakee/cpa-manager';

// ============ MCP 工具定义 ============

const TOOLS = [
  {
    name: 'cpa_status',
    description: '检查 CPA 服务状态（健康检查、运行时间、版本）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_models',
    description: '列出 CPA 可用的所有模型',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_usage',
    description: '获取 CPA 使用统计（请求数、token 用量、按模型统计）',
    inputSchema: {
      type: 'object',
      properties: {
        date: { type: 'string', description: '日期 (YYYY-MM-DD)，默认今天' }
      }
    }
  },
  {
    name: 'cpa_health',
    description: '执行 CPA 完整健康检查（服务、API、模型、存储、Docker）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_config',
    description: '获取 CPA 当前配置（提供商、模型映射，敏感信息脱敏）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_upgrade_check',
    description: '检查 CPA 和 cpa-manager 是否有新版本可用',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_upgrade',
    description: '执行 CPA 和/或 cpa-manager 升级（含备份、健康检查、自动回滚）',
    inputSchema: {
      type: 'object',
      properties: {
        component: {
          type: 'string',
          enum: ['cpa', 'cpa-manager', 'all'],
          description: '升级目标，默认 all'
        },
        dry_run: {
          type: 'boolean',
          description: '仅检查不执行，默认 false'
        }
      }
    }
  },
  {
    name: 'cpa_validate_config',
    description: '验证 CPA 配置文件的 YAML 语法和结构（修改前必用）',
    inputSchema: {
      type: 'object',
      properties: {
        config_path: {
          type: 'string',
          description: '配置文件路径（可选，默认从 CPA 容器读取）'
        }
      }
    }
  },
  {
    name: 'cpa_containers',
    description: '列出所有 CPA 相关容器的状态',
    inputSchema: { type: 'object', properties: {} }
  }
];

// ============ 工具函数 ============

function httpRequest(url, options = {}) {
  return new Promise((resolve, reject) => {
    const mod = url.startsWith('https') ? https : http;
    const req = mod.request(url, { timeout: 10000, ...options }, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try { resolve(JSON.parse(data)); }
        catch { resolve(data); }
      });
    });
    req.on('error', reject);
    req.end();
  });
}

function dockerExec(cmd, timeout = 10000) {
  try {
    return execSync(cmd, { encoding: 'utf-8', timeout }).trim();
  } catch (e) {
    return null;
  }
}

function log(msg) {
  const ts = new Date().toISOString().replace('T', ' ').substring(0, 19);
  process.stderr.write(`[${ts}] ${msg}\n`);
}

// ============ 版本检测 ============

function getCpaLocalVersion() {
  const raw = dockerExec('docker exec CPA /cpa2api/cpa2api --version 2>/dev/null | head -1');
  if (!raw) return null;
  const m = raw.match(/Version:\s*(v[\d.]+)/);
  return m ? m[1] : raw;
}

// ============ 获取远程最新版本 ============
function getCpaRemoteVersion() {
  const raw = dockerExec('docker run --rm eceasy/cli-proxy-api:latest /cpa2api/cpa2api --version 2>/dev/null | head -1');
  if (!raw) return null;
  const m = raw.match(/Version:\s*(v[\d.]+)/);
  return m ? m[1] : raw;
}

function getMgrLocalVersion() {
  const binary = dockerExec("docker inspect cpa-manager --format '{{range .HostConfig.Binds}}{{println .}}{{end}}' 2>/dev/null | grep cpa-manager | grep -v data | cut -d: -f1");
  if (!binary || !fs.existsSync(binary)) return null;
  const raw = dockerExec(`go version -m "${binary}" 2>/dev/null | grep cpa-manager | grep -oP 'v?[\\d.]+' | head -1`);
  return raw ? raw.replace(/^v/, '') : null;
}

function getMgrRemoteVersion() {
  // 优先 gh cli（绕过 GitHub API 限流）
  if (dockerExec('which gh 2>/dev/null')) {
    const raw = dockerExec(`gh release view --repo "${CPA_MGR_REPO}" --json tagName 2>/dev/null`);
    if (raw) {
      try {
        const d = JSON.parse(raw);
        return d.tagName?.replace(/^v/, '') || null;
      } catch {}
    }
  }
  const raw = dockerExec(`curl -sf "https://api.github.com/repos/${CPA_MGR_REPO}/releases/latest" 2>/dev/null`);
  if (raw) {
    try {
      return JSON.parse(raw).tag_name?.replace(/^v/, '') || null;
    } catch {}
  }
  return null;
}

// ============ 健康检查 ============

function healthCheck(port, retries = 3) {
  for (let i = 0; i < retries; i++) {
    if (dockerExec(`curl -sf --max-time 5 http://localhost:${port}/ > /dev/null 2>&1 && echo OK`)) return true;
    if (i < retries - 1) dockerExec('sleep 2');
  }
  return false;
}

function cpaApiCheck() {
  return !!dockerExec(`curl -sf --max-time 5 -H "Authorization: Bearer ${CPA_KEY}" http://localhost:8317/v1/models > /dev/null 2>&1 && echo OK`);
}

// ============ CPA 升级 ============

function upgradeCpa(dryRun) {
  const result = { component: 'cpa', steps: [] };
  const localVer = getCpaLocalVersion();
  const remoteVer = getCpaRemoteVersion();

  result.local_version = localVer || 'unknown';
  result.remote_version = remoteVer || 'unknown';

  if (!remoteVer) {
    result.error = '无法获取远程版本';
    return result;
  }

  if (localVer === remoteVer) {
    result.status = 'already_latest';
    result.message = `✅ CPA 已是最新版本 (${localVer})`;
    return result;
  }

  result.needs_upgrade = true;
  result.message = `⬆️ CPA 可升级: ${localVer} → ${remoteVer}`;

  if (dryRun) return result;

  // 拉取新镜像
  log('CPA: 拉取新镜像...');
  result.steps.push({ step: 'pull', status: dockerExec('docker pull eceasy/cli-proxy-api:latest 2>&1 | tail -3', 60000) ? 'ok' : 'failed' });

  // 再次检查版本
  const postPullVer = getCpaRemoteVersion();
  if (localVer === postPullVer) {
    result.status = 'no_change';
    result.message = '拉取后版本未变化';
    return result;
  }

  // 备份
  const rollbackTag = `pre-auto-upgrade-${Date.now()}`;
  log(`CPA: 备份镜像 ${rollbackTag}...`);
  dockerExec(`docker tag eceasy/cli-proxy-api:latest eceasy/cli-proxy-api:${rollbackTag}`);
  result.rollback_tag = rollbackTag;

  // 获取容器参数
  const binds = dockerExec("docker inspect CPA --format '{{range .HostConfig.Binds}}-v {{.}} {{end}}'") || '';
  const envs = (dockerExec("docker inspect CPA --format '{{range .Config.Env}}-e {{.}} {{end}}'") || '').replace(/-e PATH=[^ ]+/g, '');

  // 停止旧容器
  log('CPA: 停止旧容器...');
  dockerExec('docker stop CPA 2>/dev/null; docker rm CPA 2>/dev/null');

  // 启动新容器
  log('CPA: 启动新容器...');
  const runCmd = `docker run -d --name CPA --restart=unless-stopped --network=host ${envs} ${binds} eceasy/cli-proxy-api:latest`;
  dockerExec(runCmd);
  result.steps.push({ step: 'restart', status: 'ok' });

  // 健康检查
  log('CPA: 等待启动...');
  dockerExec('sleep 5');
  const healthy = healthCheck(8317) && cpaApiCheck();

  if (healthy) {
    result.status = 'success';
    result.message = `✅ CPA 升级成功: ${localVer} → ${postPullVer}`;
    result.steps.push({ step: 'health_check', status: 'ok' });
  } else {
    // 回滚
    log('CPA: 健康检查失败，回滚...');
    dockerExec('docker stop CPA 2>/dev/null; docker rm CPA 2>/dev/null');
    dockerExec(`docker run -d --name CPA --restart=unless-stopped --network=host ${envs} ${binds} eceasy/cli-proxy-api:${rollbackTag}`);
    dockerExec('sleep 5');
    const rolledBack = healthCheck(8317);
    result.status = rolledBack ? 'rolled_back' : 'rollback_failed';
    result.message = rolledBack ? '⚠️ CPA 升级失败，已回滚' : '❌ CPA 升级失败且回滚失败！';
    result.steps.push({ step: 'rollback', status: rolledBack ? 'ok' : 'failed' });
  }

  return result;
}

// ============ cpa-manager 升级 ============

function upgradeMgr(dryRun) {
  const result = { component: 'cpa-manager', steps: [] };
  const localVer = getMgrLocalVersion();
  const remoteVer = getMgrRemoteVersion();

  result.local_version = localVer || 'unknown';
  result.remote_version = remoteVer || 'unknown';

  if (!remoteVer) {
    result.error = '无法获取远程版本';
    return result;
  }

  if (localVer === remoteVer) {
    result.status = 'already_latest';
    result.message = `✅ cpa-manager 已是最新版本 (${localVer})`;
    return result;
  }

  result.needs_upgrade = true;
  result.message = `⬆️ cpa-manager 可升级: ${localVer} → ${remoteVer}`;

  if (dryRun) return result;

  // 备份当前二进制
  const currentBinary = dockerExec("docker inspect cpa-manager --format '{{range .HostConfig.Binds}}{{println .}}{{end}}' 2>/dev/null | grep cpa-manager | grep -v data | cut -d: -f1");
  if (currentBinary && fs.existsSync(currentBinary)) {
    const backup = `${currentBinary}.bak`;
    log(`cpa-manager: 备份二进制 → ${backup}`);
    fs.copyFileSync(currentBinary, backup);
    result.backup = backup;
  }

  // 下载新版本
  const tmpDir = `/tmp/cpa-mgr-upgrade-${Date.now()}`;
  fs.mkdirSync(tmpDir, { recursive: true });
  log(`cpa-manager: 下载 v${remoteVer}...`);

  let downloaded = false;
  // 优先 gh cli
  if (dockerExec('which gh 2>/dev/null')) {
    downloaded = !!dockerExec(`gh release download v${remoteVer} --repo "${CPA_MGR_REPO}" --pattern "*linux_amd64*" --dir "${tmpDir}" 2>&1`, 60000);
  }
  // fallback curl
  if (!downloaded) {
    const url = `https://github.com/${CPA_MGR_REPO}/releases/download/v${remoteVer}/cpa-manager_v${remoteVer}_linux_amd64.tar.gz`;
    downloaded = !!dockerExec(`curl -L -o "${tmpDir}/cpa-manager.tar.gz" "${url}" 2>&1`, 60000);
    if (downloaded) dockerExec(`cd "${tmpDir}" && tar xzf cpa-manager.tar.gz 2>/dev/null`);
  }

  if (!downloaded) {
    result.error = '下载失败';
    return result;
  }

  // 找到二进制
  const newBinary = dockerExec(`find "${tmpDir}" -name "cpa-manager" -type f ! -name "*.tar.gz" | head -1`);
  if (!newBinary) {
    result.error = '解压后未找到二进制';
    return result;
  }

  // 部署
  const newPath = `/home/skloxo/cpa-manager-v${remoteVer}`;
  fs.copyFileSync(newBinary, newPath);
  fs.chmodSync(newPath, 0o755);
  log(`cpa-manager: 部署到 ${newPath}`);
  result.steps.push({ step: 'download', status: 'ok', path: newPath });

  // 获取管理密钥
  const mgmtKey = dockerExec("grep secret-key /home/skloxo/cpa-official/config/config.yaml | sed 's/.*secret-key: *//' | tr -d \"'\\\"\"") || '';

  // 停止旧容器
  log('cpa-manager: 停止旧容器...');
  dockerExec('docker stop cpa-manager 2>/dev/null; docker rm cpa-manager 2>/dev/null');

  // 启动新容器
  log('cpa-manager: 启动新容器...');
  const runCmd = `docker run -d --name cpa-manager --restart=unless-stopped --network=host \
    --entrypoint cpa-manager \
    -v "${newPath}:/usr/local/bin/cpa-manager:ro" \
    -v "/home/skloxo/cpa-manager-data:/data" \
    -e CPA_BASE_URL=http://127.0.0.1:8317 \
    -e CPA_MANAGEMENT_KEY="${mgmtKey}" \
    -e HTTP_ADDR=0.0.0.0:18317 \
    -e USAGE_DB_PATH=/data/usage.sqlite \
    -e USAGE_COLLECTOR_MODE=auto \
    -e USAGE_DATA_DIR=/data \
    -e TZ=Asia/Shanghai \
    eceasy/cli-proxy-api:latest`;
  dockerExec(runCmd);
  result.steps.push({ step: 'restart', status: 'ok' });

  // 健康检查
  log('cpa-manager: 等待启动...');
  dockerExec('sleep 5');
  const healthy = healthCheck(18317);

  if (healthy) {
    result.status = 'success';
    result.message = `✅ cpa-manager 升级成功: ${localVer} → ${remoteVer}`;
    result.steps.push({ step: 'health_check', status: 'ok' });
    // 清理
    dockerExec(`rm -rf "${tmpDir}"`);
  } else {
    // 回滚
    log('cpa-manager: 健康检查失败，回滚...');
    if (result.backup && fs.existsSync(result.backup)) {
      fs.copyFileSync(result.backup, currentBinary);
      dockerExec('docker stop cpa-manager 2>/dev/null; docker rm cpa-manager 2>/dev/null');
      dockerExec(runCmd.replace(newPath, currentBinary));
      dockerExec('sleep 5');
      const rolledBack = healthCheck(18317);
      result.status = rolledBack ? 'rolled_back' : 'rollback_failed';
      result.message = rolledBack ? '⚠️ cpa-manager 升级失败，已回滚' : '❌ cpa-manager 升级失败且回滚失败！';
    } else {
      result.status = 'rollback_failed';
      result.message = '❌ cpa-manager 升级失败且无备份可回滚！';
    }
  }

  dockerExec(`rm -rf "${tmpDir}"`);
  return result;
}

// ============ 工具分发 ============

async function handleTool(name, args) {
  switch (name) {
    case 'cpa_status': {
      const checks = {};
      try {
        await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } });
        checks.cpa_backend = 'healthy';
      } catch (e) { checks.cpa_backend = `error: ${e.message}`; }
      try { await httpRequest(`${CPA_MGMT}/`); checks.cpa_manager = 'healthy'; }
      catch (e) { checks.cpa_manager = `error: ${e.message}`; }
      checks.cpa_version = getCpaLocalVersion() || 'unknown';
      checks.cpa_manager_version = getMgrLocalVersion() || 'unknown';
      checks.cpa_container = dockerExec('docker ps --filter name=CPA --format "{{.Status}}"') || 'not found';
      checks.cpa_manager_container = dockerExec('docker ps --filter name=cpa-manager --format "{{.Status}}"') || 'not found';
      return checks;
    }

    case 'cpa_models': {
      try {
        const res = await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } });
        return (res?.data || []).map(m => m.id).sort();
      } catch (e) { return { error: e.message }; }
    }

    case 'cpa_usage': {
      const date = args?.date || new Date().toISOString().split('T')[0];
      try {
        const result = execSync(
          `sqlite3 /home/skloxo/cpa-manager-data/usage.sqlite "SELECT model, COUNT(*) as requests, SUM(input_tokens) as input, SUM(output_tokens) as output, SUM(total_tokens) as total FROM usage_events WHERE date(timestamp) = '${date}' GROUP BY model ORDER BY requests DESC;" 2>/dev/null`,
          { encoding: 'utf-8', timeout: 5000 }
        ).trim();
        return { date, data: result || 'No data' };
      } catch (e) { return { date, error: e.message }; }
    }

    case 'cpa_health': {
      const checks = [];
      try { await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } }); checks.push({ name: 'CPA 后端 API', status: 'ok', port: 8317 }); }
      catch { checks.push({ name: 'CPA 后端 API', status: 'error', port: 8317 }); }
      try { await httpRequest(`${CPA_MGMT}/`); checks.push({ name: 'cpa-manager 管理面板', status: 'ok', port: 18317 }); }
      catch { checks.push({ name: 'cpa-manager 管理面板', status: 'error', port: 18317 }); }
      const cpaDocker = dockerExec('docker ps --filter name=CPA --format "{{.Names}}: {{.Status}}"');
      const mgrDocker = dockerExec('docker ps --filter name=cpa-manager --format "{{.Names}}: {{.Status}}"');
      checks.push({ name: 'CPA 容器', status: cpaDocker?.includes('Up') ? 'ok' : 'error', detail: cpaDocker });
      checks.push({ name: 'cpa-manager 容器', status: mgrDocker?.includes('Up') ? 'ok' : 'error', detail: mgrDocker });
      checks.push({ name: 'CPA 版本', version: getCpaLocalVersion() || 'unknown' });
      checks.push({ name: 'cpa-manager 版本', version: getMgrLocalVersion() || 'unknown' });
      try { const res = await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } }); checks.push({ name: '可用模型数', count: res?.data?.length || 0 }); } catch {}
      return checks;
    }

    case 'cpa_config': {
      try {
        const config = dockerExec("docker exec CPA cat /cpa2api/config.yaml 2>/dev/null | sed -E 's/(api-key|secret-key|password|proxy-url):.*/\\1: ***REDACTED***/gi' | head -60");
        return { config: config || '无法读取配置' };
      } catch (e) { return { error: e.message }; }
    }

    case 'cpa_upgrade_check': {
      const result = { cpa: {}, cpa_manager: {} };
      const cpaLocal = getCpaLocalVersion();
      const cpaRemote = getCpaRemoteVersion();
      result.cpa = {
        local_version: cpaLocal || 'unknown',
        remote_version: cpaRemote || 'unknown',
        needs_upgrade: cpaLocal !== cpaRemote,
        upgrade_available: cpaLocal !== cpaRemote ? `✅ ${cpaLocal} → ${cpaRemote}` : '✅ 已是最新'
      };
      const mgrLocal = getMgrLocalVersion();
      const mgrRemote = getMgrRemoteVersion();
      result.cpa_manager = {
        local_version: mgrLocal || 'unknown',
        remote_version: mgrRemote || 'unknown',
        needs_upgrade: mgrLocal !== mgrRemote,
        upgrade_available: mgrLocal !== mgrRemote ? `✅ ${mgrLocal} → ${mgrRemote}` : '✅ 已是最新',
        source: `github.com/${CPA_MGR_REPO}`
      };
      return result;
    }

    case 'cpa_upgrade': {
      const component = args?.component || 'all';
      const dryRun = args?.dry_run || false;
      const results = [];

      if (component === 'cpa' || component === 'all') {
        results.push(upgradeCpa(dryRun));
      }
      if (component === 'cpa-manager' || component === 'all') {
        results.push(upgradeMgr(dryRun));
      }

      // 汇总最终状态
      const finalStatus = {
        mode: dryRun ? 'dry-run' : 'execute',
        results,
        final_state: {
          cpa: { version: getCpaLocalVersion(), status: dockerExec('docker ps --filter name=CPA --format "{{.Status}}"') },
          cpa_manager: { version: getMgrLocalVersion(), status: dockerExec('docker ps --filter name=cpa-manager --format "{{.Status}}"') }
        }
      };
      return finalStatus;
    }

    case 'cpa_validate_config': {
      try {
        const configPath = args?.config_path || '/tmp/cpa-config-check.yaml';
        if (!args?.config_path) {
          if (!dockerExec(`docker cp CPA:/cpa2api/config.yaml ${configPath}`)) return { valid: false, error: '无法从容器读取配置' };
        }
        const result = execSync(`python3 -c "import yaml; yaml.safe_load(open('${configPath}')); print('YAML_OK')" 2>&1`, { encoding: 'utf-8', timeout: 5000 }).trim();
        if (result !== 'YAML_OK') return { valid: false, error: `YAML 语法错误: ${result}` };
        const issues = [];
        const content = fs.readFileSync(configPath, 'utf8');
        if (content.includes('openai-compatibility:')) {
          if (!content.match(/openai-compatibility:\s*\n(\s+-.*)/)) issues.push('openai-compatibility 必须 be 数组格式');
        }
        return { valid: issues.length === 0, issues, message: issues.length === 0 ? '✅ 配置验证通过' : '❌ 配置存在问题' };
      } catch (e) { return { valid: false, error: e.message }; }
    }

    case 'cpa_containers': {
      try {
        const raw = dockerExec('docker ps -a --format "{{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}" | grep -iE "cpa|cpaplus"');
        return (raw || '').split('\n').filter(Boolean).map(l => {
          const [name, image, ...rest] = l.split('\t');
          return { name, image, status: rest.join(' ') };
        });
      } catch (e) { return { error: e.message }; }
    }

    default:
      return { error: `Unknown tool: ${name}` };
  }
}

// ============ MCP JSON-RPC (streaming stdio transport) ============

const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin, terminal: false });

rl.on('line', async (line) => {
  const trimmed = line.trim();
  if (!trimmed) return;
  try {
    const msg = JSON.parse(trimmed);
    let result;

    switch (msg.method) {
      case 'initialize':
        result = { protocolVersion: '2024-11-05', capabilities: { tools: {} }, serverInfo: { name: 'cpa2a', version: '2.0.0' } };
        break;
      case 'tools/list':
        result = { tools: TOOLS };
        break;
      case 'tools/call':
        result = { content: [{ type: 'text', text: JSON.stringify(await handleTool(msg.params.name, msg.params.arguments), null, 2) }] };
        break;
      case 'notifications/initialized':
        return; // no response needed
      default:
        return; // ignore unknown methods
    }
    process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result }) + '\n');
  } catch (e) {
    // silently ignore parse errors
  }
});
