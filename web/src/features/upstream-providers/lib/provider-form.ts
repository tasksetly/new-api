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
import { z } from 'zod'

export function getUpstreamProviderFormSchema(
  t: TFunction,
  isEditing: boolean
) {
  return z
    .object({
      name: z.string().trim().min(1, t('Provider name is required')),
      type: z.enum(['sub2api', 'codego', 'newapi']),
      base_url: z.string().trim().url(t('Enter a valid upstream URL')),
      username: z.string().trim(),
      password: z.string(),
      token: z.string(),
      refresh_token: z.string(),
      totp_secret: z.string(),
      upstream_user_id: z.string().trim(),
      rate_correction: z
        .number()
        .finite(t('Rate correction must be a number'))
        .positive(t('Rate correction must be greater than zero')),
      sync_enabled: z.boolean(),
    })
    .superRefine((values, context) => {
      if (
        !isEditing &&
        values.type !== 'newapi' &&
        !values.token &&
        !(values.type === 'sub2api' && values.refresh_token) &&
        (!values.username || !values.password)
      ) {
        context.addIssue({
          code: 'custom',
          path: ['token'],
          message: t('Enter credentials or an access token'),
        })
      }
      if (!isEditing && values.type === 'newapi' && !values.token) {
        context.addIssue({
          code: 'custom',
          path: ['token'],
          message: t('Enter a NewAPI management access token'),
        })
      }
      if (
        !isEditing &&
        values.type === 'codego' &&
        values.token &&
        !values.upstream_user_id
      ) {
        context.addIssue({
          code: 'custom',
          path: ['upstream_user_id'],
          message: t('Enter the CodeGo-Api user ID for an access token'),
        })
      }
    })
}

export type UpstreamProviderFormValues = z.infer<
  ReturnType<typeof getUpstreamProviderFormSchema>
>
