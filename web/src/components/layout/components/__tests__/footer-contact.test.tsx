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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { Footer } from '../footer'

vi.mock('@/hooks/use-status', () => ({
  useStatus: vi.fn(() => ({ status: undefined })),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: vi.fn(() => ({})),
}))

const CONTACT_URL = 'https://t.me/FlexusAI'

async function renderFooter() {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => <Footer />,
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<RouterProvider router={router} />)
  await screen.findByRole('contentinfo')
}

beforeEach(() => {
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  vi.mocked(useStatus).mockReturnValue({
    status: undefined,
  } as unknown as ReturnType<typeof useStatus>)
  vi.mocked(useSystemConfig).mockReturnValue({
    systemName: 'Test Site',
  } as unknown as ReturnType<typeof useSystemConfig>)
})

afterEach(() => {
  cleanup()
})

describe('Footer contact link', () => {
  it('links to the operator contact channel with a safe external target', async () => {
    await renderFooter()

    const link = screen.getByRole('link', { name: 'Contact' })
    expect(link).toHaveAttribute('href', CONTACT_URL)
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('keeps rendering the contact link when an admin footer HTML is configured', async () => {
    vi.mocked(useSystemConfig).mockReturnValue({
      systemName: 'Test Site',
      footerHtml: '<p>Custom footer</p>',
    } as unknown as ReturnType<typeof useSystemConfig>)

    await renderFooter()

    expect(screen.getByText('Custom footer')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Contact' })).toHaveAttribute(
      'href',
      CONTACT_URL
    )
  })
})
