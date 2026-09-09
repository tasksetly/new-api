/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  ApiResponse,
  ProvisionUpstreamGroupsPayload,
  ProvisionUpstreamGroupsResult,
  UpstreamProvider,
  UpstreamProviderGroups,
  UpstreamProviderPage,
  UpstreamProviderPayload,
} from './types'

const requestConfig: ApiRequestConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
}

function unwrapResponse<T>(response: ApiResponse<T>): T {
  if (!response.success) {
    throw new Error(response.message || 'Request failed')
  }

  return response.data as T
}

export function getUpstreamProviderErrorMessage(
  error: unknown,
  fallback: string
): string {
  return error instanceof Error && error.message ? error.message : fallback
}

export async function getUpstreamProviders(params?: {
  p?: number
  page_size?: number
}): Promise<UpstreamProviderPage> {
  const response = await api.get<ApiResponse<UpstreamProviderPage>>(
    '/api/upstream-providers',
    { ...requestConfig, params }
  )
  return unwrapResponse(response.data)
}

export async function createUpstreamProvider(
  payload: UpstreamProviderPayload
): Promise<UpstreamProvider> {
  const response = await api.post<ApiResponse<UpstreamProvider>>(
    '/api/upstream-providers',
    payload,
    requestConfig
  )
  return unwrapResponse(response.data)
}

export async function updateUpstreamProvider(
  id: number,
  payload: UpstreamProviderPayload
): Promise<UpstreamProvider> {
  const response = await api.put<ApiResponse<UpstreamProvider>>(
    `/api/upstream-providers/${id}`,
    payload,
    requestConfig
  )
  return unwrapResponse(response.data)
}

export async function deleteUpstreamProvider(id: number): Promise<void> {
  const response = await api.delete<ApiResponse>(
    `/api/upstream-providers/${id}`,
    requestConfig
  )
  unwrapResponse(response.data)
}

export async function testUpstreamProvider(id: number): Promise<void> {
  const response = await api.post<ApiResponse>(
    `/api/upstream-providers/${id}/test`,
    undefined,
    requestConfig
  )
  unwrapResponse(response.data)
}

export async function syncUpstreamProvider(id: number): Promise<void> {
  const response = await api.post<ApiResponse>(
    `/api/upstream-providers/${id}/sync`,
    undefined,
    requestConfig
  )
  unwrapResponse(response.data)
}

export async function syncAllUpstreamProviders(): Promise<void> {
  const response = await api.post<ApiResponse>(
    '/api/upstream-providers/sync-all',
    undefined,
    requestConfig
  )
  unwrapResponse(response.data)
}

export async function getUpstreamProviderGroups(
  id: number
): Promise<UpstreamProviderGroups> {
  const response = await api.get<ApiResponse<UpstreamProviderGroups>>(
    `/api/upstream-providers/${id}/groups`,
    requestConfig
  )
  return unwrapResponse(response.data)
}

export async function getUpstreamGroupComparison(): Promise<unknown> {
  const response = await api.get<ApiResponse<unknown>>(
    '/api/upstream-providers/groups/compare',
    requestConfig
  )
  return unwrapResponse(response.data)
}

export async function provisionUpstreamGroups(
  id: number,
  payload: ProvisionUpstreamGroupsPayload
): Promise<ProvisionUpstreamGroupsResult> {
  const response = await api.post<ApiResponse<ProvisionUpstreamGroupsResult>>(
    `/api/upstream-providers/${id}/provision`,
    payload,
    requestConfig
  )
  return unwrapResponse(response.data)
}
