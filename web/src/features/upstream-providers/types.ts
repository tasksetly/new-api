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

export type UpstreamProviderType = 'sub2api' | 'codego'

export type ApiResponse<T = undefined> = {
  success: boolean
  message?: string
  data?: T
}

export type PageInfo = {
  total?: number
  page?: number
  page_size?: number
}

export type UpstreamProvider = {
  id: number
  name: string
  type: UpstreamProviderType
  base_url: string
  username?: string | null
  upstream_user_id?: string | null
  rate_correction: number
  balance?: number | null
  frozen_balance?: number | null
  upstream_cost_30d?: number | null
  upstream_cost_at?: number | null
  upstream_concurrency?: number | null
  status?: string | null
  last_sync_at?: number | null
  last_sync_error?: string | null
  sync_enabled: boolean
  created_at?: number | null
  updated_at?: number | null
  channel_count: number
  group_count: number
}

export type UpstreamProviderPage = {
  items: UpstreamProvider[]
  page_info?: PageInfo
  total?: number
  page?: number
  page_size?: number
}

export type UpstreamProviderGroup = {
  id: number
  remote_group_id: string
  name: string
  description?: string | null
  rate_multiplier?: number | null
  effective_rate_multiplier?: number | null
  is_dynamic: boolean
  corrected_rate?: number | null
  channel_count: number
  last_synced_at?: number | null
}

export type UpstreamProviderGroups = {
  items: UpstreamProviderGroup[]
}

export type UpstreamProviderPayload = {
  name: string
  type: UpstreamProviderType
  base_url: string
  username?: string
  password?: string
  token?: string
  refresh_token?: string
  totp_secret?: string
  upstream_user_id?: string
  rate_correction: number
  sync_enabled: boolean
}

export type ProvisionUpstreamGroupsPayload = {
  remote_group_ids: string[]
  local_group: string
  name_prefix?: string
}

export type ProvisionedUpstreamChannel = {
  remote_group_id: string
  remote_group_name: string
  channel_id?: number
  channel_name?: string
  error?: string
}

export type ProvisionUpstreamGroupsResult = {
  items: ProvisionedUpstreamChannel[]
}
