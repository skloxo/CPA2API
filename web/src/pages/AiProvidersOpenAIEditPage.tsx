import { useEffect, useCallback, useMemo, useRef, useState } from 'react';
import { useNavigate, useOutletContext } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { HeaderInputList } from '@/components/ui/HeaderInputList';
import { Input } from '@/components/ui/Input';
import { ModelInputList } from '@/components/ui/ModelInputList';
import { Select } from '@/components/ui/Select';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import { SecondaryScreenShell } from '@/components/common/SecondaryScreenShell';
import { useEdgeSwipeBack } from '@/hooks/useEdgeSwipeBack';
import { useNotificationStore } from '@/stores';
import { apiCallApi, getApiCallErrorMessage, authFilesApi, apiClient } from '@/services/api';
import type { ApiKeyEntry } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { buildHeaderObject, hasHeader } from '@/utils/headers';
import { buildApiKeyEntry, buildOpenAIChatCompletionsEndpoint } from '@/components/providers/utils';
import type { OpenAIEditOutletContext } from './AiProvidersOpenAIEditLayout';
import type { KeyTestStatus } from '@/stores/useOpenAIEditDraftStore';
import { Modal } from '@/components/ui/Modal';
import { AuthFilesPrefixProxyEditorModal } from '@/features/authFiles/components/AuthFilesPrefixProxyEditorModal';
import { AuthJsonPasteModal } from '@/features/authFiles/components/AuthJsonPasteModal';
import { convertAuthJsonInput, type AuthJsonInputType } from '@/features/authFiles/sessionAuthConverter';
import { useAuthFilesPrefixProxyEditor } from '@/features/authFiles/hooks/useAuthFilesPrefixProxyEditor';
import type { AuthFileItem } from '@/types';
import styles from './AiProvidersPage.module.scss';
import layoutStyles from './AiProvidersEditLayout.module.scss';

const OPENAI_TEST_TIMEOUT_MS = 30_000;

const getErrorMessage = (err: unknown) => {
  if (err instanceof Error) return err.message;
  if (typeof err === 'string') return err;
  return '';
};

// Status icon components
function StatusLoadingIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" className={styles.statusIconSpin}>
      <circle cx="8" cy="8" r="7" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2" />
      <path
        d="M8 1A7 7 0 0 1 8 15"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  );
}

function StatusSuccessIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <circle cx="8" cy="8" r="8" fill="var(--success-color, #22c55e)" />
      <path
        d="M4.5 8L7 10.5L11.5 6"
        stroke="white"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function StatusErrorIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <circle cx="8" cy="8" r="8" fill="var(--danger-color, #f56c6c)" />
      <path
        d="M5 5L11 11M11 5L5 11"
        stroke="white"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function StatusIdleIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <circle cx="8" cy="8" r="7" stroke="var(--text-tertiary, #9ca3af)" strokeWidth="2" />
    </svg>
  );
}

function StatusIcon({ status }: { status: KeyTestStatus['status'] }) {
  switch (status) {
    case 'loading':
      return <StatusLoadingIcon />;
    case 'success':
      return <StatusSuccessIcon />;
    case 'error':
      return <StatusErrorIcon />;
    default:
      return <StatusIdleIcon />;
  }
}

export function AiProvidersOpenAIEditPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { showNotification, showConfirmation } = useNotificationStore();
  const {
    hasIndexParam,
    invalidIndexParam,
    invalidIndex,
    disableControls,
    loading,
    saving,
    form,
    setForm,
    testModel,
    setTestModel,
    testStatus,
    setTestStatus,
    testMessage,
    setTestMessage,
    keyTestStatuses,
    setDraftKeyTestStatus,
    resetDraftKeyTestStatuses,
    availableModels,
    handleBack,
    handleSave,
  } = useOutletContext<OpenAIEditOutletContext>();

  const title = hasIndexParam
    ? t('ai_providers.openai_edit_modal_title')
    : t('ai_providers.openai_add_modal_title');

  const swipeRef = useEdgeSwipeBack({ onBack: handleBack });
  const [isTestingKeys, setIsTestingKeys] = useState(false);
  const [visibleKeyIndexes, setVisibleKeyIndexes] = useState<Set<number>>(new Set());

  // Qwen States & Handlers
  const [qwenCredentials, setQwenCredentials] = useState<AuthFileItem[]>([]);
  const [loadingQwen, setLoadingQwen] = useState(false);
  const [qwenProxyDrafts, setQwenProxyDrafts] = useState<Record<string, string>>({});
  const [qwenTestStatuses, setQwenTestStatuses] = useState<Record<string, { status: 'idle' | 'loading' | 'success' | 'error', message?: string }>>({});

  const fetchQwenCredentials = useCallback(async () => {
    if (form.baseUrl !== 'qwen') return;
    setLoadingQwen(true);
    try {
      const res = await authFilesApi.list();
      const filtered = (res.files || []).filter(
        (file) => String(file.type ?? file.provider ?? '').toLowerCase() === 'qwen'
      );
      setQwenCredentials(filtered);
      // 初始化代理草稿状态
      const drafts: Record<string, string> = {};
      filtered.forEach((f: any) => {
        drafts[f.name] = String(f.proxy_url ?? f.proxyUrl ?? '');
      });
      setQwenProxyDrafts(drafts);
    } catch (err) {
      console.error('Failed to load Qwen credentials', err);
      showNotification('加载 Qwen 凭证失败', 'error');
    } finally {
      setLoadingQwen(false);
    }
  }, [form.baseUrl, showNotification]);

  useEffect(() => {
    const handleAuthMessage = (event: MessageEvent) => {
      if (event.data && event.data.type === 'QWEN_AUTH_SUCCESS') {
        showNotification(`凭证授权成功 (${event.data.email || ''})，已同步存入 CPA！`, 'success');
        void fetchQwenCredentials();
      }
    };
    window.addEventListener('message', handleAuthMessage);
    return () => window.removeEventListener('message', handleAuthMessage);
  }, [fetchQwenCredentials, showNotification]);

  useEffect(() => {
    if (form.baseUrl === 'qwen') {
      void fetchQwenCredentials();
    }
  }, [form.baseUrl, fetchQwenCredentials]);


  const runQwenCredentialTest = async (file: AuthFileItem) => {
    const authIndex = file.authIndex || file.auth_index || file.name;
    setQwenTestStatuses((prev) => ({ ...prev, [file.name]: { status: 'loading' } }));

    const modelName = testModel.trim() || availableModels[0] || 'qwen3.7-max';
    const endpoint = window.location.origin + '/v1/chat/completions';

    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer $TOKEN$',
    };

    try {
      const result = await apiCallApi.request(
        {
          authIndex: String(authIndex),
          method: 'POST',
          url: endpoint,
          header: headers,
          data: JSON.stringify({
            model: modelName,
            messages: [{ role: 'user', content: 'Hi' }],
            stream: false,
            max_tokens: 5,
          }),
        },
        { timeout: OPENAI_TEST_TIMEOUT_MS }
      );

      if (result.statusCode < 200 || result.statusCode >= 300) {
        throw new Error(getApiCallErrorMessage(result));
      }

      setQwenTestStatuses((prev) => ({ ...prev, [file.name]: { status: 'success' } }));
      showNotification(`${file.email || file.name} 测试连接成功`, 'success');
    } catch (err: unknown) {
      const message = getErrorMessage(err);
      setQwenTestStatuses((prev) => ({ ...prev, [file.name]: { status: 'error', message } }));
      showNotification(`${file.email || file.name} 测试连接失败: ${message}`, 'error');
    }
  };

  const handleQwenRefresh = async (name: string) => {
    try {
      showNotification('正在尝试刷新凭证...', 'info');
      const res = await authFilesApi.refresh(name);
      if (res.status === 'success') {
        showNotification('凭证刷新成功！', 'success');
      } else {
        showNotification(res.message || '凭证刷新失败', 'error');
      }
      void fetchQwenCredentials();
    } catch (err) {
      showNotification(`刷新失败: ${getErrorMessage(err)}`, 'error');
    }
  };

  const handleQwenDelete = async (name: string) => {
    showConfirmation({
      title: '删除 Qwen 凭证',
      message: '确定要删除该 Qwen 凭证吗？该操作不可撤销。',
      variant: 'danger',
      confirmText: t('common.confirm'),
      onConfirm: async () => {
        try {
          await authFilesApi.deleteFileByName(name);
          showNotification('凭证删除成功', 'success');
          void fetchQwenCredentials();
        } catch (err) {
          showNotification(`删除失败: ${getErrorMessage(err)}`, 'error');
        }
      }
    });
  };

  const handleQwenProxyBlur = async (name: string, value: string) => {
    try {
      await authFilesApi.patchFields(name, { proxy_url: value.trim() });
      showNotification('代理地址已更新', 'success');
      void fetchQwenCredentials();
    } catch (err) {
      showNotification(`保存代理失败: ${getErrorMessage(err)}`, 'error');
    }
  };

  const updateQwenProxyDraft = (name: string, value: string) => {
    setQwenProxyDrafts((prev) => ({ ...prev, [name]: value }));
  };

  const [authJsonPasteOpen, setAuthJsonPasteOpen] = useState(false);
  const [authJsonPasteSaving, setAuthJsonPasteSaving] = useState(false);

  const [isQwenLoginOpen, setIsQwenLoginOpen] = useState(false);
  const [qwenEmail, setQwenEmail] = useState('');
  const [qwenPassword, setQwenPassword] = useState('');
  const [qwenCookieInput, setQwenCookieInput] = useState('');
  const [qwenProxy, setQwenProxy] = useState('');
  const [loggingInQwen, setLoggingInQwen] = useState(false);

  const handleOpenQwenOAuthBridge = () => {
    setAuthJsonPasteOpen(false);
    setQwenEmail('');
    setQwenPassword('');
    setQwenCookieInput('');
    setQwenProxy('');
    setIsQwenLoginOpen(true);
  };

  const handleQwenLoginSubmit = async () => {
    if (!qwenEmail.trim() && !qwenCookieInput.trim()) {
      showNotification('请输入邮箱/密码或 Cookie 凭证', 'error');
      return;
    }
    setLoggingInQwen(true);
    try {
      const res = await apiClient.post<{ status: string; email: string }>('/qwen-login', {
        email: qwenEmail.trim() || undefined,
        password: qwenPassword.trim() || undefined,
        cookie: qwenCookieInput.trim() || undefined,
        proxy: qwenProxy.trim() || undefined,
      });
      if (res.status === 'success') {
        showNotification('Qwen 凭证添加成功！', 'success');
        setIsQwenLoginOpen(false);
        setQwenEmail('');
        setQwenPassword('');
        setQwenCookieInput('');
        setQwenProxy('');
        void fetchQwenCredentials();
      } else {
        showNotification('登录/保存凭证失败', 'error');
      }
    } catch (err) {
      showNotification(`操作失败: ${getErrorMessage(err)}`, 'error');
    } finally {
      setLoggingInQwen(false);
    }
  };

  const handleSavePastedAuthJson = async (
    type: AuthJsonInputType,
    fileName: string,
    jsonText: string
  ) => {
    setAuthJsonPasteSaving(true);
    try {
      const authJson = convertAuthJsonInput(jsonText, type);
      const file = new File([JSON.stringify(authJson, null, 2)], fileName, {
        type: 'application/json',
      });
      await authFilesApi.uploadFiles([file]);
      showNotification('凭证添加成功！', 'success');
      setAuthJsonPasteOpen(false);
      void fetchQwenCredentials();
    } catch (err) {
      showNotification(`保存凭证失败: ${getErrorMessage(err)}`, 'error');
      throw err;
    } finally {
      setAuthJsonPasteSaving(false);
    }
  };

  // Auth File details editor hook
  const {
    prefixProxyEditor,
    prefixProxyUpdatedText,
    prefixProxyDirty,
    openPrefixProxyEditor,
    closePrefixProxyEditor,
    handlePrefixProxyChange,
    handlePrefixProxySave,
  } = useAuthFilesPrefixProxyEditor({
    disableControls,
    loadFiles: fetchQwenCredentials,
  });

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        handleBack();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleBack]);

  const canSave = !disableControls && !loading && !saving && !invalidIndexParam && !invalidIndex && !isTestingKeys;
  const hasConfiguredModels = form.baseUrl === 'qwen'
    ? true
    : form.modelEntries.some((entry) => entry.name.trim());
  const hasTestableKeys = form.baseUrl === 'qwen'
    ? qwenCredentials.length > 0
    : form.apiKeyEntries.some(
        (entry) => entry.apiKey?.trim() || normalizeAuthIndex(entry.authIndex)
      );
  const modelSelectOptions = useMemo(() => {
    const seen = new Set<string>();
    return form.modelEntries.reduce<Array<{ value: string; label: string }>>((acc, entry) => {
      const name = entry.name.trim();
      if (!name || seen.has(name)) return acc;
      seen.add(name);
      const alias = entry.alias.trim();
      acc.push({
        value: name,
        label: alias && alias !== name ? `${name} (${alias})` : name,
      });
      return acc;
    }, []);
  }, [form.modelEntries]);
  const connectivityConfigSignature = useMemo(() => {
    const headersSignature = form.headers
      .map((entry) => `${entry.key.trim()}:${entry.value.trim()}`)
      .join('|');
    const modelsSignature = form.modelEntries
      .map((entry) => `${entry.name.trim()}:${entry.alias.trim()}`)
      .join('|');
    return [form.baseUrl.trim(), testModel.trim(), headersSignature, modelsSignature].join('||');
  }, [form.baseUrl, form.headers, form.modelEntries, testModel]);
  const previousConnectivityConfigRef = useRef(connectivityConfigSignature);

  useEffect(() => {
    if (previousConnectivityConfigRef.current === connectivityConfigSignature) {
      return;
    }
    previousConnectivityConfigRef.current = connectivityConfigSignature;
    resetDraftKeyTestStatuses(form.apiKeyEntries.length);
    setTestStatus('idle');
    setTestMessage('');
  }, [
    connectivityConfigSignature,
    form.apiKeyEntries.length,
    resetDraftKeyTestStatuses,
    setTestStatus,
    setTestMessage,
  ]);

  // Test a single key by index
  const runSingleKeyTest = useCallback(
    async (keyIndex: number): Promise<boolean> => {
      const baseUrl = form.baseUrl.trim();
      if (!baseUrl) {
        showNotification(t('notification.openai_test_url_required'), 'error');
        return false;
      }

      const endpoint = buildOpenAIChatCompletionsEndpoint(baseUrl);
      if (!endpoint) {
        showNotification(t('notification.openai_test_url_required'), 'error');
        return false;
      }

      const keyEntry = form.apiKeyEntries[keyIndex];
      const keyAuthIndex = normalizeAuthIndex(keyEntry?.authIndex) ?? undefined;
      if (!keyEntry?.apiKey?.trim() && !keyAuthIndex) {
        setDraftKeyTestStatus(keyIndex, { status: 'error', message: t('notification.openai_test_key_required') });
        return false;
      }

      const modelName = testModel.trim() || availableModels[0] || '';
      if (!modelName) {
        showNotification(t('notification.openai_test_model_required'), 'error');
        return false;
      }

      const customHeaders = buildHeaderObject(form.headers);
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        ...customHeaders,
      };
      if (!hasHeader(headers, 'authorization')) {
        headers.Authorization = keyAuthIndex ? 'Bearer $TOKEN$' : `Bearer ${keyEntry.apiKey.trim()}`;
      }

      // Set loading state for this key
      setDraftKeyTestStatus(keyIndex, { status: 'loading', message: '' });

      try {
        const result = await apiCallApi.request(
          {
            authIndex: keyAuthIndex,
            method: 'POST',
            url: endpoint,
            header: Object.keys(headers).length ? headers : undefined,
            data: JSON.stringify({
              model: modelName,
              messages: [{ role: 'user', content: 'Hi' }],
              stream: false,
              max_tokens: 5,
            }),
          },
          { timeout: OPENAI_TEST_TIMEOUT_MS }
        );

        if (result.statusCode < 200 || result.statusCode >= 300) {
          throw new Error(getApiCallErrorMessage(result));
        }

        setDraftKeyTestStatus(keyIndex, { status: 'success', message: '' });
        return true;
      } catch (err: unknown) {
        const message = getErrorMessage(err);
        const errorCode =
          typeof err === 'object' && err !== null && 'code' in err
            ? String((err as { code?: string }).code)
            : '';
        const isTimeout = errorCode === 'ECONNABORTED' || message.toLowerCase().includes('timeout');
        const errorMessage = isTimeout
          ? t('ai_providers.openai_test_timeout', { seconds: OPENAI_TEST_TIMEOUT_MS / 1000 })
          : message;
        setDraftKeyTestStatus(keyIndex, { status: 'error', message: errorMessage });
        return false;
      }
    },
    [form.baseUrl, form.apiKeyEntries, form.headers, testModel, availableModels, t, setDraftKeyTestStatus, showNotification]
  );

  const testSingleKey = useCallback(
    async (keyIndex: number): Promise<boolean> => {
      if (isTestingKeys) return false;
      setIsTestingKeys(true);
      try {
        return await runSingleKeyTest(keyIndex);
      } finally {
        setIsTestingKeys(false);
      }
    },
    [isTestingKeys, runSingleKeyTest]
  );

  // Test all keys
  const testAllKeys = useCallback(async () => {
    if (isTestingKeys) return;

    const baseUrl = form.baseUrl.trim();
    if (!baseUrl) {
      const message = t('notification.openai_test_url_required');
      setTestStatus('error');
      setTestMessage(message);
      showNotification(message, 'error');
      return;
    }

    if (baseUrl === 'qwen') {
      if (qwenCredentials.length === 0) {
        const message = '暂无 Qwen 凭证';
        setTestStatus('error');
        setTestMessage(message);
        showNotification(message, 'error');
        return;
      }
      setIsTestingKeys(true);
      setTestStatus('loading');
      setTestMessage('正在测试全部凭证...');

      setQwenTestStatuses((prev) => {
        const next = { ...prev };
        qwenCredentials.forEach((file) => {
          next[file.name] = { status: 'loading' };
        });
        return next;
      });

      const modelName = testModel.trim() || availableModels[0] || 'qwen3.7-max';
      const endpoint = window.location.origin + '/v1/chat/completions';
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer $TOKEN$',
      };

      try {
        const results = await Promise.all(
          qwenCredentials.map(async (file) => {
            const authIndex = file.authIndex || file.auth_index || file.name;
            try {
              const result = await apiCallApi.request(
                {
                  authIndex: String(authIndex),
                  method: 'POST',
                  url: endpoint,
                  header: headers,
                  data: JSON.stringify({
                    model: modelName,
                    messages: [{ role: 'user', content: 'Hi' }],
                    stream: false,
                    max_tokens: 5,
                  }),
                },
                { timeout: OPENAI_TEST_TIMEOUT_MS }
              );

              if (result.statusCode < 200 || result.statusCode >= 300) {
                throw new Error(getApiCallErrorMessage(result));
              }

              setQwenTestStatuses((prev) => ({ ...prev, [file.name]: { status: 'success' } }));
              return true;
            } catch (err: unknown) {
              const message = getErrorMessage(err);
              setQwenTestStatuses((prev) => ({ ...prev, [file.name]: { status: 'error', message } }));
              return false;
            }
          })
        );

        const successCount = results.filter(Boolean).length;
        const failCount = qwenCredentials.length - successCount;

        if (failCount === 0) {
          const message = `所有凭证测试连接成功 (共 ${successCount} 个)`;
          setTestStatus('success');
          setTestMessage(message);
          showNotification(message, 'success');
        } else if (successCount === 0) {
          const message = `所有凭证测试连接失败 (共 ${failCount} 个)`;
          setTestStatus('error');
          setTestMessage(message);
          showNotification(message, 'error');
        } else {
          const message = `部分凭证测试连接成功 (成功 ${successCount} 个，失败 ${failCount} 个)`;
          setTestStatus('error');
          setTestMessage(message);
          showNotification(message, 'warning');
        }
      } catch (err) {
        const message = getErrorMessage(err);
        setTestStatus('error');
        setTestMessage(message);
        showNotification(message, 'error');
      } finally {
        setIsTestingKeys(false);
      }
      return;
    }

    const endpoint = buildOpenAIChatCompletionsEndpoint(baseUrl);
    if (!endpoint) {
      const message = t('notification.openai_test_url_required');
      setTestStatus('error');
      setTestMessage(message);
      showNotification(message, 'error');
      return;
    }

    const modelName = testModel.trim() || availableModels[0] || '';
    if (!modelName) {
      const message = t('notification.openai_test_model_required');
      setTestStatus('error');
      setTestMessage(message);
      showNotification(message, 'error');
      return;
    }

    const validKeyIndexes = form.apiKeyEntries
      .map((entry, index) => (entry.apiKey?.trim() || normalizeAuthIndex(entry.authIndex) ? index : -1))
      .filter((index) => index >= 0);
    if (validKeyIndexes.length === 0) {
      const message = t('notification.openai_test_key_required');
      setTestStatus('error');
      setTestMessage(message);
      showNotification(message, 'error');
      return;
    }

    setIsTestingKeys(true);
    setTestStatus('loading');
    setTestMessage(t('ai_providers.openai_test_running'));
    resetDraftKeyTestStatuses(form.apiKeyEntries.length);

    try {
      const results = await Promise.all(validKeyIndexes.map((index) => runSingleKeyTest(index)));

      const successCount = results.filter(Boolean).length;
      const failCount = validKeyIndexes.length - successCount;

      if (failCount === 0) {
        const message = t('ai_providers.openai_test_all_success', { count: successCount });
        setTestStatus('success');
        setTestMessage(message);
        showNotification(message, 'success');
      } else if (successCount === 0) {
        const message = t('ai_providers.openai_test_all_failed', { count: failCount });
        setTestStatus('error');
        setTestMessage(message);
        showNotification(message, 'error');
      } else {
        const message = t('ai_providers.openai_test_all_partial', { success: successCount, failed: failCount });
        setTestStatus('error');
        setTestMessage(message);
        showNotification(message, 'warning');
      }
    } finally {
      setIsTestingKeys(false);
    }
  }, [
    isTestingKeys,
    form.baseUrl,
    form.apiKeyEntries,
    testModel,
    availableModels,
    t,
    setTestStatus,
    setTestMessage,
    resetDraftKeyTestStatuses,
    runSingleKeyTest,
    showNotification,
    qwenCredentials,
    setQwenTestStatuses,
  ]);

  const openOpenaiModelDiscovery = () => {
    const baseUrl = form.baseUrl.trim();
    if (!baseUrl) {
      showNotification(t('ai_providers.openai_models_fetch_invalid_url'), 'error');
      return;
    }
    navigate('models');
  };

  const renderKeyEntries = (entries: ApiKeyEntry[]) => {
    const list = entries.length ? entries : [buildApiKeyEntry()];

    const updateEntry = (idx: number, field: keyof ApiKeyEntry, value: string) => {
      const next = list.map((entry, i) => (i === idx ? { ...entry, [field]: value } : entry));
      setForm((prev) => ({ ...prev, apiKeyEntries: next }));
      setDraftKeyTestStatus(idx, { status: 'idle', message: '' });
      setTestStatus('idle');
      setTestMessage('');
    };

    const removeEntry = (idx: number) => {
      const next = list.filter((_, i) => i !== idx);
      const nextLength = next.length ? next.length : 1;
      setForm((prev) => ({
        ...prev,
        apiKeyEntries: next.length ? next : [buildApiKeyEntry()],
      }));
      setVisibleKeyIndexes((prev) => {
        const shifted = new Set<number>();
        prev.forEach((visibleIndex) => {
          if (visibleIndex < idx) {
            shifted.add(visibleIndex);
          } else if (visibleIndex > idx) {
            shifted.add(visibleIndex - 1);
          }
        });
        return shifted;
      });
      resetDraftKeyTestStatuses(nextLength);
      setTestStatus('idle');
      setTestMessage('');
    };

    const addEntry = () => {
      setForm((prev) => ({ ...prev, apiKeyEntries: [...list, buildApiKeyEntry()] }));
      resetDraftKeyTestStatuses(list.length + 1);
      setTestStatus('idle');
      setTestMessage('');
    };

    return (
      <div className={styles.keyEntriesList}>
        <div className={styles.keyEntriesToolbar}>
          <span className={styles.keyEntriesCount}>
            {t('ai_providers.openai_keys_count')}: {list.length}
          </span>
          <Button
            variant="secondary"
            size="sm"
            onClick={addEntry}
            disabled={saving || disableControls || isTestingKeys}
            className={styles.addKeyButton}
          >
            {t('ai_providers.openai_keys_add_btn')}
          </Button>
        </div>
        <div className={styles.keyTableShell}>
          {/* 表头 */}
          <div className={styles.keyTableHeader}>
            <div className={styles.keyTableColIndex}>#</div>
            <div className={styles.keyTableColStatus}>{t('common.status')}</div>
            <div className={styles.keyTableColKey}>{t('common.api_key')}</div>
            <div className={styles.keyTableColProxy}>{t('common.proxy_url')}</div>
            <div className={styles.keyTableColAction}>{t('common.action')}</div>
          </div>

          {/* 数据行 */}
          {list.map((entry, index) => {
            const keyStatus = keyTestStatuses[index]?.status ?? 'idle';
            const keyVisible = visibleKeyIndexes.has(index);
            const canTestKey =
              Boolean(entry.apiKey?.trim() || normalizeAuthIndex(entry.authIndex)) &&
              hasConfiguredModels;

            return (
              <div key={index} className={styles.keyTableRow}>
                {/* 序号 */}
                <div className={styles.keyTableColIndex}>{index + 1}</div>

                {/* 状态指示灯 */}
                <div
                  className={styles.keyTableColStatus}
                  title={keyTestStatuses[index]?.message || ''}
                >
                  <StatusIcon status={keyStatus} />
                </div>

                {/* Key 输入框 */}
                <div className={styles.keyTableColKey}>
                  <div className={styles.keyTableInputWrap}>
                    <input
                      type={keyVisible ? 'text' : 'password'}
                      name={`openai-provider-api-key-${index}`}
                      autoComplete="new-password"
                      value={entry.apiKey}
                      onChange={(e) => updateEntry(index, 'apiKey', e.target.value)}
                      disabled={saving || disableControls || isTestingKeys}
                      className={`input ${styles.keyTableInput} ${styles.keyTableSecretInput}`}
                      placeholder={t('ai_providers.openai_key_placeholder')}
                    />
                    <button
                      type="button"
                      className={styles.keyTableVisibilityButton}
                      onClick={() =>
                        setVisibleKeyIndexes((prev) => {
                          const next = new Set(prev);
                          if (next.has(index)) {
                            next.delete(index);
                          } else {
                            next.add(index);
                          }
                          return next;
                        })
                      }
                      aria-label={keyVisible ? t('login.hide_key') : t('login.show_key')}
                      title={keyVisible ? t('login.hide_key') : t('login.show_key')}
                      disabled={saving || disableControls || isTestingKeys}
                    >
                      {keyVisible ? <IconEyeOff size={16} /> : <IconEye size={16} />}
                    </button>
                  </div>
                </div>

                {/* Proxy 输入框 */}
                <div className={styles.keyTableColProxy}>
                  <input
                    type="text"
                    value={entry.proxyUrl ?? ''}
                    onChange={(e) => updateEntry(index, 'proxyUrl', e.target.value)}
                    disabled={saving || disableControls || isTestingKeys}
                    className={`input ${styles.keyTableInput}`}
                    placeholder={t('ai_providers.openai_proxy_placeholder')}
                  />
                </div>

                {/* 操作按钮 */}
                <div className={styles.keyTableColAction}>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void testSingleKey(index)}
                    disabled={saving || disableControls || isTestingKeys || !canTestKey}
                    loading={keyStatus === 'loading'}
                  >
                    {t('ai_providers.openai_test_single_action')}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => removeEntry(index)}
                    disabled={saving || disableControls || isTestingKeys || list.length <= 1}
                  >
                    {t('common.delete')}
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    );
  };

  const copyTextWithNotification = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showNotification(t('notification.link_copied', { defaultValue: '已复制到剪贴板' }), 'success');
    } catch {
      showNotification(t('notification.copy_failed', { defaultValue: '复制失败' }), 'error');
    }
  };

  const renderQwenCredentialsTable = () => {
    return (
      <div className={styles.keyEntriesList}>
        <div className={styles.keyEntriesToolbar}>
          <span className={styles.keyEntriesCount}>
            凭证总数: {qwenCredentials.length}
          </span>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleOpenQwenOAuthBridge}
            disabled={saving || disableControls || isTestingKeys}
            className={styles.addKeyButton}
          >
            添加 Qwen 凭证
          </Button>
        </div>
        <div className={styles.keyTableShell}>
          {/* 表头 */}
          <div className={styles.keyTableHeader}>
            <div className={styles.keyTableColIndex} style={{ width: '40px' }}>#</div>
            <div className={styles.keyTableColStatus} style={{ width: '64px' }}>测试状态</div>
            <div className={styles.keyTableColKey}>账号 (Email)</div>
            <div className={styles.keyTableColProxy}>账号级代理 (优先走账号代理)</div>
            <div className={styles.keyTableColAction}>操作</div>
          </div>

          {/* 数据行 */}
          {loadingQwen ? (
            <div style={{ padding: '20px', textAlign: 'center', color: 'var(--text-secondary)' }}>
              加载中...
            </div>
          ) : qwenCredentials.length === 0 ? (
            <div style={{ padding: '20px', textAlign: 'center', color: 'var(--text-secondary)' }}>
              暂无账号凭证，请点击上方“添加 Qwen 凭证”进行添加
            </div>
          ) : (
            qwenCredentials.map((file, index) => {
              const testStatus = qwenTestStatuses[file.name]?.status ?? 'idle';
              
              return (
                <div key={file.name} className={styles.keyTableRow}>
                  {/* 序号 */}
                  <div className={styles.keyTableColIndex} style={{ width: '40px' }}>{index + 1}</div>

                  {/* 测试连接状态灯 */}
                  <div
                    className={styles.keyTableColStatus}
                    style={{ width: '64px' }}
                    title={qwenTestStatuses[file.name]?.message || ''}
                  >
                    <StatusIcon status={testStatus} />
                  </div>

                  {/* Email */}
                  <div className={styles.keyTableColKey} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {String(file.email || file.name || '')}
                  </div>

                  {/* 账号级代理代理输入框 */}
                  <div className={styles.keyTableColProxy}>
                    <input
                      type="text"
                      value={qwenProxyDrafts[file.name] ?? String(file.proxy_url ?? file.proxyUrl ?? '')}
                      onChange={(e) => updateQwenProxyDraft(file.name, e.target.value)}
                      onBlur={(e) => void handleQwenProxyBlur(file.name, e.target.value)}
                      disabled={saving || disableControls}
                      className={`input ${styles.keyTableInput}`}
                      placeholder="默认使用全局代理 / 直连"
                    />
                  </div>

                  {/* 操作按钮 */}
                  <div className={styles.keyTableColAction}>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => void runQwenCredentialTest(file)}
                      disabled={saving || disableControls || isTestingKeys}
                      loading={testStatus === 'loading'}
                    >
                      测试
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => void openPrefixProxyEditor(file)}
                      disabled={saving || disableControls}
                    >
                      详情
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => void handleQwenRefresh(file.name)}
                      disabled={saving || disableControls}
                    >
                      刷新
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      onClick={() => void handleQwenDelete(file.name)}
                      disabled={saving || disableControls}
                    >
                      删除
                    </Button>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>
    );
  };

  return (
    <SecondaryScreenShell
      ref={swipeRef}
      contentClassName={layoutStyles.content}
      title={title}
      onBack={handleBack}
      backLabel={t('common.back')}
      backAriaLabel={t('common.back')}
      hideTopBarBackButton
      hideTopBarRightAction
      floatingAction={
        <div className={layoutStyles.floatingActions}>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleBack}
            className={layoutStyles.floatingBackButton}
          >
            {t('common.back')}
          </Button>
          <Button
            size="sm"
            onClick={() => void handleSave()}
            loading={saving}
            disabled={!canSave}
            className={layoutStyles.floatingSaveButton}
          >
            {t('common.save')}
          </Button>
        </div>
      }
      isLoading={loading}
      loadingLabel={t('common.loading')}
    >
      <Card>
        {invalidIndexParam || invalidIndex ? (
          <div className={styles.sectionHint}>{t('common.invalid_provider_index')}</div>
        ) : (
          <div className={styles.openaiEditForm}>
            <Input
              label={t('ai_providers.openai_add_modal_name_label')}
              value={form.name}
              onChange={(e) => setForm((prev) => ({ ...prev, name: e.target.value }))}
              disabled={saving || disableControls || isTestingKeys}
            />
            <Input
              label={t('ai_providers.priority_label')}
              hint={t('ai_providers.priority_hint')}
              type="number"
              step={1}
              value={form.priority ?? ''}
              onChange={(e) => {
                const raw = e.target.value;
                const parsed = raw.trim() === '' ? undefined : Number(raw);
                setForm((prev) => ({
                  ...prev,
                  priority: parsed !== undefined && Number.isFinite(parsed) ? parsed : undefined,
                }));
              }}
              disabled={saving || disableControls || isTestingKeys}
            />
            <Input
              label={t('ai_providers.prefix_label')}
              placeholder={t('ai_providers.prefix_placeholder')}
              value={form.prefix ?? ''}
              onChange={(e) => setForm((prev) => ({ ...prev, prefix: e.target.value }))}
              hint={t('ai_providers.prefix_hint')}
              disabled={saving || disableControls || isTestingKeys}
            />
            <div className="form-group">
              <label>接口类型 / 预设</label>
              <Select
                value={form.baseUrl === 'qwen' ? 'qwen' : 'custom'}
                options={[
                  { value: 'custom', label: '自定义 OpenAI 兼容接口' },
                  { value: 'qwen', label: 'Qwen Preset (通义千问网页端 OAuth 模式)' },
                ]}
                onChange={(val) => {
                  if (val === 'qwen') {
                    setForm((prev) => ({ ...prev, baseUrl: 'qwen' }));
                  } else {
                    setForm((prev) => ({ ...prev, baseUrl: '' }));
                  }
                }}
                disabled={saving || disableControls || isTestingKeys}
              />
            </div>

            {form.baseUrl === 'qwen' ? (
              <Input
                label={t('ai_providers.openai_add_modal_url_label')}
                value="chat.qwen.ai (Qwen OAuth 登录模式)"
                disabled
              />
            ) : (
              <Input
                label={t('ai_providers.openai_add_modal_url_label')}
                value={form.baseUrl}
                onChange={(e) => setForm((prev) => ({ ...prev, baseUrl: e.target.value }))}
                disabled={saving || disableControls || isTestingKeys}
              />
            )}

            {form.baseUrl === 'qwen' && (
              <Input
                label="代理地址 (可选)"
                value={form.proxyUrl ?? ''}
                onChange={(e) => setForm((prev) => ({ ...prev, proxyUrl: e.target.value || undefined }))}
                placeholder="例如 http://127.0.0.1:7890"
                disabled={saving || disableControls || isTestingKeys}
              />
            )}

            {form.baseUrl !== 'qwen' && (
              <HeaderInputList
                entries={form.headers}
                onChange={(entries) => setForm((prev) => ({ ...prev, headers: entries }))}
                addLabel={t('common.custom_headers_add')}
                keyPlaceholder={t('common.custom_headers_key_placeholder')}
                valuePlaceholder={t('common.custom_headers_value_placeholder')}
                removeButtonTitle={t('common.delete')}
                removeButtonAriaLabel={t('common.delete')}
                disabled={saving || disableControls || isTestingKeys}
              />
            )}

            {/* 模型配置区域 - 统一布局 */}
            <div className={styles.modelConfigSection}>
              {/* 标题行 */}
              <div className={styles.modelConfigHeader}>
                <label className={styles.modelConfigTitle}>
                  {hasIndexParam
                    ? t('ai_providers.openai_edit_modal_models_label')
                    : t('ai_providers.openai_add_modal_models_label')}
                </label>
                <div className={styles.modelConfigToolbar}>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setForm((prev) => ({
                      ...prev,
                      modelEntries: [...prev.modelEntries, { name: '', alias: '' }]
                    }))}
                    disabled={saving || disableControls || isTestingKeys}
                  >
                    {t('ai_providers.openai_models_add_btn')}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={openOpenaiModelDiscovery}
                    disabled={saving || disableControls || isTestingKeys}
                  >
                    {t('ai_providers.openai_models_fetch_button')}
                  </Button>
                </div>
              </div>

              {/* 提示文本 */}
              <div className={styles.sectionHint}>{t('ai_providers.openai_models_hint')}</div>

              {/* 模型列表 */}
              <ModelInputList
                entries={form.modelEntries}
                onChange={(entries) => setForm((prev) => ({ ...prev, modelEntries: entries }))}
                namePlaceholder={t('common.model_name_placeholder')}
                aliasPlaceholder={t('common.model_alias_placeholder')}
                disabled={saving || disableControls || isTestingKeys}
                hideAddButton
                className={styles.modelInputList}
                rowClassName={styles.modelInputRow}
                inputClassName={styles.modelInputField}
                removeButtonClassName={styles.modelRowRemoveButton}
                removeButtonTitle={t('common.delete')}
                removeButtonAriaLabel={t('common.delete')}
              />

              {/* 测试区域 */}
              <div className={styles.modelTestPanel}>
                <div className={styles.modelTestMeta}>
                  <label className={styles.modelTestLabel}>{t('ai_providers.openai_test_title')}</label>
                  <span className={styles.modelTestHint}>{t('ai_providers.openai_test_hint')}</span>
                </div>
                <div className={styles.modelTestControls}>
                  <Select
                    value={testModel}
                    options={modelSelectOptions}
                    onChange={(value) => {
                      setTestModel(value);
                      setTestStatus('idle');
                      setTestMessage('');
                    }}
                    placeholder={
                      availableModels.length
                        ? t('ai_providers.openai_test_select_placeholder')
                        : t('ai_providers.openai_test_select_empty')
                    }
                    className={styles.openaiTestSelect}
                    ariaLabel={t('ai_providers.openai_test_title')}
                    disabled={saving || disableControls || isTestingKeys || testStatus === 'loading' || availableModels.length === 0}
                  />
                  <Button
                    variant={testStatus === 'error' ? 'danger' : 'secondary'}
                    size="sm"
                    onClick={() => void testAllKeys()}
                    loading={testStatus === 'loading'}
                    disabled={saving || disableControls || isTestingKeys || testStatus === 'loading' || !hasConfiguredModels || !hasTestableKeys}
                    title={t('ai_providers.openai_test_all_hint')}
                    className={styles.modelTestAllButton}
                  >
                    {t('ai_providers.openai_test_all_action')}
                  </Button>
                </div>
              </div>
              {testMessage && (
                <div
                  className={`status-badge ${
                    testStatus === 'error'
                      ? 'error'
                      : testStatus === 'success'
                        ? 'success'
                        : 'muted'
                  }`}
                >
                  {testMessage}
                </div>
              )}
            </div>

            <div className={styles.keyEntriesSection}>
              <div className={styles.keyEntriesHeader}>
                <label className={styles.keyEntriesTitle}>
                  凭证管理
                </label>
                <span className={styles.keyEntriesHint}>
                  {form.baseUrl === 'qwen'
                    ? '管理通义千问网页端账号凭证，支持账号级代理、手动刷新等。'
                    : t('ai_providers.openai_keys_hint')}
                </span>
              </div>
              {form.baseUrl === 'qwen'
                ? renderQwenCredentialsTable()
                : renderKeyEntries(form.apiKeyEntries)}
            </div>
          </div>
        )}
      </Card>

      <AuthFilesPrefixProxyEditorModal
        disableControls={disableControls}
        editor={prefixProxyEditor}
        updatedText={prefixProxyUpdatedText}
        dirty={prefixProxyDirty}
        onClose={closePrefixProxyEditor}
        onCopyText={copyTextWithNotification}
        onRefresh={() => {
          if (prefixProxyEditor?.fileName) {
            void handleQwenRefresh(prefixProxyEditor.fileName);
          }
        }}
        onSave={handlePrefixProxySave}
        onChange={handlePrefixProxyChange}
      />

      <AuthJsonPasteModal
        open={authJsonPasteOpen}
        saving={authJsonPasteSaving}
        disabled={disableControls}
        onClose={() => {
          if (!authJsonPasteSaving) setAuthJsonPasteOpen(false);
        }}
        onSave={handleSavePastedAuthJson}
      />

      <Modal
        open={isQwenLoginOpen}
        title="添加 Qwen 账号凭证"
        onClose={() => {
          if (!loggingInQwen) {
            setIsQwenLoginOpen(false);
            setQwenEmail('');
            setQwenPassword('');
            setQwenCookieInput('');
            setQwenProxy('');
          }
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '16px', padding: '16px 0' }}>
          <Input
            label="邮箱账号"
            type="email"
            value={qwenEmail}
            onChange={(e) => setQwenEmail(e.target.value)}
            placeholder="请输入 Qwen 登录邮箱 (示例: user@qwen.ai)"
            disabled={loggingInQwen}
          />
          <Input
            label="登录密码 (密码登录自动获取 Token & Cookie)"
            type="password"
            value={qwenPassword}
            onChange={(e) => setQwenPassword(e.target.value)}
            placeholder="请输入 Qwen 登录密码"
            disabled={loggingInQwen}
          />
          <Input
            label="Cookie 字符串 (可选 / 手动粘贴 document.cookie)"
            value={qwenCookieInput}
            onChange={(e) => setQwenCookieInput(e.target.value)}
            placeholder="可选粘贴 document.cookie (cna=...; ssxmod_itna=...)"
            disabled={loggingInQwen}
          />
          <Input
            label="代理地址 (可选)"
            value={qwenProxy}
            onChange={(e) => setQwenProxy(e.target.value)}
            placeholder="账号级代理，例如 http://127.0.0.1:7890"
            disabled={loggingInQwen}
            hint="配置此项后，该账号的登录与后续 API 调用将优先走此代理。"
          />
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '16px' }}>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setIsQwenLoginOpen(false);
              setAuthJsonPasteOpen(true);
            }}
            disabled={loggingInQwen}
          >
            粘贴 Auth JSON / 会话
          </Button>
          <div style={{ display: 'flex', gap: '12px' }}>
            <Button
              variant="secondary"
              onClick={() => {
                setIsQwenLoginOpen(false);
                setQwenEmail('');
                setQwenPassword('');
                setQwenCookieInput('');
                setQwenProxy('');
              }}
              disabled={loggingInQwen}
            >
              {t('common.cancel')}
            </Button>
            <Button
              onClick={() => void handleQwenLoginSubmit()}
              loading={loggingInQwen}
              disabled={loggingInQwen}
            >
              获取凭证并保存
            </Button>
          </div>
        </div>
      </Modal>
    </SecondaryScreenShell>
  );
}
