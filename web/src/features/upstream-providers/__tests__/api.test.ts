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
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getUpstreamProviders,
  provisionUpstreamGroups,
  syncUpstreamProvider,
} from '../api'

const { get, post, put, remove } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  remove: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: { get, post, put, delete: remove },
}))

describe('upstream provider API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    remove.mockReset()
  })

  it('requests paginated providers from the management endpoint', async () => {
    get.mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [],
          page_info: { total: 0, page: 1, page_size: 20 },
        },
      },
    })

    await expect(
      getUpstreamProviders({ p: 1, page_size: 20 })
    ).resolves.toMatchObject({ items: [] })
    expect(get).toHaveBeenCalledWith('/api/upstream-providers', {
      skipBusinessError: true,
      skipErrorHandler: true,
      params: { p: 1, page_size: 20 },
    })
  })

  it('provisions selected remote groups into one local channel group', async () => {
    const payload = {
      remote_group_ids: ['default', 'vip'],
      local_group: 'default',
      name_prefix: 'sub2api',
    }
    post.mockResolvedValue({ data: { success: true, data: { items: [] } } })

    await provisionUpstreamGroups(42, payload)

    expect(post).toHaveBeenCalledWith(
      '/api/upstream-providers/42/provision',
      payload,
      expect.objectContaining({
        skipBusinessError: true,
        skipErrorHandler: true,
      })
    )
  })

  it('rejects a failed synchronization response for the caller to display', async () => {
    post.mockResolvedValue({
      data: { success: false, message: 'upstream unavailable' },
    })

    await expect(syncUpstreamProvider(42)).rejects.toThrow(
      'upstream unavailable'
    )
  })
})
