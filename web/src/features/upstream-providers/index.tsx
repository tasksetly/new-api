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
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'

import {
  deleteUpstreamProvider,
  getUpstreamProviderErrorMessage,
  getUpstreamProviders,
  syncAllUpstreamProviders,
  syncUpstreamProvider,
  testUpstreamProvider,
} from './api'
import { ProviderFormDialog } from './components/provider-form-dialog'
import { ProviderGroupsDialog } from './components/provider-groups-dialog'
import { ProviderTable } from './components/provider-table'
import type { UpstreamProvider } from './types'

const PAGE_SIZE = 20

export function UpstreamProviders() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [formOpen, setFormOpen] = useState(false)
  const [editingProvider, setEditingProvider] =
    useState<UpstreamProvider | null>(null)
  const [groupsProvider, setGroupsProvider] = useState<UpstreamProvider | null>(
    null
  )
  const [deleteTarget, setDeleteTarget] = useState<UpstreamProvider | null>(
    null
  )

  const providersQuery = useQuery({
    queryKey: ['upstream-providers', page, PAGE_SIZE],
    queryFn: () => getUpstreamProviders({ p: page, page_size: PAGE_SIZE }),
    placeholderData: (previousData) => previousData,
  })

  const invalidateProviders = () =>
    queryClient.invalidateQueries({ queryKey: ['upstream-providers'] })

  const syncProvider = useMutation({
    mutationFn: syncUpstreamProvider,
    onSuccess: () => {
      invalidateProviders()
      toast.success(t('Upstream provider synchronized successfully'))
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(
          error,
          t('Failed to synchronize upstream provider')
        )
      )
    },
  })
  const syncAllProviders = useMutation({
    mutationFn: syncAllUpstreamProviders,
    onSuccess: () => {
      invalidateProviders()
      toast.success(
        t('All enabled upstream providers synchronized successfully')
      )
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(
          error,
          t('Failed to synchronize upstream providers')
        )
      )
    },
  })
  const testProvider = useMutation({
    mutationFn: testUpstreamProvider,
    onSuccess: () => {
      toast.success(t('Connection successful'))
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(error, t('Connection failed'))
      )
    },
  })
  const deleteProvider = useMutation({
    mutationFn: deleteUpstreamProvider,
    onSuccess: () => {
      invalidateProviders()
      toast.success(t('Upstream provider deleted successfully'))
      setDeleteTarget(null)
    },
    onError: (error) => {
      toast.error(
        getUpstreamProviderErrorMessage(
          error,
          t('Failed to delete upstream provider')
        )
      )
    },
  })

  const providerPage = providersQuery.data
  const providers = providerPage?.items ?? []
  const pageInfo = providerPage?.page_info ?? providerPage
  const total = pageInfo?.total ?? 0
  const pageSize = Math.max(1, pageInfo?.page_size ?? PAGE_SIZE)
  const currentPage = pageInfo?.page ?? page
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  const openCreateDialog = () => {
    setEditingProvider(null)
    setFormOpen(true)
  }

  const openEditDialog = (provider: UpstreamProvider) => {
    setEditingProvider(provider)
    setFormOpen(true)
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Upstream Management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            onClick={() => syncAllProviders.mutate()}
            disabled={syncAllProviders.isPending}
          >
            <RefreshCw
              data-icon='inline-start'
              className={
                syncAllProviders.isPending ? 'animate-spin' : undefined
              }
            />
            {syncAllProviders.isPending ? t('Syncing...') : t('Sync All')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='space-y-4'>
            {providersQuery.isError && (
              <div
                role='alert'
                className='border-destructive/30 bg-destructive/10 text-destructive rounded-lg border px-3 py-2 text-sm'
              >
                {getUpstreamProviderErrorMessage(
                  providersQuery.error,
                  t('Failed to load upstream providers')
                )}
              </div>
            )}
            <ProviderTable
              providers={providers}
              isLoading={providersQuery.isLoading}
              syncingProviderID={
                syncProvider.isPending ? syncProvider.variables : undefined
              }
              testingProviderID={
                testProvider.isPending ? testProvider.variables : undefined
              }
              onCreate={openCreateDialog}
              onEdit={openEditDialog}
              onDelete={setDeleteTarget}
              onSync={(provider) => syncProvider.mutate(provider.id)}
              onTest={(provider) => testProvider.mutate(provider.id)}
              onGroups={setGroupsProvider}
            />
            {totalPages > 1 && (
              <div className='flex items-center justify-end gap-3'>
                <span className='text-muted-foreground text-sm'>
                  {t('Page {{current}} of {{total}}', {
                    current: currentPage,
                    total: totalPages,
                  })}
                </span>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => setPage((current) => current - 1)}
                  disabled={page <= 1 || providersQuery.isFetching}
                >
                  {t('Previous')}
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => setPage((current) => current + 1)}
                  disabled={page >= totalPages || providersQuery.isFetching}
                >
                  {t('Next')}
                </Button>
              </div>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ProviderFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        provider={editingProvider}
      />
      <ProviderGroupsDialog
        open={groupsProvider !== null}
        onOpenChange={(open) => !open && setGroupsProvider(null)}
        provider={groupsProvider}
      />
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('Delete Upstream Provider')}
        desc={t(
          'Delete "{{name}}"? Existing channels will remain, but they can no longer synchronize with this upstream provider.',
          { name: deleteTarget?.name ?? '' }
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() => {
          if (deleteTarget) deleteProvider.mutate(deleteTarget.id)
        }}
        isLoading={deleteProvider.isPending}
      />
    </>
  )
}
