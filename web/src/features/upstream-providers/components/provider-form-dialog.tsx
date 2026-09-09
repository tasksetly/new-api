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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo } from 'react'
import { type Resolver, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  createUpstreamProvider,
  getUpstreamProviderErrorMessage,
  updateUpstreamProvider,
} from '../api'
import {
  getUpstreamProviderFormSchema,
  type UpstreamProviderFormValues,
} from '../lib/provider-form'
import type {
  UpstreamProvider,
  UpstreamProviderPayload,
  UpstreamProviderType,
} from '../types'

type ProviderFormDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: UpstreamProvider | null
}

const PROVIDER_FORM_ID = 'upstream-provider-form'

const providerTypes: { value: UpstreamProviderType; label: string }[] = [
  { value: 'sub2api', label: 'Sub2API' },
  { value: 'codego', label: 'CodeGo-Api' },
]

export function ProviderFormDialog(props: ProviderFormDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isEditing = props.provider !== null
  const formSchema = useMemo(
    () => getUpstreamProviderFormSchema(t, isEditing),
    [isEditing, t]
  )

  const form = useForm<UpstreamProviderFormValues>({
    resolver: zodResolver(
      formSchema
    ) as unknown as Resolver<UpstreamProviderFormValues>,
    defaultValues: {
      name: '',
      type: 'sub2api',
      base_url: '',
      username: '',
      password: '',
      token: '',
      refresh_token: '',
      totp_secret: '',
      upstream_user_id: '',
      rate_correction: 1,
      sync_enabled: true,
    },
  })

  useEffect(() => {
    if (!props.open) return

    form.reset({
      name: props.provider?.name ?? '',
      type: props.provider?.type ?? 'sub2api',
      base_url: props.provider?.base_url ?? '',
      username: props.provider?.username ?? '',
      password: '',
      token: '',
      refresh_token: '',
      totp_secret: '',
      upstream_user_id: props.provider?.upstream_user_id ?? '',
      rate_correction: props.provider?.rate_correction ?? 1,
      sync_enabled: props.provider?.sync_enabled ?? true,
    })
  }, [form, props.open, props.provider])

  const saveProvider = useMutation({
    mutationFn: ({
      id,
      payload,
    }: {
      id?: number
      payload: UpstreamProviderPayload
    }) =>
      id === undefined
        ? createUpstreamProvider(payload)
        : updateUpstreamProvider(id, payload),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['upstream-providers'] })
      toast.success(
        isEditing
          ? t('Upstream provider updated successfully')
          : t('Upstream provider created successfully')
      )
      props.onOpenChange(false)
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(
          error,
          t('Failed to save upstream provider')
        )
      )
    },
  })

  const onSubmit = (values: UpstreamProviderFormValues) => {
    const payload: UpstreamProviderPayload = {
      name: values.name,
      type: values.type,
      base_url: values.base_url,
      rate_correction: values.rate_correction,
      sync_enabled: values.sync_enabled,
      ...(values.username ? { username: values.username } : {}),
      ...(values.password ? { password: values.password } : {}),
      ...(values.token ? { token: values.token } : {}),
      ...(values.refresh_token ? { refresh_token: values.refresh_token } : {}),
      ...(values.totp_secret ? { totp_secret: values.totp_secret } : {}),
      ...(values.upstream_user_id
        ? { upstream_user_id: values.upstream_user_id }
        : {}),
    }

    saveProvider.mutate({ id: props.provider?.id, payload })
  }

  let submitLabel = t('Create Upstream Provider')
  if (isEditing) submitLabel = t('Save Changes')
  if (saveProvider.isPending) submitLabel = t('Saving...')

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        isEditing ? t('Edit Upstream Provider') : t('Add Upstream Provider')
      }
      description={t(
        'Connect an upstream account to synchronize balances, groups, and costs.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={saveProvider.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form={PROVIDER_FORM_ID}
            disabled={saveProvider.isPending}
          >
            {submitLabel}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={PROVIDER_FORM_ID}
          className='space-y-5'
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Provider Name')}</FormLabel>
                  <FormControl>
                    <Input
                      autoComplete='off'
                      placeholder={t('e.g. Primary Sub2API')}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='type'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Provider Type')}</FormLabel>
                  <Select
                    items={providerTypes}
                    value={field.value}
                    onValueChange={(value) => {
                      if (value === 'sub2api' || value === 'codego') {
                        field.onChange(value)
                      }
                    }}
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {providerTypes.map((providerType) => (
                          <SelectItem
                            key={providerType.value}
                            value={providerType.value}
                          >
                            {providerType.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='base_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream URL')}</FormLabel>
                <FormControl>
                  <Input
                    inputMode='url'
                    placeholder='https://api.example.com'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('Use the base URL of the upstream management API.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='username'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Username')}</FormLabel>
                  <FormControl>
                    <Input autoComplete='username' {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='password'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Password')}</FormLabel>
                  <FormControl>
                    <Input
                      type='password'
                      autoComplete='new-password'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {isEditing
                      ? t('Leave empty to keep the saved password.')
                      : t('Required when the upstream uses password sign-in.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='refresh_token'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Refresh Token')}</FormLabel>
                  <FormControl>
                    <Input type='password' autoComplete='off' {...field} />
                  </FormControl>
                  <FormDescription>
                    {isEditing
                      ? t('Leave empty to keep the saved refresh token.')
                      : t('Optional token used to renew a Sub2API session.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='totp_secret'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('TOTP Secret')}</FormLabel>
                  <FormControl>
                    <Input type='password' autoComplete='off' {...field} />
                  </FormControl>
                  <FormDescription>
                    {isEditing
                      ? t('Leave empty to keep the saved TOTP secret.')
                      : t('Optional secret for upstream two-factor sign-in.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='token'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Access Token')}</FormLabel>
                  <FormControl>
                    <Input type='password' autoComplete='off' {...field} />
                  </FormControl>
                  <FormDescription>
                    {isEditing
                      ? t('Leave empty to keep the saved access token.')
                      : t(
                          'Use this when the upstream accepts a management token.'
                        )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='upstream_user_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Upstream User ID')}</FormLabel>
                  <FormControl>
                    <Input autoComplete='off' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Required by CodeGo-Api when using an access token.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end'>
            <FormField
              control={form.control}
              name='rate_correction'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rate Correction')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='0.0001'
                      step='0.01'
                      value={field.value}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Applied to synchronized group rate multipliers.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='sync_enabled'
              render={({ field }) => (
                <FormItem className='flex items-center gap-3 space-y-0 pb-1'>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <div className='space-y-1'>
                    <FormLabel>{t('Enable synchronization')}</FormLabel>
                    <FormDescription>
                      {t('Include this provider when syncing all upstreams.')}
                    </FormDescription>
                  </div>
                </FormItem>
              )}
            />
          </div>
        </form>
      </Form>
    </Dialog>
  )
}
