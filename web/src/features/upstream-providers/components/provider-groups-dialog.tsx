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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getGroups } from '@/features/users/api'
import { formatNumber } from '@/lib/format'

import {
  getUpstreamProviderErrorMessage,
  getUpstreamProviderGroups,
  provisionUpstreamGroups,
} from '../api'
import type { UpstreamProvider, UpstreamProviderGroup } from '../types'

type ProviderGroupsDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: UpstreamProvider | null
}

export function ProviderGroupsDialog(props: ProviderGroupsDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selectedGroupIDs, setSelectedGroupIDs] = useState<string[]>([])
  const [localGroup, setLocalGroup] = useState('default')
  const [namePrefix, setNamePrefix] = useState('')
  const providerID = props.provider?.id

  const providerGroupsQuery = useQuery({
    queryKey: ['upstream-provider-groups', providerID],
    queryFn: () => getUpstreamProviderGroups(providerID as number),
    enabled: props.open && providerID !== undefined,
    retry: false,
  })
  const localGroupsQuery = useQuery({
    queryKey: ['channel-groups'],
    queryFn: async () => {
      const response = await getGroups()
      if (!response.success) {
        throw new Error(response.message || t('Failed to load local groups'))
      }
      return response.data ?? []
    },
    enabled: props.open,
    staleTime: 5 * 60 * 1000,
  })

  useEffect(() => {
    if (!props.open) return
    setSelectedGroupIDs([])
    setLocalGroup('default')
    setNamePrefix('')
  }, [props.open, providerID])

  const provisionGroups = useMutation({
    mutationFn: () =>
      provisionUpstreamGroups(providerID as number, {
        remote_group_ids: selectedGroupIDs,
        local_group: selectedLocalGroup,
        ...(namePrefix.trim() ? { name_prefix: namePrefix.trim() } : {}),
      }),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['upstream-providers'] })
      queryClient.invalidateQueries({ queryKey: ['channels'] })

      const failedCount = result.items.filter((item) => item.error).length
      if (failedCount > 0) {
        toast.error(
          t('{{count}} selected group(s) could not be provisioned', {
            count: failedCount,
          })
        )
        props.onOpenChange(false)
        return
      }

      toast.success(
        t('{{count}} channel(s) created and bound to the upstream', {
          count: result.items.length,
        })
      )
      props.onOpenChange(false)
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(
          error,
          t('Failed to provision upstream channels')
        )
      )
    },
  })

  const groups = providerGroupsQuery.data?.items ?? []
  const localGroups = localGroupsQuery.data ?? []
  const selectedLocalGroup = localGroups.includes(localGroup)
    ? localGroup
    : (localGroups[0] ?? '')
  const localGroupItems = localGroups.map((group) => ({
    value: group,
    label: group,
  }))

  const toggleGroup = (groupID: string, checked: boolean) => {
    setSelectedGroupIDs((current) => {
      if (checked) return [...current, groupID]
      return current.filter((id) => id !== groupID)
    })
  }

  const renderRate = (value: number | null | undefined) =>
    value == null ? '-' : `${formatNumber(value)}×`

  let groupsContent: ReactNode
  if (providerGroupsQuery.isLoading) {
    groupsContent = (
      <p className='text-muted-foreground py-8 text-center text-sm'>
        {t('Loading upstream groups...')}
      </p>
    )
  } else if (providerGroupsQuery.isError) {
    groupsContent = (
      <p className='text-destructive py-8 text-center text-sm'>
        {getUpstreamProviderErrorMessage(
          providerGroupsQuery.error,
          t('Failed to load upstream groups')
        )}
      </p>
    )
  } else {
    groupsContent = (
      <StaticDataTable<UpstreamProviderGroup>
        data={groups}
        getRowKey={(group) => group.remote_group_id}
        emptyContent={t('No synchronized upstream groups found.')}
        emptyClassName='text-sm'
        columns={[
          {
            id: 'select',
            header: t('Select'),
            className: 'w-12',
            cell: (group) => (
              <Checkbox
                checked={selectedGroupIDs.includes(group.remote_group_id)}
                disabled={group.is_dynamic}
                onCheckedChange={(checked) =>
                  toggleGroup(group.remote_group_id, checked === true)
                }
                aria-label={t('Select {{name}}', { name: group.name })}
              />
            ),
          },
          {
            id: 'name',
            header: t('Upstream Group'),
            cellClassName: 'font-medium',
            cell: (group) => group.name,
          },
          {
            id: 'description',
            header: t('Description'),
            cellClassName: 'text-muted-foreground max-w-48',
            cell: (group) => group.description || '-',
          },
          {
            id: 'base-rate',
            header: t('Base Rate'),
            cell: (group) => renderRate(group.rate_multiplier),
          },
          {
            id: 'effective-rate',
            header: t('Effective Rate'),
            cell: (group) => renderRate(group.effective_rate_multiplier),
          },
          {
            id: 'corrected-rate',
            header: t('Corrected Rate'),
            cell: (group) =>
              group.is_dynamic
                ? t('Dynamic')
                : renderRate(group.corrected_rate),
          },
          {
            id: 'channels',
            header: t('Bound Channels'),
            cell: (group) => formatNumber(group.channel_count),
          },
        ]}
      />
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Upstream Groups')}
      description={t(
        'Choose remote groups to create disabled local channels with the synchronized cost multiplier.'
      )}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={provisionGroups.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={() => provisionGroups.mutate()}
            disabled={
              provisionGroups.isPending ||
              selectedGroupIDs.length === 0 ||
              localGroupsQuery.isLoading ||
              selectedLocalGroup === ''
            }
          >
            {provisionGroups.isPending
              ? t('Creating Channels...')
              : t('Create Bound Channels')}
          </Button>
        </>
      }
    >
      <div className='space-y-5'>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='grid gap-2'>
            <label
              className='text-sm font-medium'
              htmlFor='local-channel-group'
            >
              {t('Local Channel Group')}
            </label>
            <Select
              items={localGroupItems}
              value={selectedLocalGroup}
              onValueChange={(value) => value && setLocalGroup(value)}
            >
              <SelectTrigger id='local-channel-group' className='w-full'>
                <SelectValue placeholder={t('Select a local group')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {localGroups.map((group) => (
                    <SelectItem key={group} value={group}>
                      {group}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            {localGroupsQuery.isError && (
              <p className='text-destructive text-sm'>
                {getUpstreamProviderErrorMessage(
                  localGroupsQuery.error,
                  t('Failed to load local groups')
                )}
              </p>
            )}
          </div>
          <div className='grid gap-2'>
            <label
              className='text-sm font-medium'
              htmlFor='channel-name-prefix'
            >
              {t('Channel Name Prefix')}
            </label>
            <Input
              id='channel-name-prefix'
              value={namePrefix}
              onChange={(event) => setNamePrefix(event.target.value)}
              placeholder={t('Optional')}
            />
          </div>
        </div>

        <p className='text-muted-foreground text-sm'>
          {t(
            'New channels are disabled until you review and enable them from Channels.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Estimated upstream cost uses local consume logs and the latest synchronized group multiplier.'
          )}
        </p>

        {groupsContent}
      </div>
    </Dialog>
  )
}
