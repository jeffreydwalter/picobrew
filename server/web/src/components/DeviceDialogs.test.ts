// @vitest-environment jsdom
import { createElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HubContext, type HubState } from '../context'
import { setCSRF } from '../lib/api'
import type { Device } from '../types'
import { DeviceDialog, HubConnectionStatus } from './DeviceDialogs'
import { FermentDialog } from './FermentDialog'
import { dateLabel } from '../lib/utils'
import { PicoFermAssociation } from './PicoFermAssociation'

const fetchMock = vi.fn<typeof fetch>()
const discover = vi.fn<typeof fetch>()
const onClose = vi.fn()
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')

function machine(overrides: Partial<Device> = {}): Device {
  return {
    id: 'physical-s',
    name: 'Kitchen brewer',
    model: 'pico-s',
    model_source: 'firmware',
    adapter: 'pico-standalone',
    base_url: 'http://192.168.1.42',
    location: 'Kitchen',
    enabled: true,
    key_configured: true,
    created_at: '',
    online: true,
    last_seen: null,
    status: null,
    capabilities: null,
    telemetry: { state: 'configured' },
    ...overrides,
  }
}

function hub(device?: Device): HubState {
  return {
    session: {
      authenticated: true,
      setup_required: false,
      csrf_token: 'test',
      version: 'test',
      demo: false,
    },
    devices: device ? [device] : [],
    profiles: [],
    navigate: vi.fn(),
    notify: vi.fn(),
    refresh: vi.fn(async () => {}),
  } as unknown as HubState
}

function show(device?: Device) {
  const state = hub(device)
  render(
    createElement(
      HubContext.Provider,
      { value: state },
      createElement(DeviceDialog, { device, onClose })
    )
  )
  return state
}

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  fetchMock.mockReset()
  discover.mockReset().mockResolvedValue(json({ candidates: [], partial: false }))
  onClose.mockReset()
  vi.stubGlobal('fetch', (input: RequestInfo | URL, options?: RequestInit) =>
    String(input).endsWith('/devices/discover')
      ? discover(input, options)
      : fetchMock(input, options)
  )
  setCSRF('test')
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true
    },
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  if (originalShowModal)
    Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal)
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
})

describe('automatic physical-machine connections', () => {
  it('assigns a journal batch to the chosen PicoFerm and offers only its unclaimed recordings', async () => {
    const user = userEvent.setup()
    const state = hub(machine())
    const resources = {
      brews: [{ id: 'brew', recipe_name: 'Pale ale', observed_at: '2026-11-24T12:00:00Z' }],
      picoferms: [{ id: 'sensor', name: 'Cellar keg', brewer_id: '', revision: 4 }],
      recordings: [
        {
          id: 'recording',
          device_id: 'sensor',
          session_number: '42',
          started_at: '2026-11-22T08:30:00Z',
          observed_at: '2026-11-24T12:30:00Z',
        },
        {
          id: 'second',
          device_id: 'sensor',
          session_number: '43',
          observed_at: '2026-11-24T12:30:00Z',
        },
        {
          id: 'claimed',
          device_id: 'sensor',
          observed_at: '2026-11-20T12:00:00Z',
          linked_brew_id: 'older',
        },
        { id: 'other', device_id: 'other-sensor', observed_at: '2026-11-24T13:00:00Z' },
      ],
    }
    fetchMock.mockImplementation(async (input, options) => {
      expect(String(input)).toBe('/api/v1/devices/physical-s/picoferms')
      if (options?.method === 'PUT') {
        expect(JSON.parse(String(options.body))).toEqual({
          picoferm_id: 'sensor',
          revision: 4,
          brew_id: 'brew',
          recording_id: 'recording',
        })
        return json({ brewer_id: 'physical-s', revision: 5 })
      }
      return json(resources)
    })
    render(
      createElement(
        HubContext.Provider,
        { value: state },
        createElement(FermentDialog, { brewerId: 'physical-s', brewId: 'brew', onClose })
      )
    )
    await user.selectOptions(await screen.findByLabelText('PicoFerm'), 'sensor')
    const recordings = screen.getByLabelText('Fermentation recording') as HTMLSelectElement
    expect([...recordings.options].map((x) => x.value)).toEqual(['', 'recording', 'second'])
    expect(recordings.options[1].text).toBe(
      'Session 42 · Started ' + dateLabel('2026-11-22T08:30:00Z')
    )
    expect(recordings.options[2].text).toBe(
      'Session 43 · First seen ' + dateLabel('2026-11-24T12:30:00Z')
    )
    await user.selectOptions(recordings, 'recording')
    await user.click(screen.getByRole('button', { name: 'Assign to batch' }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    expect(fetchMock.mock.calls.filter(([, options]) => options?.method === 'PUT')).toHaveLength(1)
  })
  it('unlinks equipment from the brewer without changing a batch', async () => {
    const user = userEvent.setup()
    const state = hub(machine())
    fetchMock.mockImplementation(async (input, options) => {
      expect(String(input)).toBe('/api/v1/devices/physical-s/picoferms')
      if (options?.method === 'PUT') {
        expect(JSON.parse(String(options.body))).toEqual({
          picoferm_id: 'sensor',
          revision: 7,
          unlink: true,
        })
        return json({ brewer_id: '', revision: 8 })
      }
      return json({
        picoferms: [{ id: 'sensor', name: 'Cellar keg', brewer_id: 'physical-s', revision: 7 }],
        brews: [
          {
            id: 'brew',
            recipe_name: 'Pale ale',
            session_number: '1',
            observed_at: '2026-11-24T12:00:00Z',
          },
        ],
        recordings: [],
      })
    })
    render(
      createElement(
        HubContext.Provider,
        { value: state },
        createElement(FermentDialog, { brewerId: 'physical-s', onClose })
      )
    )
    const batch = await screen.findByLabelText('Batch')
    await user.selectOptions(batch, 'brew')
    await user.selectOptions(screen.getByLabelText('PicoFerm'), 'sensor')
    expect(screen.queryByRole('button', { name: 'Unlink from brewer' })).toBeNull()
    await user.selectOptions(batch, '')
    await user.selectOptions(screen.getByLabelText('PicoFerm'), 'sensor')
    await user.click(screen.getByRole('button', { name: 'Unlink from brewer' }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  })
  it('distinguishes same-named monitors by their registered location and address', async () => {
    const state = hub(machine())
    state.devices.push(
      machine({
        id: 'west',
        model: 'picoferm',
        name: 'PicoFerm',
        location: 'Cellar',
        base_url: 'http://192.168.1.70/',
      }),
      machine({
        id: 'east',
        model: 'picoferm',
        name: 'PicoFerm',
        location: 'Garage',
        base_url: 'http://192.168.1.71',
      }),
      machine({
        id: 'solo',
        model: 'picoferm',
        name: 'Lager keg',
        location: 'Garage',
        base_url: 'http://192.168.1.72',
      })
    )
    fetchMock.mockResolvedValue(
      json({
        picoferms: state.devices
          .slice(1)
          .map((d) => ({ id: d.id, name: d.name, brewer_id: '', revision: 0 })),
        brews: [],
        recordings: [],
      })
    )
    render(
      createElement(
        HubContext.Provider,
        { value: state },
        createElement(FermentDialog, { brewerId: 'physical-s', onClose })
      )
    )
    const select = (await screen.findByLabelText('PicoFerm')) as HTMLSelectElement
    expect([...select.options].slice(1).map((o) => o.text)).toEqual([
      'PicoFerm · Cellar · 192.168.1.70',
      'PicoFerm · Garage · 192.168.1.71',
      'Lager keg',
    ])
  })
  it('links and unlinks a registered PicoFerm while the brewer is offline', async () => {
    const user = userEvent.setup()
    let saved = { brewer_id: '', revision: 0 }
    fetchMock.mockImplementation(async (input, options) => {
      expect(String(input)).toBe('/api/v1/devices/ferm/brewer')
      if (options?.method === 'PUT') {
        const body = JSON.parse(String(options.body))
        expect(body.revision).toBe(saved.revision)
        saved = { brewer_id: body.brewer_id, revision: saved.revision + 1 }
      }
      return json(saved)
    })
    const state = hub(machine({ online: false }))
    state.devices.push(machine({ id: 'still', model: 'picostill', name: 'Garage still' }))
    render(
      createElement(
        HubContext.Provider,
        { value: state },
        createElement(PicoFermAssociation, { id: 'ferm' })
      )
    )
    const select = screen.getByRole('combobox')
    await waitFor(() => expect((select as HTMLSelectElement).disabled).toBe(false))
    expect(screen.queryByRole('option', { name: 'Garage still' })).toBeNull()
    await user.selectOptions(select, 'physical-s')
    await user.click(screen.getByRole('button', { name: 'Save link' }))
    await user.click(await screen.findByRole('button', { name: 'Open brewer' }))
    expect(state.navigate).toHaveBeenCalledWith('/devices/physical-s')
    await user.selectOptions(select, '')
    await user.click(screen.getByRole('button', { name: 'Save link' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Open brewer' })).toBeNull())
    expect(saved).toEqual({ brewer_id: '', revision: 2 })
  })
  it('finds a PicoFerm, preserves the chosen name, and enrolls it through its key', async () => {
    const user = userEvent.setup()
    discover.mockResolvedValue(
      json({
        candidates: [
          {
            name: 'PicoFerm',
            model: 'picoferm',
            source: 'standalone',
            base_url: 'http://192.168.1.50',
          },
        ],
        partial: false,
      })
    )
    fetchMock.mockResolvedValue(json(machine({ id: 'ferm', model: 'picoferm' })))
    const state = show()
    await user.type(screen.getByLabelText('Machine name'), 'Garage keg')
    await user.click(await screen.findByRole('button', { name: 'Select' }))
    expect(fetchMock).not.toHaveBeenCalled()
    expect((screen.getByLabelText('Machine name') as HTMLInputElement).value).toBe('Garage keg')
    expect((screen.getByLabelText('Machine URL') as HTMLInputElement).value).toBe(
      'http://192.168.1.50'
    )
    await user.type(screen.getByLabelText('Machine API key'), 'provisioned-key')
    await user.click(screen.getByRole('button', { name: 'Add machine' }))
    await waitFor(() => expect(state.navigate).toHaveBeenCalledWith('/devices/ferm'))
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      model: '',
      api_key: 'provisioned-key',
    })
  })

  it('routes a Zymatic sync sighting to recipes without trying to enroll it', async () => {
    const user = userEvent.setup()
    discover.mockResolvedValue(
      json({
        candidates: [{ name: 'Zymatic', source: 'zymatic-sync', serial: '112233445566' }],
        partial: false,
      })
    )
    const state = show()
    try {
      await user.click(await screen.findByRole('button', { name: 'Use for recipe sync' }))
      expect(localStorage.getItem('stock-zymatic-serial')).toBe('112233445566')
      expect(state.navigate).toHaveBeenCalledWith('/recipes')
      expect(fetchMock).not.toHaveBeenCalled()
    } finally {
      localStorage.removeItem('stock-zymatic-serial')
    }
  })

  it('opens a stock Still’s readings without offering standalone pairing', async () => {
    discover.mockResolvedValue(
      json({
        candidates: [
          {
            name: 'PicoStill',
            model: 'picostill',
            source: 'stock',
            base_url: 'http://192.168.1.71',
          },
        ],
        partial: false,
      })
    )
    show()
    const link = await screen.findByRole('link', { name: 'Open device website' })
    expect(link.getAttribute('href')).toBe('http://192.168.1.71/data-page')
    expect(screen.queryByRole('button', { name: 'Select' })).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('sets up a connected machine through authenticated USB jobs and resumes its confirmation', async () => {
    const user = userEvent.setup()
    const id = '1'.repeat(32)
    const waiting = {
      id,
      model: 'pico-c',
      action: 'install',
      port: 'adapter',
      state: 'waiting',
      confirmation: 'stage-1',
      message: 'Keep USB connected, then confirm firmware installation.',
    }
    let submitted: unknown
    fetchMock.mockImplementation(async (input, opt) => {
      const path = String(input)
      if (path.endsWith('/usb/devices'))
        return json({
          available: true,
          devices: [
            { port: 'adapter', label: 'Brewer USB', models: ['pico-s', 'pico-pro', 'pico-c'] },
          ],
        })
      if (path.endsWith('/usb/jobs')) {
        submitted = JSON.parse(String(opt?.body))
        return json(waiting, 202)
      }
      if (path.endsWith('/confirm')) {
        expect(JSON.parse(String(opt?.body))).toEqual({ confirm: 'stage-1' })
        expect(new Headers(opt?.headers).get('X-CSRF-Token')).toBe('test')
        return json({
          ...waiting,
          state: 'complete',
          confirmation: undefined,
          message: 'Firmware verified.',
          pairing_ready: true,
        })
      }
      return json(waiting)
    })
    show()
    await user.type(screen.getByLabelText('Machine name'), 'My brewer')
    await user.click(screen.getByRole('button', { name: 'USB setup and recovery' }))
    const select = await screen.findByLabelText('Machine model', { selector: 'select' })
    await user.selectOptions(select, 'pico-c')
    const install = screen.getByRole('button', { name: 'Install standalone firmware' })
    expect((install as HTMLButtonElement).disabled).toBe(true)
    await user.selectOptions(screen.getByLabelText('Physical Pico C display controller'), 'ssd1309')
    await user.click(install)
    expect(submitted).toEqual({
      action: 'install',
      model: 'pico-c',
      port: 'adapter',
      display: 'ssd1309',
    })
    await user.click(await screen.findByRole('button', { name: 'Continue' }))
    expect(await screen.findByText('Firmware verified.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Add machine' })).toBeTruthy()
    await user.click(screen.getByRole('button', { name: 'Back to Add machine' }))
    expect((screen.getByLabelText('Machine name') as HTMLInputElement).value).toBe('My brewer')
  })
  it('registers a brewer after dial approval without typing or receiving its key', async () => {
    let approve: (response: Response) => void = () => {}
    fetchMock.mockResolvedValueOnce(json({ pairing_id: 'test-handle', code: 'b65d8001' }))
    fetchMock.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          approve = resolve
        })
    )
    const user = userEvent.setup()
    show()
    expect(screen.queryByLabelText('Machine API key')).toBeNull()
    await user.type(screen.getByLabelText('Machine name'), 'Garage Pico')
    await user.type(screen.getByLabelText('Machine URL'), 'http://192.168.1.42')
    await user.click(screen.getByRole('button', { name: 'Add machine' }))
    await screen.findByText('b65d8001')
    expect(screen.getByText('About Zymatic')).toBeTruthy()
    expect(screen.getByText('Settings → About → Pair with Hub')).toBeTruthy()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/devices/pair')
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toMatchObject({
      pairing_id: 'test-handle',
      api_key: '',
    })
    approve(json(machine()))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  })

  it('registers a physical brewer without making the user choose or assume a model', async () => {
    fetchMock.mockResolvedValue(json(machine({ model: '', model_source: 'pending' })))
    const user = userEvent.setup()
    const state = show()
    const model = screen.getByLabelText('Machine model') as HTMLInputElement
    expect(model.readOnly).toBe(true)
    expect(model.value).toBe('Detect automatically')
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
    await user.type(screen.getByLabelText('Machine name'), 'My Pico S')
    await user.type(screen.getByLabelText('Machine URL'), 'http://192.168.1.42')
    await user.click(screen.getByRole('button', { name: 'Use an existing key instead' }))
    await user.type(screen.getByLabelText('Machine API key'), 'local-test-key')
    await user.click(screen.getByRole('button', { name: 'Add machine' }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/devices')
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      adapter: 'pico-standalone',
      model: '',
      name: 'My Pico S',
      base_url: 'http://192.168.1.42',
    })
    expect(state.notify).toHaveBeenCalledWith(expect.stringContaining('setting up live readings'))
  })

  it('keeps simulator selection and resets to automatic identification when returning to a physical brewer', async () => {
    const user = userEvent.setup()
    show()
    await user.selectOptions(screen.getByLabelText('Connection type'), 'simulator')
    await user.selectOptions(screen.getByLabelText('Machine model'), 'pico-s')
    expect((screen.getByLabelText('Machine model') as HTMLSelectElement).value).toBe('pico-s')
    await user.selectOptions(screen.getByLabelText('Connection type'), 'pico-standalone')
    expect((screen.getByLabelText('Machine model') as HTMLInputElement).value).toBe(
      'Detect automatically'
    )
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
  })

  it('keeps a reported Pico S read-only when editing the same machine record', async () => {
    fetchMock.mockResolvedValue(json(machine()))
    const user = userEvent.setup()
    show(machine())
    expect((screen.getByLabelText('Machine model') as HTMLInputElement).value).toBe('Pico S')
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
    expect(screen.getByText('Saved. Live readings are configured.')).toBeTruthy()
    await user.clear(screen.getByLabelText('Machine name'))
    await user.type(screen.getByLabelText('Machine name'), 'Renamed S')
    await user.click(screen.getByRole('button', { name: 'Save machine' }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/devices/physical-s')
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({
      name: 'Renamed S',
      model: '',
    })
  })

  it('offers an empty explicit fallback only when older firmware requires a selection', async () => {
    fetchMock.mockResolvedValue(json(machine({ model_source: 'manual' })))
    const user = userEvent.setup()
    show(
      machine({
        model: '',
        model_source: 'pending',
        model_selection_required: true,
        model_error_code: 'not_reported',
        model_error: 'This firmware does not report its model.',
      })
    )
    const model = screen.getByRole('combobox', { name: 'Machine model' }) as HTMLSelectElement
    expect(model.value).toBe('')
    expect(model.required).toBe(true)
    await user.selectOptions(model, 'pico-s')
    await user.click(screen.getByRole('button', { name: 'Save machine' }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({ model: 'pico-s' })
  })

  it('identifies an existing manual record without creating another machine or losing its connection', async () => {
    fetchMock.mockResolvedValue(json(machine()))
    const user = userEvent.setup()
    const state = show(machine({ model: 'pico-pro', model_source: 'manual' }))
    expect((screen.getByLabelText('Machine model') as HTMLSelectElement).value).toBe('pico-pro')
    await user.click(screen.getByRole('button', { name: 'Identify machine' }))
    await waitFor(() =>
      expect((screen.getByLabelText('Machine model') as HTMLInputElement).value).toBe('Pico S')
    )
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/devices/physical-s/test')
    expect(state.refresh).toHaveBeenCalledOnce()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('preserves known identity after an unreachable-machine error', async () => {
    fetchMock.mockRejectedValue(new Error('Machine is unreachable.'))
    const user = userEvent.setup()
    show(machine())
    await user.click(screen.getByRole('button', { name: 'Identify machine' }))
    await screen.findByText('Machine is unreachable.')
    expect((screen.getByLabelText('Machine model') as HTMLInputElement).value).toBe('Pico S')
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('does not offer a manual override for invalid reported identity', () => {
    show(
      machine({
        model: '',
        model_source: 'manual',
        model_error_code: 'invalid_model',
        model_error: 'The reported model is not valid.',
      })
    )
    expect(screen.getByText('The reported model is not valid.')).toBeTruthy()
    expect((screen.getByLabelText('Machine model') as HTMLInputElement).value).toBe(
      'Model not identified'
    )
    expect(screen.queryByRole('combobox', { name: 'Machine model' })).toBeNull()
  })

  it('shows automatic Hub connection status without receiver configuration controls', () => {
    render(createElement(HubConnectionStatus, { device: machine() }))
    expect(screen.getByRole('status').textContent).toBe('Saved. Live readings are configured.')
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.queryByRole('spinbutton')).toBeNull()
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()
  })
})
