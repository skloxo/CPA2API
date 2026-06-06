import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { IconGithub, IconBookOpen, IconExternalLink } from '@/components/ui/icons';
import {
  useAuthStore,
  useConfigStore,
  useNotificationStore,
  useModelsStore,
  useThemeStore,
} from '@/stores';
import { versionApi } from '@/services/api';
import { apiKeysApi } from '@/services/api/apiKeys';
import { classifyModels } from '@/utils/models';
import { STORAGE_KEY_AUTH } from '@/utils/constants';
import { INLINE_LOGO_JPEG } from '@/assets/logoInline';
import iconGemini from '@/assets/icons/gemini.svg';
import iconClaude from '@/assets/icons/claude.svg';
import iconOpenaiLight from '@/assets/icons/openai-light.svg';
import iconOpenaiDark from '@/assets/icons/openai-dark.svg';
import iconQwen from '@/assets/icons/qwen.svg';
import iconKimiLight from '@/assets/icons/kimi-light.svg';
import iconKimiDark from '@/assets/icons/kimi-dark.svg';
import iconGlm from '@/assets/icons/glm.svg';
import iconGrok from '@/assets/icons/grok.svg';
import iconGrokDark from '@/assets/icons/grok-dark.svg';
import iconDeepseek from '@/assets/icons/deepseek.svg';
import iconMinimax from '@/assets/icons/minimax.svg';
import styles from './SystemPage.module.scss';

const MODEL_CATEGORY_ICONS: Record<string, string | { light: string; dark: string }> = {
  gpt: { light: iconOpenaiLight, dark: iconOpenaiDark },
  claude: iconClaude,
  gemini: iconGemini,
  qwen: iconQwen,
  kimi: { light: iconKimiLight, dark: iconKimiDark },
  glm: iconGlm,
  grok: { light: iconGrok, dark: iconGrokDark },
  deepseek: iconDeepseek,
  minimax: iconMinimax,
};

interface ParsedVersion {
  upstream: number[];
  patch: number | null;
}

const parseVersion = (versionStr?: string | null): ParsedVersion | null => {
  if (!versionStr) return null;
  const trimmed = versionStr.trim();
  if (!trimmed) return null;

  // Find the custom patch suffix (e.g., -s.5 or -s5)
  const patchMatch = trimmed.match(/-s\.?(\d+)(?:-|$)/i);
  let patch: number | null = null;
  if (patchMatch) {
    patch = Number.parseInt(patchMatch[1], 10);
  }

  // Remove the custom patch suffix to extract the upstream part
  let upstreamStr = trimmed;
  if (patchMatch) {
    upstreamStr = trimmed.replace(patchMatch[0], '');
  }

  // Clean the upstream part
  const cleanedUpstream = upstreamStr.replace(/^v/i, '');
  const upstream = cleanedUpstream
    .split(/[^0-9]+/)
    .filter(Boolean)
    .map((segment) => Number.parseInt(segment, 10))
    .filter(Number.isFinite);

  return {
    upstream: upstream.length ? upstream : [0],
    patch,
  };
};

const compareVersions = (latest?: string | null, current?: string | null) => {
  const latestParsed = parseVersion(latest);
  const currentParsed = parseVersion(current);
  if (!latestParsed || !currentParsed) return null;

  // 1. If both have patch versions, compare patch versions first (our monotonic release counter)
  if (latestParsed.patch !== null && currentParsed.patch !== null) {
    if (latestParsed.patch > currentParsed.patch) return 1;
    if (latestParsed.patch < currentParsed.patch) return -1;
    // If patch versions are equal, fall through to upstream semver comparison
  }

  // 2. Compare upstream semver segments
  const len = Math.max(latestParsed.upstream.length, currentParsed.upstream.length);
  for (let i = 0; i < len; i++) {
    const l = latestParsed.upstream[i] || 0;
    const c = currentParsed.upstream[i] || 0;
    if (l > c) return 1;
    if (l < c) return -1;
  }

  // 3. If upstream segments are equal, but patch presence differs
  if (latestParsed.patch !== null && currentParsed.patch === null) {
    return 1;
  }
  if (latestParsed.patch === null && currentParsed.patch !== null) {
    return -1;
  }

  return 0;
};

export function SystemPage() {
  const { t, i18n } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const auth = useAuthStore();
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);

  const models = useModelsStore((state) => state.models);
  const modelsLoading = useModelsStore((state) => state.loading);
  const modelsError = useModelsStore((state) => state.error);
  const fetchModelsFromStore = useModelsStore((state) => state.fetchModels);

  const [modelStatus, setModelStatus] = useState<{
    type: 'success' | 'warning' | 'error' | 'muted';
    message: string;
  }>();
  const [checkingVersion, setCheckingVersion] = useState(false);
  const [latestVersion, setLatestVersion] = useState<string | null>(null);
  const [upgrading, setUpgrading] = useState(false);

  const apiKeysCache = useRef<string[]>([]);

  const otherLabel = useMemo(
    () => (i18n.language?.toLowerCase().startsWith('zh') ? '其他' : 'Other'),
    [i18n.language]
  );
  const groupedModels = useMemo(() => classifyModels(models, { otherLabel }), [models, otherLabel]);

  const apiVersion = auth.serverVersion || t('system_info.version_unknown');
  const buildTime = auth.serverBuildDate && auth.serverBuildDate !== 'unknown' && auth.serverBuildDate !== 'none'
    ? (() => {
        const date = new Date(auth.serverBuildDate);
        return isNaN(date.getTime()) ? t('system_info.version_unknown') : date.toLocaleString(i18n.language);
      })()
    : t('system_info.version_unknown');

  const getIconForCategory = (categoryId: string): string | null => {
    const iconEntry = MODEL_CATEGORY_ICONS[categoryId];
    if (!iconEntry) return null;
    if (typeof iconEntry === 'string') return iconEntry;
    return resolvedTheme === 'dark' ? iconEntry.dark : iconEntry.light;
  };

  const normalizeApiKeyList = (input: unknown): string[] => {
    if (!Array.isArray(input)) return [];
    const seen = new Set<string>();
    const keys: string[] = [];

    input.forEach((item) => {
      const record =
        item !== null && typeof item === 'object' && !Array.isArray(item)
          ? (item as Record<string, unknown>)
          : null;
      const value =
        typeof item === 'string'
          ? item
          : record
            ? (record['api-key'] ?? record['apiKey'] ?? record.key ?? record.Key)
            : '';
      const trimmed = String(value ?? '').trim();
      if (!trimmed || seen.has(trimmed)) return;
      seen.add(trimmed);
      keys.push(trimmed);
    });

    return keys;
  };

  const resolveApiKeysForModels = useCallback(async () => {
    if (apiKeysCache.current.length) {
      return apiKeysCache.current;
    }

    const configKeys = normalizeApiKeyList(config?.apiKeys);
    if (configKeys.length) {
      apiKeysCache.current = configKeys;
      return configKeys;
    }

    try {
      const list = await apiKeysApi.list();
      const normalized = normalizeApiKeyList(list);
      if (normalized.length) {
        apiKeysCache.current = normalized;
      }
      return normalized;
    } catch (err) {
      console.warn('Auto loading API keys for models failed:', err);
      return [];
    }
  }, [config?.apiKeys]);

  const fetchModels = async ({ forceRefresh = false }: { forceRefresh?: boolean } = {}) => {
    if (auth.connectionStatus !== 'connected') {
      setModelStatus({
        type: 'warning',
        message: t('notification.connection_required'),
      });
      return;
    }

    if (!auth.apiBase) {
      showNotification(t('notification.connection_required'), 'warning');
      return;
    }

    if (forceRefresh) {
      apiKeysCache.current = [];
    }

    setModelStatus({ type: 'muted', message: t('system_info.models_loading') });
    try {
      const apiKeys = await resolveApiKeysForModels();
      const primaryKey = apiKeys[0];
      const list = await fetchModelsFromStore(auth.apiBase, primaryKey, forceRefresh);
      const hasModels = list.length > 0;
      setModelStatus({
        type: hasModels ? 'success' : 'warning',
        message: hasModels
          ? t('system_info.models_count', { count: list.length })
          : t('system_info.models_empty'),
      });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : typeof err === 'string' ? err : '';
      const suffix = message ? `: ${message}` : '';
      const text = `${t('system_info.models_error')}${suffix}`;
      setModelStatus({ type: 'error', message: text });
    }
  };

  const handleClearLoginStorage = () => {
    showConfirmation({
      title: t('system_info.clear_login_title', { defaultValue: 'Clear Login Storage' }),
      message: t('system_info.clear_login_confirm'),
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: () => {
        auth.logout();
        if (typeof localStorage === 'undefined') return;
        const keysToRemove = [STORAGE_KEY_AUTH, 'isLoggedIn', 'apiBase', 'apiUrl', 'managementKey'];
        keysToRemove.forEach((key) => localStorage.removeItem(key));
        showNotification(t('notification.login_storage_cleared'), 'success');
      },
    });
  };

  // Removed Tap-7 easter egg logic


  const handleVersionCheck = useCallback(async () => {
    setCheckingVersion(true);
    try {
      const data = await versionApi.checkLatest();
      const latestRaw = data?.['latest-version'] ?? data?.latest_version ?? data?.latest ?? '';
      const latest = typeof latestRaw === 'string' ? latestRaw : String(latestRaw ?? '');
      const comparison = compareVersions(latest, auth.serverVersion);

      if (!latest) {
        setLatestVersion(null);
        showNotification(t('system_info.version_check_error'), 'error');
        return;
      }

      if (comparison === null) {
        setLatestVersion(null);
        showNotification(t('system_info.version_current_missing'), 'warning');
        return;
      }

      if (comparison > 0) {
        setLatestVersion(latest);
        showNotification(t('system_info.version_update_available', { version: latest }), 'warning');
      } else {
        setLatestVersion(null);
        showNotification(t('system_info.version_is_latest'), 'success');
      }
    } catch (error: unknown) {
      setLatestVersion(null);
      const message =
        error instanceof Error ? error.message : typeof error === 'string' ? error : '';
      const suffix = message ? `: ${message}` : '';
      showNotification(`${t('system_info.version_check_error')}${suffix}`, 'error');
    } finally {
      setCheckingVersion(false);
    }
  }, [auth.serverVersion, showNotification, t]);

  const handleUpgrade = useCallback(() => {
    if (!latestVersion) return;
    showConfirmation({
      title: t('system_info.upgrade_confirm_title', { defaultValue: 'Confirm Upgrade' }),
      message: t('system_info.upgrade_confirm_msg', {
        defaultValue: 'The server will download the latest binary from GitHub and restart. If running in Docker, this update is ephemeral unless .env is mounted as /app/.env. Proceed?'
      }),
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        setUpgrading(true);
        try {
          const res = await versionApi.upgrade();
          const msg = (res as { message?: string })?.message || t('system_info.upgrade_success', { defaultValue: 'Upgrade requested successfully! Server is restarting...' });
          showNotification(msg, 'success');
          // Wait 5 seconds, then refresh page
          setTimeout(() => {
            window.location.reload();
          }, 5000);
        } catch (error: unknown) {
          const message = error instanceof Error ? error.message : typeof error === 'string' ? error : '';
          const suffix = message ? `: ${message}` : '';
          showNotification(`${t('system_info.upgrade_failed', { defaultValue: 'Upgrade failed' })}${suffix}`, 'error');
          setUpgrading(false);
        }
      },
    });
  }, [latestVersion, showConfirmation, showNotification, t]);

  useEffect(() => {
    fetchConfig().catch(() => {
      // ignore
    });
  }, [fetchConfig]);



  // Removed versionTapTimer cleanup

  useEffect(() => {
    fetchModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auth.connectionStatus, auth.apiBase]);

  return (
    <div className={styles.container}>
      <h1 className={styles.pageTitle}>{t('system_info.title')}</h1>
      <div className={styles.content}>
        <Card className={styles.aboutCard}>
          <div className={styles.aboutHeader}>
            <img src={INLINE_LOGO_JPEG} alt="CPAMC" className={styles.aboutLogo} />
            <div className={styles.aboutTitle}>{t('system_info.about_title')}</div>
          </div>

          <div className={styles.aboutInfoGrid}>
            <div className={styles.infoTile}>
              <div className={styles.tileLabel}>{t('footer.api_version')}</div>
              <div className={styles.tileValue}>{apiVersion}</div>
              <div className={styles.tileSub}>
                <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                  <button
                    type="button"
                    className={styles.updateButton}
                    onClick={(event) => {
                      event.stopPropagation();
                      void handleVersionCheck();
                    }}
                    disabled={checkingVersion || upgrading}
                  >
                    {checkingVersion ? t('common.loading') : t('system_info.version_check_button')}
                  </button>
                  {latestVersion && (
                    <button
                      type="button"
                      className={styles.updateButton}
                      onClick={(event) => {
                        event.stopPropagation();
                        handleUpgrade();
                      }}
                      disabled={upgrading}
                      style={{ color: '#f59e0b', textDecoration: 'underline' }}
                    >
                      {upgrading ? t('common.loading') : t('system_info.version_upgrade_button')}
                    </button>
                  )}
                </div>
              </div>
            </div>

            <div className={styles.infoTile}>
              <div className={styles.tileLabel}>{t('footer.build_date')}</div>
              <div className={styles.tileValue}>{buildTime}</div>
              <div className={styles.tileSub}>&nbsp;</div>
            </div>

            <div className={`${styles.infoTile} ${styles.connectionTile}`}>
              <div className={styles.tileLabel}>{t('connection.status')}</div>
              <div className={styles.tileValue}>{t(`common.${auth.connectionStatus}_status`)}</div>
              <div className={styles.tileSub}>{auth.apiBase || '-'}</div>
            </div>
          </div>

          <div className={styles.cardDivider} />

          <div className={styles.aboutLinks}>
            <a
              href="https://github.com/skloxo/CPA2API"
              target="_blank"
              rel="noopener noreferrer"
              className={styles.aboutLink}
            >
              <IconGithub size={16} />
              <span>{t('system_info.link_main_repo')}</span>
              <IconExternalLink size={12} />
            </a>

            <a
              href="https://github.com/skloxo/CPA2API/blob/main/skills/cpa2api-skill/SKILL.md"
              target="_blank"
              rel="noopener noreferrer"
              className={styles.aboutLink}
            >
              <IconBookOpen size={16} />
              <span>{t('system_info.link_docs')}</span>
              <IconExternalLink size={12} />
            </a>
          </div>
        </Card>

        <Card
          title={t('system_info.models_title')}
          extra={
            <Button
              variant="secondary"
              size="sm"
              onClick={() => fetchModels({ forceRefresh: true })}
              loading={modelsLoading}
            >
              {t('common.refresh')}
            </Button>
          }
        >
          <p className={styles.sectionDescription}>{t('system_info.models_desc')}</p>
          {modelStatus && (
            <div className={`status-badge ${modelStatus.type}`}>{modelStatus.message}</div>
          )}
          {modelsError && <div className="error-box">{modelsError}</div>}
          {modelsLoading ? (
            <div className="hint">{t('common.loading')}</div>
          ) : models.length === 0 ? (
            <div className="hint">{t('system_info.models_empty')}</div>
          ) : (
            <div className="item-list">
              {groupedModels.map((group) => {
                const iconSrc = getIconForCategory(group.id);
                return (
                  <div key={group.id} className="item-row">
                    <div className="item-meta">
                      <div className={styles.groupTitle}>
                        {iconSrc && <img src={iconSrc} alt="" className={styles.groupIcon} />}
                        <span className="item-title">{group.label}</span>
                      </div>
                      <div className="item-subtitle">
                        {t('system_info.models_count', { count: group.items.length })}
                      </div>
                    </div>
                    <div className={styles.modelTags}>
                      {group.items.map((model) => (
                        <span
                          key={`${model.name}-${model.alias ?? 'default'}`}
                          className={styles.modelTag}
                          title={model.description || ''}
                        >
                          <span className={styles.modelName}>{model.name}</span>
                          {model.alias && <span className={styles.modelAlias}>{model.alias}</span>}
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>

        <Card title={t('system_info.clear_login_title')}>
          <p className={styles.sectionDescription}>{t('system_info.clear_login_desc')}</p>
          <div className={styles.clearLoginActions}>
            <Button variant="danger" onClick={handleClearLoginStorage}>
              {t('system_info.clear_login_button')}
            </Button>
          </div>
        </Card>
      </div>
    </div>
  );
}
