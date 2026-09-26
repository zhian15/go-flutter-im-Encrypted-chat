import { http } from './http'

// 统一响应结构：{ code, message, data }，服务端只回错误码，文案由客户端按语言渲染
export interface ApiResp<T = unknown> {
  code: number
  message: string
  data: T
}

export interface AuthConfig {
  authMode: 'none' | 'sms' | 'email'
  inviteCodeOn: boolean
  registerOn: boolean
  guestOn: boolean
  e2eOn: boolean
}

export interface LoginResult {
  accessToken: string
  refreshToken: string
  user: Record<string, unknown>
}

export interface CaptchaResult {
  captchaId: string
  image: string
}

export const authApi = {
  getConfig: () => http.get<ApiResp<AuthConfig>>('/auth/config'),
  captcha: () => http.get<ApiResp<CaptchaResult>>('/auth/captcha'),
  sendCode: (account: string, captchaId: string, captchaCode: string, countryCode = '+86') =>
    http.post<ApiResp>('/auth/send-code', { account, captchaId, captchaCode, countryCode }),
  login: (payload: { account: string; password: string; deviceType?: number; deviceId?: string }) =>
    http.post<ApiResp<LoginResult>>('/auth/login', payload),
  register: (payload: {
    account: string
    password: string
    nickname?: string
    countryCode?: string
    departmentId?: number
    code?: string
    inviteCode?: string
    captchaId: string
    captchaCode: string
    deviceType?: number
  }) => http.post<ApiResp<LoginResult>>('/auth/register', payload),
  logout: () => http.post<ApiResp>('/auth/logout')
}

export interface Dept {
  id: number
  nameZh: string
  nameEn: string
}

export const userApi = {
  profile: () => http.get<ApiResp>('/user/profile'),
  updateProfile: (payload: Record<string, unknown>) => http.put<ApiResp>('/user/profile', payload),
  search: (kw: string) => http.get<ApiResp>('/user/search', { params: { kw } }),
  deptTree: () => http.get<ApiResp<Dept[]>>('/department/tree')
}
