#!/usr/bin/env node
/**
 * CPA (cpa2api) MCP Server
 * 统一运维管理：状态/健康/配置/升级
 * 版本: 2.1.0 | 更新: 2026-06-02
 * 变更: 适配独立部署目录 /home/skloxo/services/cpa2api/
 *       容器名统一为 cli-proxy-api（由 docker compose 管理）
 *       升级逻辑改用 docker compose pull && up -d
 */

const http = require('http');
const https = require('https');
const { execSync } = require('child_process');
const fs = require('fs');

const CPA_BASE       = process.env.CPA_BASE_URL  || 'http://localhost:8317';
const CPA_KEY        = process.env.CPA_API_KEY   || '';
const CPA_DEPLOY_DIR = process.env.CPA_DEPLOY_DIR || '/home/skloxo/services/cpa2api';
const CONTAINER_NAME = 'cli-proxy-api';

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
    description: '执行 CPA 完整健康检查（服务、API、模型、Docker 容器）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_config',
    description: '获取 CPA 当前配置（提供商、模型映射，敏感信息脱敏）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_upgrade_check',
    description: '检查 CPA 是否有新版本可用（对比 .env 版本与 DockerHub latest）',
    inputSchema: { type: 'object', properties: {} }
  },
  {
    name: 'cpa_upgrade',
    description: '执行 CPA 升级：修改 .env 版本号，docker compose pull && up -d',
    inputSchema: {
      type: 'object',
      properties: {
        version: {
          type: 'string',
          description: '目标版本号，例如 v7.2.3。不填则拉取 latest'
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
          description: `配置文件路径，默认 ${CPA_DEPLOY_DIR}/config.yaml`
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

function sh(cmd, timeout = 10000) {
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

/** 读取部署目录 .env 里的 CPA_VERSION */
function getLocalVersion() {
  const envFile = `${CPA_DEPLOY_DIR}/.env`;
  if (!fs.existsSync(envFile)) return null;
  const m = fs.readFileSync(envFile, 'utf8').match(/^CPA_VERSION=(.+)$/m);
  return m ? m[1].trim() : null;
}

/** 从运行中的容器读取实际版本 */
function getContainerVersion() {
  const raw = sh(`docker exec ${CONTAINER_NAME} /app/cpa2api --version 2>/dev/null | head -1`);
  if (!raw) return null;
  const m = raw.match(/Version:\s*(v[\d.\w-]+)/);
  return m ? m[1] : raw;
}

/** 从 DockerHub 获取 latest 镜像版本 */
function getRemoteVersion() {
  const raw = sh('docker run --rm --entrypoint /app/cpa2api eceasy/cli-proxy-api:latest --version 2>/dev/null | head -1', 30000);
  if (!raw) return null;
  const m = raw.match(/Version:\s*(v[\d.\w-]+)/);
  return m ? m[1] : raw;
}

// ============ 健康检查 ============

function healthCheck(port, retries = 3) {
  for (let i = 0; i < retries; i++) {
    if (sh(`curl -sf --max-time 5 http://localhost:${port}/healthz > /dev/null 2>&1 && echo OK`)) return true;
    if (i < retries - 1) sh('sleep 2');
  }
  return false;
}

// ============ CPA 升级（使用 docker compose） ============

function upgradeCpa(targetVersion, dryRun) {
  const result = { steps: [] };
  const localVer  = getLocalVersion();
  const actualVer = getContainerVersion();

  result.env_version       = localVer  || 'unknown';
  result.container_version = actualVer || 'unknown';
  result.target_version    = targetVersion || 'latest';

  if (dryRun) {
    result.mode    = 'dry-run';
    result.message = `将执行: cd ${CPA_DEPLOY_DIR} && sed -i CPA_VERSION=${result.target_version} .env && docker compose pull && docker compose up -d`;
    return result;
  }

  // 1. 修改 .env 版本号
  if (targetVersion) {
    log(`CPA: 更新 .env CPA_VERSION → ${targetVersion}`);
    const envPath = `${CPA_DEPLOY_DIR}/.env`;
    let envContent = fs.readFileSync(envPath, 'utf8');
    envContent = envContent.replace(/^CPA_VERSION=.*/m, `CPA_VERSION=${targetVersion}`);
    fs.writeFileSync(envPath, envContent);
    result.steps.push({ step: 'update_env', status: 'ok', version: targetVersion });
  }

  // 2. docker compose pull
  log('CPA: docker compose pull...');
  const pullOut = sh(`cd ${CPA_DEPLOY_DIR} && docker compose pull 2>&1 | tail -5`, 120000);
  result.steps.push({ step: 'pull', status: pullOut ? 'ok' : 'failed', output: pullOut });

  // 3. docker compose up -d
  log('CPA: docker compose up -d...');
  const upOut = sh(`cd ${CPA_DEPLOY_DIR} && docker compose up -d 2>&1`, 30000);
  result.steps.push({ step: 'up', status: upOut ? 'ok' : 'failed', output: upOut });

  // 4. 健康检查
  log('CPA: 等待启动...');
  sh('sleep 5');
  const healthy = healthCheck(8317);

  if (healthy) {
    result.status  = 'success';
    result.message = `✅ CPA 升级成功，当前版本: ${getContainerVersion() || 'unknown'}`;
    result.steps.push({ step: 'health_check', status: 'ok' });
  } else {
    result.status  = 'failed';
    result.message = '❌ 升级后健康检查失败，请手动检查: docker logs cli-proxy-api';
    result.steps.push({ step: 'health_check', status: 'failed' });
  }

  return result;
}

// ============ 工具分发 ============

async function handleTool(name, args) {
  switch (name) {

    case 'cpa_status': {
      const checks = {};
      try {
        await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } });
        checks.api = 'healthy';
      } catch (e) { checks.api = `error: ${e.message}`; }
      checks.container_version = getContainerVersion() || 'unknown';
      checks.env_version       = getLocalVersion()     || 'unknown';
      checks.deploy_dir        = CPA_DEPLOY_DIR;
      checks.container_status  = sh(`docker ps --filter name=${CONTAINER_NAME} --format "{{.Status}}"`) || 'not found';
      checks.uptime            = sh(`docker inspect ${CONTAINER_NAME} --format "{{.State.StartedAt}}" 2>/dev/null`) || 'unknown';
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
      const dbPath = `${CPA_DEPLOY_DIR}/auths/usage.sqlite`;
      try {
        const result = execSync(
          `sqlite3 "${dbPath}" "SELECT model, COUNT(*) as requests, SUM(input_tokens) as input, SUM(output_tokens) as output, SUM(total_tokens) as total FROM usage_events WHERE date(timestamp) = '${date}' GROUP BY model ORDER BY requests DESC;" 2>/dev/null`,
          { encoding: 'utf-8', timeout: 5000 }
        ).trim();
        return { date, db: dbPath, data: result || 'No data' };
      } catch (e) { return { date, error: e.message }; }
    }

    case 'cpa_health': {
      const checks = [];
      try {
        await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } });
        checks.push({ name: 'CPA API (/v1/models)', status: 'ok', port: 8317 });
      } catch { checks.push({ name: 'CPA API (/v1/models)', status: 'error', port: 8317 }); }

      checks.push({ name: '/healthz', status: healthCheck(8317, 1) ? 'ok' : 'error' });

      const cpaDocker = sh(`docker ps --filter name=${CONTAINER_NAME} --format "{{.Names}}: {{.Status}}"`);
      checks.push({ name: 'Docker Container', status: cpaDocker?.includes('Up') ? 'ok' : 'error', detail: cpaDocker });
      checks.push({ name: 'Container Version', version: getContainerVersion() || 'unknown' });
      checks.push({ name: '.env Version',      version: getLocalVersion()     || 'unknown' });
      checks.push({ name: 'Deploy Directory',  path: CPA_DEPLOY_DIR, exists: fs.existsSync(CPA_DEPLOY_DIR) });

      try {
        const res = await httpRequest(`${CPA_BASE}/v1/models`, { headers: { 'Authorization': `Bearer ${CPA_KEY}` } });
        checks.push({ name: 'Available Models', count: res?.data?.length || 0 });
      } catch {}
      return checks;
    }

    case 'cpa_config': {
      try {
        const configPath = `${CPA_DEPLOY_DIR}/config.yaml`;
        if (!fs.existsSync(configPath)) return { error: `配置文件不存在: ${configPath}` };
        const config = sh(`sed -E 's/(api-key|secret-key|password|proxy-url|api_key):.*/\\1: ***REDACTED***/gi' "${configPath}" | head -80`);
        return { config_path: configPath, config: config || '无法读取配置' };
      } catch (e) { return { error: e.message }; }
    }

    case 'cpa_upgrade_check': {
      const localVer  = getLocalVersion();
      const actualVer = getContainerVersion();
      const remoteVer = getRemoteVersion();
      return {
        env_version:       localVer  || 'unknown',
        container_version: actualVer || 'unknown',
        remote_latest:     remoteVer || 'unknown (pull failed)',
        deploy_dir:        CPA_DEPLOY_DIR,
        upgrade_hint:      `cd ${CPA_DEPLOY_DIR} && sed -i 's/^CPA_VERSION=.*/CPA_VERSION=vX.X.X/' .env && docker compose pull && docker compose up -d`
      };
    }

    case 'cpa_upgrade': {
      const targetVersion = args?.version || null;
      const dryRun        = args?.dry_run || false;
      return upgradeCpa(targetVersion, dryRun);
    }

    case 'cpa_validate_config': {
      try {
        const configPath = args?.config_path || `${CPA_DEPLOY_DIR}/config.yaml`;
        if (!fs.existsSync(configPath)) return { valid: false, error: `文件不存在: ${configPath}` };
        const result = execSync(
          `python3 -c "import yaml; yaml.safe_load(open('${configPath}')); print('YAML_OK')" 2>&1`,
          { encoding: 'utf-8', timeout: 5000 }
        ).trim();
        if (result !== 'YAML_OK') return { valid: false, error: `YAML 语法错误: ${result}` };
        const issues = [];
        const content = fs.readFileSync(configPath, 'utf8');
        if (content.includes('openai-compatibility:')) {
          if (!content.match(/openai-compatibility:\s*\n(\s+-.*)/)) issues.push('openai-compatibility 必须为数组格式');
        }
        return { valid: issues.length === 0, config_path: configPath, issues, message: issues.length === 0 ? '✅ 配置验证通过' : '❌ 配置存在问题' };
      } catch (e) { return { valid: false, error: e.message }; }
    }

    case 'cpa_containers': {
      try {
        const raw = sh(`docker ps -a --format "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" | grep -iE "cpa|cli-proxy"`);
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
        result = { protocolVersion: '2024-11-05', capabilities: { tools: {} }, serverInfo: { name: 'cpa2a', version: '2.1.0' } };
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
