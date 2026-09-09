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
import type { TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'

import { getUpstreamProviderFormSchema } from '../lib/provider-form'

const translate = ((key: string) => key) as TFunction

const validValues = {
  name: 'Primary upstream',
  type: 'sub2api' as const,
  base_url: 'https://upstream.example.com',
  username: 'admin',
  password: 'password',
  token: '',
  refresh_token: '',
  totp_secret: '',
  upstream_user_id: '',
  rate_correction: 1,
  sync_enabled: true,
}

describe('upstream provider form', () => {
  it('requires a token or a complete username and password when creating', () => {
    const result = getUpstreamProviderFormSchema(translate, false).safeParse({
      ...validValues,
      password: '',
    })

    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues).toContainEqual(
        expect.objectContaining({
          path: ['token'],
          message: 'Enter credentials or an access token',
        })
      )
    }
  })

  it('allows blank credentials when editing so saved secrets are retained', () => {
    const result = getUpstreamProviderFormSchema(translate, true).safeParse({
      ...validValues,
      username: '',
      password: '',
      token: '',
    })

    expect(result.success).toBe(true)
  })
})
