// @vitest-environment jsdom
import { createElement } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { HubContext, type HubState } from '../context'
import type { Recipe } from '../types'
import { TransferDialog } from './TransferDialog'

const picoUID = 'a'.repeat(32)
const zAddress = 'http://192.168.20.35'
const recipes = [
  { id: 'pale', name: 'Pale ale', revision: 4 },
  { id: 'amber', name: 'Amber ale', revision: 2 },
].map((r) => ({ ...r, source_model: 'pico-pro', steps: [], ingredients: [] }) as unknown as Recipe)
const [pale, amber] = recipes
const program = {
  id: 'native',
  name: 'Compiled mash',
  steps: [{ name: 'Mash', location: 2, minutes: 45, drain_minutes: 1, target_f: 150 }],
}

const fetchMock = vi.fn<typeof fetch>()
const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
const originalShow = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true
    },
  })
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  cleanup()
  fetchMock.mockReset()
  vi.unstubAllGlobals()
  if (originalShow) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShow)
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
})

async function chooseTarget(
  id: string,
  name: string,
  handle: (path: string, data: Record<string, unknown>) => Response
) {
  const calls: { path: string; body: Record<string, unknown> }[] = []
  fetchMock.mockImplementation(async (url, opts) => {
    const path = String(url)
    if (opts?.method === 'POST') {
      const body = JSON.parse(String(opts.body))
      calls.push({ path, body })
      return handle(path, body)
    }
    if (path === '/api/v1/stock-picos')
      return json({ machines: [{ uid: picoUID, name: 'Kitchen Pico', model: 'pico-c' }] })
    if (path === '/api/v1/stock-z-programs')
      return json({ machines: [{ id: 'module', name: 'Z downstairs', base_url: zAddress }] })
    if (path === '/api/v1/brew-groups')
      return json({
        groups: [
          { id: 'class', name: 'Brew class', revision: 3, device_ids: ['stock-z:module', 'other'] },
        ],
      })
    throw Error('Unexpected GET ' + path)
  })
  const hub = {
    session: { demo: false },
    devices: [],
    recipes,
    profiles: [],
    unit: 'F',
  } as unknown as HubState
  render(
    createElement(
      HubContext.Provider,
      { value: hub },
      createElement(TransferDialog, { recipes, initialSelection: [pale.id], onClose: vi.fn() })
    )
  )
  const user = userEvent.setup()
  await screen.findByRole('option', { name: name + ' · Original firmware' })
  await user.selectOptions(screen.getByLabelText('Transfer to machine'), id)
  await screen.findByRole('dialog', { name: 'Send to ' + name })
  expect(calls).toHaveLength(0)
  return { user, calls }
}

it('publishes only the reviewed Pico recipe, tag and revision from the normal target chooser', async () => {
  let previews = 0
  const { user, calls } = await chooseTarget('stock-pico:' + picoUID, 'Kitchen Pico', (_, data) => {
    return data.publish
      ? json({ items: [] })
      : json({
          items: [{ recipe_id: pale.id, program }],
          review_hash: 'menu-review-' + ++previews,
        })
  })
  const tag = screen.getByLabelText(/RFID tag/)
  await user.type(tag, 'AABBCCDD')
  await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
  await screen.findByRole('button', { name: 'Publish to brewer menu' })
  expect(calls).toHaveLength(1)
  expect(calls[0]).toEqual({
    path: '/api/v1/stock-picos/' + picoUID + '/recipes',
    body: {
      recipes: [{ recipe_id: pale.id, revision: pale.revision, rfid: 'AABBCCDD' }],
      publish: false,
    },
  })
  expect(screen.getByRole('heading', { name: program.name })).toBeTruthy()
  await user.clear(tag)
  expect(screen.queryByRole('button', { name: 'Publish to brewer menu' })).toBeNull()
  await user.type(tag, '00112233')
  await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
  await user.click(await screen.findByRole('button', { name: 'Publish to brewer menu' }))
  await screen.findByText(/Published. Open the recipe menu/)
  expect(calls).toHaveLength(3)
  expect(calls[2].body).toEqual({
    recipes: [{ recipe_id: pale.id, revision: pale.revision, rfid: '00112233' }],
    publish: true,
    review_hash: 'menu-review-2',
  })
})

it('requires a fresh Z preview and address confirmation after changing recipes, and offers only the acknowledged transfer', async () => {
  const { user, calls } = await chooseTarget('stock-z:module', 'Z downstairs', (path, data) => {
    if (path.endsWith('/group-offers')) return json({ offered: 1 })
    if (data.confirm_replace) return json({ transfer_id: 'ack-of-amber' })
    const id = data.recipe_id
    return json({
      program_name: id === 'amber' ? 'Amber Program' : 'Pale Program',
      step_count: 2,
      temperature_limits:
        id === 'pale' ? [{ step: 'Boil', requested_c: 96, maximum_c: 94.45 }] : [],
      program_hash: 'program-' + id,
      review_hash: 'review-' + id,
    })
  })
  const preview = () => user.click(screen.getByRole('button', { name: 'Preview recipe' }))
  const replace = () => screen.findByRole('button', { name: 'Replace working recipe' })
  const confirmation = () =>
    screen.getByRole('checkbox', { name: /Z downstairs at http:\/\/192\.168\.20\.35 is idle/ })
  await preview()
  expect(((await replace()) as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByText(/Boil requests 204.8 °F. The Z limits it to 202 °F/)).toBeTruthy()
  expect(calls[0].body).toEqual({
    recipe_id: pale.id,
    revision: pale.revision,
    confirm_replace: false,
  })
  await user.click(confirmation())
  await user.click(screen.getByRole('radio', { name: amber.name }))
  expect(screen.queryByRole('button', { name: 'Replace working recipe' })).toBeNull()
  await preview()
  expect(((await replace()) as HTMLButtonElement).disabled).toBe(true)
  expect((confirmation() as HTMLInputElement).checked).toBe(false)
  expect(screen.queryByRole('heading', { name: 'Kettle temperature' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Offer recipe to Brew class' })).toBeNull()
  await user.click(confirmation())
  await user.click(await replace())
  const offer = await screen.findByRole('button', { name: 'Offer recipe to Brew class' })
  expect(calls).toHaveLength(3)
  expect(calls[2]).toEqual({
    path: '/api/v1/stock-z-programs/module/recipes',
    body: {
      recipe_id: amber.id,
      revision: amber.revision,
      confirm_replace: true,
      program_hash: 'program-amber',
      review_hash: 'review-amber',
    },
  })
  await user.click(offer)
  expect(calls.at(-1)).toEqual({
    path: '/api/v1/stock-z-programs/module/group-offers',
    body: {
      transfer_id: 'ack-of-amber',
      group_id: 'class',
      group_revision: 3,
    },
  })
})
