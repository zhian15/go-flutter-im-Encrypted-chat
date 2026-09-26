import { http } from './http'
import type { ApiResp } from './auth'

export interface FriendReq {
  id: number
  fromUser: number
  toUser: number
  message: string
  status: number
}

export interface Dept {
  id: number
  nameZh: string
  nameEn: string
  parentId: number
  sort: number
}

export const friendApi = {
  list: () => http.get<ApiResp>('/friend/list'),
  request: (toId: number, message = '') =>
    http.post<ApiResp>('/friend/request', { toId, message }),
  incoming: () => http.get<ApiResp<FriendReq[]>>('/friend/request/incoming'),
  outgoing: () => http.get<ApiResp<FriendReq[]>>('/friend/request/outgoing'),
  handle: (id: number, agree: boolean) =>
    http.post<ApiResp>(`/friend/request/${id}/handle?agree=${agree ? 1 : 0}`),
  del: (id: number) => http.delete<ApiResp>(`/friend/${id}`),
  remark: (id: number, remark: string) =>
    http.put<ApiResp>(`/friend/${id}/remark`, { remark }),
  blacklistAdd: (blockId: number) => http.post<ApiResp>('/friend/blacklist', { blockId }),
  blacklistDel: (id: number) => http.delete<ApiResp>(`/friend/blacklist/${id}`),
  blacklistList: () => http.get<ApiResp>('/friend/blacklist')
}

// 部门成员接口补充（userApi 扩展）
export const userApiExt = {
  deptMembers: (deptId: number) => http.get<ApiResp>(`/department/${deptId}/members`)
}
