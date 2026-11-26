// @vitest-environment jsdom
import { createElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HubContext, type HubState } from '../context'
import { setCSRF } from '../lib/api'
import type { Adapted, Device, Profile, Recipe } from '../types'
import { AdaptDialog } from './AdaptDialog'

const source: Recipe = {
  id: 'imported-pale',
  name: 'Imported pale',
  kind: 'recipe',
  style: '',
  description: '',
  batch_liters: 14.0060236008,
  efficiency: 75,
  ingredients: [
    { name: 'Pale malt', kind: 'fermentable', amount: 2, unit: 'kg', use: 'mash', minutes: 0 },
  ],
  steps: [
    {
      name: 'Mash',
      kind: 'mash',
      target_c: 65.5555555,
      minutes: 60,
      drain_minutes: 2,
      location: 0,
    },
  ],
  tags: [],
  notes: '',
  revision: 4,
  created_at: '',
  updated_at: '',
}
const pro: Profile = {
  id: 'pico-pro',
  name: 'Pico Pro',
  batch_liters: 5,
  efficiency: 50,
  max_steps: 15,
  max_target_c: 100,
  max_grain_kg: 0,
  hop_slots: 0,
  control_supported: true,
  notes: '',
}
const s: Profile = {
  ...pro,
  id: 'pico-s',
  name: 'Pico S',
  batch_liters: 4.8,
  user_configured: true,
}
const z: Profile = {
  ...pro,
  id: 'z',
  name: 'Pico Z',
  batch_liters: 9.46352946,
  control_supported: false,
}
const devices = ['pico-pro', 'pico-s'].map(
  (model) =>
    ({
      id: model,
      model,
      name: model === 'pico-pro' ? 'Kitchen Pro' : 'Bench S',
      online: true,
      status: { ready: true, busy: false, stopped: false },
    }) as Device
)
const fetchMock = vi.fn<typeof fetch>()
let inventory: NonNullable<Adapted['machine_recipe']>[] = []
const onClose = vi.fn()
const dialogMethods = ['showModal', 'close'] as const
const originalDialogMethods = dialogMethods.map((name) =>
  Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, name)
)

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function plan(batch = 5, ready = false, profile = pro): Adapted {
  return {
    recipe: {
      ...source,
      batch_liters: batch,
      efficiency: profile.efficiency,
      ingredients: source.ingredients.map((item) => ({
        ...item,
        amount: (item.amount * batch) / source.batch_liters,
      })),
    },
    profile,
    machine_recipe: ready
      ? {
          id: source.id,
          name: source.name,
          steps: [{ name: 'Mash', target_f: 150, minutes: 60, drain_minutes: 2, location: 1 }],
        }
      : null,
    deployment_blockers: ready
      ? []
      : ['Choose a flow path for step 1 before sending this recipe to the brewer.'],
    warnings: [],
    can_deploy: ready,
    source_revision: source.revision,
    profile_fingerprint: `profile-${batch}`,
  }
}

function state(overrides: Partial<HubState> = {}): HubState {
  return {
    session: { demo: false },
    profiles: [pro, s, z],
    devices: [],
    notify: vi.fn(),
    navigate: vi.fn(),
    refresh: vi.fn(async () => {}),
    setDraft: vi.fn(),
    ...overrides,
  } as unknown as HubState
}

function element(hub: HubState, recipe = source) {
  return createElement(
    HubContext.Provider,
    { value: hub },
    createElement(AdaptDialog, { recipe, onClose })
  )
}

function requests(suffix: string) {
  return fetchMock.mock.calls.filter(([path]) => String(path).endsWith(suffix))
}

beforeEach(() => {
  fetchMock.mockReset()
  inventory = []
  onClose.mockReset()
  vi.stubGlobal('fetch', (path: RequestInfo | URL, options?: RequestInit) => {
    if (['/api/v1/stock-picos', '/api/v1/stock-z-programs'].includes(String(path)))
      return Promise.resolve(json({ machines: [] }))
    if (String(path).startsWith('/api/v1/devices/') && String(path).endsWith('/recipes'))
      return Promise.resolve(json({ recipes: inventory }))
    return fetchMock(path, options)
  })
  setCSRF('test-csrf')
  for (const name of dialogMethods)
    Object.defineProperty(HTMLDialogElement.prototype, name, {
      configurable: true,
      value(this: HTMLDialogElement) {
        this.open = name === 'showModal'
      },
    })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  dialogMethods.forEach((name, index) => {
    const original = originalDialogMethods[index]
    if (original) Object.defineProperty(HTMLDialogElement.prototype, name, original)
    else Reflect.deleteProperty(HTMLDialogElement.prototype, name)
  })
})

describe('recipe adaptation planning and deployment review', () => {
  it('does not assume a Pro profile belongs to an unidentified physical machine', () => {
    const unknown = { ...devices[0], id: 'unidentified', model: '', name: 'New machine' }
    render(element(state({ devices: [unknown] })))
    expect((screen.getByLabelText('Target machine') as HTMLSelectElement).value).toBe('')
    expect(
      (
        screen.getByRole('option', {
          name: 'New machine (model not identified)',
        }) as HTMLOptionElement
      ).disabled
    ).toBe(true)
  })
  it('requires the reviewed installed program to replace a matching ID', async () => {
    const result = plan(5, true)
    inventory = [{ ...result.machine_recipe!, name: 'Old program' }]
    fetchMock.mockImplementation(async (path) =>
      String(path).endsWith('/adapt')
        ? json(result)
        : json({ device_id: 'pico-pro', recipe: result.machine_recipe })
    )
    const user = userEvent.setup()
    render(element(state({ devices })))
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByText(/will replace “Old program”/)
    expect(requests('/deploy')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Replace on machine' }))
    await waitFor(() => expect(requests('/deploy')).toHaveLength(1))
    expect(JSON.parse(String(requests('/deploy')[0][1]?.body)).replace_expected).toEqual(
      inventory[0]
    )
  })

  it('defaults a 14 L imported recipe to the 5 L Pro profile and retains an unmapped planning schedule', async () => {
    fetchMock.mockResolvedValue(json(plan()))
    const user = userEvent.setup()
    const hub = state({ devices })
    render(element(hub))
    expect((screen.getByLabelText('Target machine') as HTMLSelectElement).value).toBe('pico-pro')
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByRole('heading', { name: 'Recipe schedule' })
    expect(JSON.parse(String(requests('/adapt')[0][1]?.body))).toEqual({
      profile_id: 'pico-pro',
      batch_liters: 0,
    })
    expect(screen.getByRole('heading', { name: 'Scaled ingredients' })).toBeTruthy()
    expect(screen.getByRole('region', { name: 'Deployment blockers' }).textContent).toContain(
      'Choose a flow path'
    )
    expect(screen.getByText('Unassigned')).toBeTruthy()
    expect(
      (screen.getByRole('button', { name: 'Send to machine' }) as HTMLButtonElement).disabled
    ).toBe(true)
    await user.click(
      screen.getByRole('button', { name: 'Save this adaptation as a separate recipe' })
    )
    expect(hub.setDraft).toHaveBeenCalledWith(
      expect.objectContaining({ id: '', revision: 0, batch_liters: 5, steps: source.steps })
    )
    expect(hub.navigate).toHaveBeenCalledWith('/recipes/new')
    expect(requests('/deploy')).toHaveLength(0)
  })

  it('derives the profile from an offline target and allows preview only', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(json(plan(5, true, s)))
    render(element(state({ devices: [{ ...devices[1], online: false }] })))
    expect(screen.queryByLabelText('Equipment profile')).toBeNull()
    expect(screen.queryByRole('spinbutton')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByText(/Reconnect it to send/)
    expect(JSON.parse(String(requests('/adapt')[0][1]?.body))).toEqual({
      profile_id: 'pico-s',
      batch_liters: 0,
    })
    expect(
      (screen.getByRole('button', { name: 'Send to machine' }) as HTMLButtonElement).disabled
    ).toBe(true)
  })
  it('invalidates reviewed plans on calibration changes for the selected machine', async () => {
    fetchMock.mockResolvedValue(json(plan(4.25, true)))
    const user = userEvent.setup()
    const hub = state({ devices })
    const view = render(element(hub))
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByText('Machine recipe ready')
    expect(
      (screen.getByRole('button', { name: 'Send to machine' }) as HTMLButtonElement).disabled
    ).toBe(false)
    view.rerender(
      element({ ...hub, profiles: [{ ...pro, efficiency: 60, batch_liters: 4.8 }, s, z] })
    )
    await screen.findByRole('button', { name: 'Preview recipe' })
    expect(screen.queryByRole('button', { name: 'Send to machine' })).toBeNull()
    view.rerender(element({ ...hub, profiles: [{ ...pro, batch_liters: 4 }, s, z] }))
    expect(requests('/deploy')).toHaveLength(0)
  })

  it('ignores a late preview and sends only the currently reviewed batch and calibration fingerprint', async () => {
    let resolveOld!: (value: Response) => void
    fetchMock.mockImplementation(async (path) => {
      if (String(path).endsWith('/deploy'))
        return json({
          deployed: true,
          started: false,
          device_id: pro.id,
          recipe: { id: 'from-server', name: source.name },
        })
      if (requests('/adapt').length === 1)
        return new Promise<Response>((resolve) => {
          resolveOld = resolve
        })
      return json(plan(4, true, s))
    })
    const user = userEvent.setup()
    render(element(state({ devices })))
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    const oldSignal = requests('/adapt')[0][1]?.signal
    await user.selectOptions(screen.getByLabelText('Target machine'), 'pico-s')
    expect(oldSignal?.aborted).toBe(true)
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByText('Machine recipe ready')
    await act(async () => {
      resolveOld(json(plan(5, true)))
    })
    await user.click(screen.getByRole('button', { name: 'Send to machine' }))
    await waitFor(() => expect(requests('/deploy')).toHaveLength(1))
    expect(JSON.parse(String(requests('/deploy')[0][1]?.body))).toEqual({
      recipe_id: source.id,
      profile_id: 'pico-s',
      batch_liters: 0,
      source_revision: source.revision,
      profile_fingerprint: 'profile-4',
    })
  })

  it('clears errors and prevents an old device response from enabling deployment after a switch', async () => {
    let resolveOld!: (value: Response) => void
    fetchMock.mockImplementation(
      async () =>
        new Promise<Response>((resolve) => {
          resolveOld = resolve
        })
    )
    const user = userEvent.setup()
    render(element(state({ devices })))
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await user.selectOptions(screen.getByLabelText('Target machine'), 'pico-s')
    await act(async () => {
      resolveOld(json(plan(5, true)))
    })
    expect(screen.queryByRole('button', { name: 'Send to machine' })).toBeNull()
    fetchMock.mockResolvedValue(
      json({ error: { code: 'adaptation', message: 'Old capacity error' } }, 422)
    )
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByRole('alert')
    await user.selectOptions(screen.getByLabelText('Target machine'), 'pico-pro')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(requests('/deploy')).toHaveLength(0)
  })

  it('keeps native programs at batch zero with their exact source schedule and no invented ingredients', async () => {
    const program = {
      id: 'pale-pak',
      name: 'Pale PicoPak',
      steps: [{ name: 'Heat mash', target_f: 152, minutes: 0, drain_minutes: 3, location: 1 }],
    }
    const native: Recipe = {
      ...source,
      name: program.name,
      kind: 'native-program',
      source_model: 'pico',
      batch_liters: 0,
      efficiency: 0,
      ingredients: [],
      steps: [],
      native_program: program,
    }
    const result: Adapted = { ...plan(0, true), recipe: native, machine_recipe: program }
    fetchMock.mockImplementation(async (path) =>
      String(path).endsWith('/adapt')
        ? json(result)
        : json({ deployed: true, device_id: pro.id, recipe: program })
    )
    const user = userEvent.setup()
    const hub = state({ devices })
    render(element(hub, native))
    expect(screen.queryByRole('spinbutton', { name: 'Target batch size (L)' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Preview recipe' }))
    await screen.findByText('Exact native schedule')
    expect(JSON.parse(String(requests('/adapt')[0][1]?.body)).batch_liters).toBe(0)
    expect(screen.getByText('152°F')).toBeTruthy()
    expect(screen.getByText('0 min')).toBeTruthy()
    expect(screen.queryByRole('heading', { name: 'Scaled ingredients' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Send to machine' }))
    await waitFor(() => expect(requests('/deploy')).toHaveLength(1))
    expect(JSON.parse(String(requests('/deploy')[0][1]?.body)).batch_liters).toBe(0)
    expect(hub.navigate).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Open Kitchen Pro' }))
    expect(hub.navigate).toHaveBeenCalledWith('/devices/pico-pro?recipe=pale-pak')
    expect(requests('/commands')).toHaveLength(0)
  })
})

it('shows the edition procedure, hop slot, manual stage and contact time before sending', async () => {
  const result = plan(5, true)
  result.recipe.notes =
    'Hub adaptation (Post-brew lactose). Add the cooled lactose after fermentation. https://example.org/guide Original record: Open the hot keg.\n\nHistorical instructions'
  result.recipe.ingredients.push(
    { name: 'Lactose', kind: 'misc', amount: 80, unit: 'g', use: 'conditioning', minutes: 0 },
    { name: 'Cascade', kind: 'hop', amount: 5, unit: 'g', use: 'boil', minutes: 15 },
    { name: 'Citra', kind: 'hop', amount: 10, unit: 'g', use: 'dry-hop', minutes: 7200 }
  )
  result.hop_slots_by_minute = { 15: 2 }
  fetchMock.mockResolvedValue(json(result))
  render(element(state({ devices }), { ...source, ingredients: result.recipe.ingredients }))
  await userEvent.setup().click(screen.getByRole('button', { name: 'Preview recipe' }))
  await screen.findByText('Add the cooled lactose after fermentation.')
  expect(screen.getByRole('heading', { name: 'Hub adaptation · Post-brew lactose' })).toBeTruthy()
  expect(screen.getByText('These notes give quantities for the original batch.')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'example.org' }).getAttribute('href')).toBe(
    'https://example.org/guide'
  )
  expect(screen.queryByText(/Open the hot keg/)).toBeNull()
  expect(screen.getByText('Conditioning').closest('tr')?.textContent).toContain('Lactose')
  expect(
    screen.getByText('Hop slot 2 · 15 min before boil ends').closest('tr')?.textContent
  ).toContain('Cascade')
  expect(screen.getByText('Dry hop · 5 days').closest('tr')?.textContent).toContain('Citra')
})
