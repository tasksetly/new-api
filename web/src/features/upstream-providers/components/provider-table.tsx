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
import { Layers3, Pencil, PlugZap, RefreshCw, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuGroup,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu'
import {
  formatCurrencyUSD,
  formatNumber,
  formatTimestampToDate,
} from '@/lib/format'

import type { UpstreamProvider } from '../types'

type ProviderTableProps = {
  providers: UpstreamProvider[]
  isLoading: boolean
  syncingProviderID?: number
  testingProviderID?: number
  onCreate: () => void
  onEdit: (provider: UpstreamProvider) => void
  onDelete: (provider: UpstreamProvider) => void
  onSync: (provider: UpstreamProvider) => void
  onTest: (provider: UpstreamProvider) => void
  onGroups: (provider: UpstreamProvider) => void
}

function getProviderTypeLabel(type: UpstreamProvider['type']) {
  return type === 'codego' ? 'CodeGo-Api' : 'Sub2API'
}

export function ProviderTable(props: ProviderTableProps) {
  const { t } = useTranslation()

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Manage upstream accounts, synchronized balances, group multipliers, and costs.'
          )}
        </p>
        <Button size='sm' onClick={props.onCreate}>
          {t('Add Upstream Provider')}
        </Button>
      </div>

      <StaticDataTable<UpstreamProvider>
        data={props.providers}
        getRowKey={(provider) => provider.id}
        emptyContent={
          props.isLoading
            ? t('Loading upstream providers...')
            : t('No upstream providers configured yet.')
        }
        emptyClassName='text-sm'
        columns={[
          {
            id: 'name',
            header: t('Name'),
            cellClassName: 'min-w-40 font-medium',
            cell: (provider) => (
              <div className='flex items-center gap-2'>
                <span>{provider.name}</span>
                {!provider.sync_enabled && (
                  <StatusBadge
                    label={t('Sync Off')}
                    variant='neutral'
                    copyable={false}
                  />
                )}
              </div>
            ),
          },
          {
            id: 'type',
            header: t('Type'),
            cell: (provider) => (
              <StatusBadge
                label={getProviderTypeLabel(provider.type)}
                variant='info'
                copyable={false}
              />
            ),
          },
          {
            id: 'base-url',
            header: t('Upstream URL'),
            cellClassName:
              'text-muted-foreground min-w-52 max-w-72 font-mono text-xs',
            cell: (provider) => provider.base_url,
          },
          {
            id: 'balance',
            header: t('Balance'),
            cell: (provider) => formatCurrencyUSD(provider.balance),
          },
          {
            id: 'rate-correction',
            header: t('Rate Correction'),
            cell: (provider) => `${formatNumber(provider.rate_correction)}×`,
          },
          {
            id: 'upstream-cost-30d',
            header: t('Upstream Cost (30d)'),
            cell: (provider) => formatCurrencyUSD(provider.upstream_cost_30d),
          },
          {
            id: 'last-sync',
            header: t('Last Synced'),
            cell: (provider) =>
              formatTimestampToDate(provider.last_sync_at ?? undefined),
          },
          {
            id: 'last-sync-error',
            header: t('Last Sync Error'),
            cellClassName: 'max-w-60',
            cell: (provider) =>
              provider.last_sync_error ? (
                <StatusBadge
                  label={provider.last_sync_error}
                  variant='danger'
                  copyable={false}
                />
              ) : (
                <span className='text-muted-foreground'>{t('None')}</span>
              ),
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'w-24 text-right',
            cellClassName: 'text-right',
            cell: (provider) => (
              <div className='flex justify-end gap-1'>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => props.onEdit(provider)}
                  aria-label={t('Edit {{name}}', { name: provider.name })}
                >
                  <Pencil />
                </Button>
                <DataTableRowActionMenu ariaLabel={t('Open provider actions')}>
                  <DropdownMenuGroup>
                    <DropdownMenuItem
                      onClick={() => props.onSync(provider)}
                      disabled={props.syncingProviderID === provider.id}
                    >
                      <RefreshCw />
                      {props.syncingProviderID === provider.id
                        ? t('Syncing...')
                        : t('Sync Now')}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onClick={() => props.onTest(provider)}
                      disabled={props.testingProviderID === provider.id}
                    >
                      <PlugZap />
                      {props.testingProviderID === provider.id
                        ? t('Testing...')
                        : t('Test Connection')}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => props.onGroups(provider)}>
                      <Layers3 />
                      {t('Manage Groups')}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      variant='destructive'
                      onClick={() => props.onDelete(provider)}
                    >
                      <Trash2 />
                      {t('Delete')}
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </DataTableRowActionMenu>
              </div>
            ),
          },
        ]}
      />
    </div>
  )
}
