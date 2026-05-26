/**
 * Qwen 邮箱密码登录 API
 */

import { apiClient } from './client';

export interface QwenLoginResponse {
  status: string;
  email?: string;
  message?: string;
}

export const qwenLoginApi = {
  login: (email: string, password: string, proxy?: string) =>
    apiClient.post<QwenLoginResponse>('/qwen-login', {
      email,
      password,
      ...(proxy ? { proxy } : {}),
    }),

  refreshToken: (name: string) =>
    apiClient.post<{ status: string; message?: string }>(
      `/auth-files/${encodeURIComponent(name)}/refresh`
    ),
};
