// @vitest-environment jsdom
import { createElement } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BrewGroupManager, BrewInvitations } from './BrewGroups'
import { HubContext, type HubState } from '../context'
import type { Device } from '../types'

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value: function (this: HTMLDialogElement) { this.setAttribute('open','') },
  })
})
afterEach(() => {
  cleanup()
  if(originalShowModal) Object.defineProperty(HTMLDialogElement.prototype,'showModal',originalShowModal)
  else Reflect.deleteProperty(HTMLDialogElement.prototype,'showModal')
  vi.unstubAllGlobals()
})
function brewer(id: string, model: string) {
  return { id, name: id, model, adapter: 'pico-standalone', online: true } as Device
}
const devices = [
  brewer('Pico S', 'pico-s'),
  brewer('Z1', 'z-series'),
  brewer('Z2', 'z-series'),
  brewer('Fermentation monitor', 'picoferm'),
]
function mount(child: ReturnType<typeof createElement>) {
  return render(
    createElement(HubContext.Provider, { value: { notify: vi.fn() } as unknown as HubState }, child)
  )
}

it('groups only explicitly selected brewers and accepts a stock Zymatic menu member', async () => {
  const fetcher = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) =>
    Response.json(
      init?.method === 'POST'
        ? {}
        : {
            groups: [],
            stock_candidates: [{ id: 'stock-zymatic:ABC', name: 'Zymatic ABC' }],
          }
    )
  )
  vi.stubGlobal('fetch', fetcher)
  mount(createElement(BrewGroupManager, { devices }))
  fireEvent.click(await screen.findByRole('button', { name: 'Group machines' }))
  fireEvent.change(screen.getByLabelText('Group name'), { target: { value: 'Brew day' } })
  expect(screen.queryByRole('checkbox', { name: /fermentation/i })).toBeNull()
  fireEvent.click(screen.getByRole('checkbox', { name: 'Pico S' }))
  expect((screen.getByRole('button', { name: 'Group' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(await screen.findByRole('checkbox', { name: /Zymatic ABC/ }))
  fireEvent.click(screen.getByRole('button', { name: 'Group' }))
  await waitFor(() => expect(fetcher.mock.calls.some(([, v]) => v?.method === 'POST')).toBe(true))
  const submit = fetcher.mock.calls.find(([, v]) => v?.method === 'POST')![1]!
  expect(JSON.parse(String(submit.body))).toMatchObject({
    name: 'Brew day',
    device_ids: ['Pico S', 'stock-zymatic:ABC'],
  })
})

it('stages a stock Zymatic invitation only after an explicit click', async () => {
  const invite = {
    id: 'a123',
    source_name: 'Z1',
    group_name: 'Brew day',
    target_device_id: 'stock-zymatic:ABC',
    menu_serial: 'ABC',
    recipe_name: 'Amber',
    state: 'pending',
    action: 'brew',
    expires_at: new Date(Date.now() + 100000).toISOString(),
  }
  const fetcher = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
    Response.json({ invites: [invite] })
  )
  vi.stubGlobal('fetch', fetcher)
  mount(createElement(BrewInvitations, { devices }))
  const accept = await screen.findByRole('button', { name: 'Add to brewer menu' })
  expect(fetcher.mock.calls.every(([, v]) => v?.method !== 'POST')).toBe(true)
  fireEvent.click(accept)
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([url, v]) =>
          String(url).endsWith('/stock-zymatic:ABC/brew-invite/a123/respond') &&
          v?.body === '{"accept":true}'
      )
    ).toBe(true)
  )
})
