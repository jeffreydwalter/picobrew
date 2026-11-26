// @vitest-environment jsdom
import { createElement } from 'react'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import * as api from '../lib/api'
import { HubContext, type HubState } from '../context'
import type { Recipe } from '../types'
import { StockZymaticTransfer, offlineTarget } from './StockZymaticTransfer'
import { StockSetupGuide } from './StockSetupGuide'
import { TransferDialog } from './TransferDialog'

const program = {
  id: '0123456789abcdef0123456789abcdef',
  name: 'Offline ale',
  steps: [{ name: 'Mash', location: 1, target_f: 150, minutes: 60, drain_minutes: 2 }],
}
const saved = {
  id: 'library-ale',
  revision: 3,
  name: 'Library ale',
  source_model: 'zymatic',
  steps: [],
  ingredients: [],
  native_program: program,
} as unknown as Recipe
const props = { recipes: [saved], initialSelection: [saved.id], back: vi.fn(), close: vi.fn() }
const fetchMock = vi.fn<typeof fetch>()
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })
const showModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
beforeEach(() => {
  localStorage.setItem('stock-zymatic-serial', 'AAAAAAAAAAAA')
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true
    },
  })
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockImplementation(async (url, opts) =>
    opts?.method === 'POST'
      ? json({ items: [{ recipe_id: saved.id, revision: saved.revision, program }] })
      : json({ machine: String(url).split('/').at(-2), items: [] })
  )
})
afterEach(() => {
  cleanup()
  localStorage.clear()
  fetchMock.mockReset()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  if (showModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModal)
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
})
it('downloads an offline program from the normal target chooser without a brewer, serial, or menu requests', async () => {
  const hub = {
    devices: [],
    profiles: [],
    recipes: [saved],
    session: { demo: false },
  } as unknown as HubState
  const click = userEvent.setup()
  const save = vi.spyOn(api, 'saveBlob').mockImplementation(() => {})
  render(
    createElement(
      HubContext.Provider,
      { value: hub },
      createElement(TransferDialog, {
        recipes: [saved],
        initialSelection: [saved.id],
        onClose: vi.fn(),
      })
    )
  )
  await click.selectOptions(screen.getByLabelText('Transfer to machine'), offlineTarget)
  expect(screen.queryByLabelText(/Zymatic serial number/)).toBeNull()
  await click.click(screen.getByRole('button', { name: 'Preview offline programs' }))
  await click.click(await screen.findByRole('button', { name: 'Download offline program' }))
  const zymaticRequests = fetchMock.mock.calls.filter(([url]) =>
    String(url).includes('/stock-zymatics')
  )
  expect(zymaticRequests.map(([url]) => String(url))).toEqual(['/api/v1/stock-zymatics/preview'])
  expect(fetchMock.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(JSON.parse(String(zymaticRequests[0][1]?.body))).toEqual({
    recipes: [{ recipe_id: saved.id, revision: 3 }],
  })
  expect(screen.queryByRole('button', { name: 'Publish to brewer menu' })).toBeNull()
  const [blob, filename] = save.mock.calls[0]
  expect(filename).toBe('zymatic-' + program.id + '.json')
  const contents = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = reject
    reader.onload = () => resolve(String(reader.result))
    reader.readAsText(blob)
  })
  expect(JSON.parse(contents)).toEqual({ model: 'zymatic', ...program })
})
it('starts USB setup with the exact reviewed offline program without a file or serial', async () => {
  const click = userEvent.setup()
  const port = 'serial-' + 'ab'.repeat(16)
  fetchMock.mockImplementation(async (url) => {
    if (String(url).endsWith('/usb/devices'))
      return json({
        available: true,
        devices: [{ port, models: ['zymatic'], label: 'Test Zymatic' }],
      })
    if (String(url).endsWith('/usb/jobs'))
      return json({
        id: 'job',
        action: 'install',
        model: 'zymatic',
        state: 'waiting',
        confirmation: 'nonce',
        offline_program: program,
        message: 'Connect the idle Zymatic.',
      })
    return json({ items: [{ recipe_id: saved.id, revision: saved.revision, program }] })
  })
  render(
    createElement(
      HubContext.Provider,
      { value: { devices: [], recipes: [saved] } as unknown as HubState },
      createElement(StockZymaticTransfer, { ...props, offline: true })
    )
  )
  await click.click(screen.getByRole('button', { name: 'Preview offline programs' }))
  await click.click(await screen.findByRole('button', { name: 'Use in USB setup' }))
  await click.click(await screen.findByRole('button', { name: 'Install standalone firmware' }))
  await screen.findByText('Connect the idle Zymatic.')
  const req = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/usb/jobs'))
  expect(JSON.parse(String(req?.[1]?.body))).toEqual({
    action: 'install',
    model: 'zymatic',
    port,
    offline_program: { ...program, model: 'zymatic' },
  })
})

it('rejects a mismatched file and sends an exported Zymatic file only during installation', async () => {
  const click = userEvent.setup()
  const port = 'serial-' + 'ac'.repeat(16)
  fetchMock.mockImplementation(async (url) =>
    String(url).endsWith('/usb/devices')
      ? json({ available: true, devices: [{ port, label: 'Test Zymatic', models: ['zymatic'] }] })
      : json({
          id: 'job',
          action: 'install',
          model: 'zymatic',
          state: 'waiting',
          confirmation: 'nonce',
          message: 'Confirm installation.',
        })
  )
  render(
    createElement(
      HubContext.Provider,
      { value: {} as HubState },
      createElement(StockSetupGuide, { onClose: vi.fn() })
    )
  )
  const input = await screen.findByLabelText(/Offline recipe \(optional\)/)
  const install = screen.getByRole('button', { name: 'Install standalone firmware' })
  const file = (model: string) =>
    new File([JSON.stringify({ ...program, model })], 'recipe.json', { type: 'application/json' })
  await click.upload(input, file('pico-s'))
  await click.click(install)
  await screen.findByText('Choose an offline program exported for the Zymatic.')
  expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/usb/jobs'))).toBe(false)
  await click.selectOptions(screen.getByLabelText('USB operation'), 'backup')
  expect(screen.queryByLabelText(/Offline recipe/)).toBeNull()
  await click.selectOptions(screen.getByLabelText('USB operation'), 'install')
  await click.upload(screen.getByLabelText(/Offline recipe \(optional\)/), file('zymatic'))
  await click.click(screen.getByRole('button', { name: 'Install standalone firmware' }))
  await screen.findByText('Confirm installation.')
  const req = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/usb/jobs'))
  expect(JSON.parse(String(req?.[1]?.body)).offline_program).toEqual({
    ...program,
    model: 'zymatic',
  })
})

it('routes both preview and publish exclusively to the selected paired brewer', async () => {
  const click = userEvent.setup()
  const serial = '012345ABCDEF'
  render(
    createElement(StockZymaticTransfer, {
      ...props,
      paired: { name: 'Cellar Zymatic', serial: serial.toLowerCase() },
    })
  )
  expect((screen.getByLabelText(/Zymatic serial number/) as HTMLInputElement).value).toBe(serial)
  await click.click(screen.getByRole('button', { name: 'Preview stock menu' }))
  await click.click(await screen.findByRole('button', { name: 'Publish to brewer menu' }))
  await waitFor(() => expect(screen.getByRole('status').textContent).toContain(serial))
  expect(
    fetchMock.mock.calls.every(
      ([url]) => String(url) === '/api/v1/stock-zymatics/' + serial + '/recipes'
    )
  ).toBe(true)
  expect(
    fetchMock.mock.calls
      .filter(([, options]) => options?.method === 'POST')
      .map(([, options]) => JSON.parse(String(options?.body)).publish)
  ).toEqual([false, true])
  expect(localStorage.getItem('stock-zymatic-serial')).toBe('AAAAAAAAAAAA')
})

it('shows the stock compiler’s scaled bill and reviewed directions before publishing', async () => {
  const beer = {
    ...saved,
    native_program: undefined,
    batch_liters: 10,
    notes:
      'Hub adaptation (Primary honey). Mix the honey into the cooled wort before pitching.\n\nArchived keg procedure',
    ingredients: [
      {
        name: 'Honey',
        kind: 'fermentable',
        unit: 'kg',
        amount: 0.1,
        use: 'fermentation',
        minutes: 0,
      },
      { name: 'Hallertau', kind: 'hop', unit: 'g', amount: 20, use: 'boil', minutes: 60 },
    ],
  }
  fetchMock.mockImplementation(async (_url, opts) =>
    opts?.method === 'POST'
      ? json({
          items: [
            {
              recipe_id: beer.id,
              revision: beer.revision,
              program,
              batch_liters: 5,
              hop_slots_by_minute: { 60: 3 },
            },
          ],
        })
      : json({ machine: 'AAAAAAAAAAAA', items: [] })
  )
  render(createElement(StockZymaticTransfer, { ...props, recipes: [beer] }))
  await userEvent.setup().click(screen.getByRole('button', { name: 'Preview stock menu' }))
  await screen.findByText('Mix the honey into the cooled wort before pitching.')
  expect(screen.queryByText('Archived keg procedure')).toBeNull()
  expect(screen.getByText('Honey').closest('tr')?.textContent).toContain('50 gFermentation')
  expect(screen.getByText('Hallertau').closest('tr')?.textContent).toContain(
    '10 gHop slot 3 · 60 min before boil ends'
  )
})
