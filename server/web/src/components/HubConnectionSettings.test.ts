// @vitest-environment jsdom
import { createElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HubContext, type HubState } from '../context'
import { setCSRF } from '../lib/api'
import type { Device, Telemetry } from '../types'
import { HubConnectionSettings } from './HubConnectionSettings'

const fetchMock = vi.fn<typeof fetch>()
const notify = vi.fn()
let connection: Telemetry
const device = {
  id: 'pico-s',
  adapter: 'pico-standalone',
  enabled: true,
  online: true,
  capabilities: { telemetry_forwarding: true },
  telemetry: { enabled: true, host: 'hub.local', port: 8080, state: 'configured' },
} as Device

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function show(overrides: Partial<Device> = {}) {
  const brewer = { ...device, ...overrides }
  const hub = { devices: [brewer], refresh: vi.fn(async () => {}), notify } as unknown as HubState
  const element = (next: Device) =>
    createElement(
      HubContext.Provider,
      { value: { ...hub, devices: [next] } },
      createElement(HubConnectionSettings, { device: next })
    )
  const view = render(element(brewer))
  return {
    ...view,
    hub,
    update: (next: Partial<Device>) => view.rerender(element({ ...brewer, ...next })),
  }
}

function writes() {
  return fetchMock.mock.calls.filter(([, options]) => options?.method === 'PUT')
}
async function ready() {
  await waitFor(() =>
    expect(
      (screen.getByRole('button', { name: 'Save connection' }) as HTMLButtonElement).disabled
    ).toBe(false)
  )
}

beforeEach(() => {
  connection = { enabled: true, host: 'hub.local', port: 8080, state: 'configured' }
  notify.mockReset()
  fetchMock.mockReset()
  fetchMock.mockImplementation(async (_url, options) => {
    if (options?.method === 'PUT')
      connection = { ...JSON.parse(String(options.body)), state: connection.state }
    return json(connection)
  })
  vi.stubGlobal('fetch', fetchMock)
  setCSRF('test')
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('saved live-reading connection', () => {
  it('lets a PicoFerm select this Hub again without resubmitting the old address', async () => {
    const user = userEvent.setup()
    connection = { enabled: false, host: 'old.example', port: 8181, state: 'disabled' }
    fetchMock.mockImplementation(async (_url, options) => {
      if (options?.method === 'PUT') {
        expect(JSON.parse(String(options.body))).toEqual({ mode: 'automatic' })
        connection = { enabled: true, host: 'new.example', port: 8080, state: 'configured' }
      }
      return json(connection)
    })
    show({ model: 'picoferm' })
    await ready()
    await user.click(screen.getByRole('button', { name: 'Use this Hub' }))
    await waitFor(() =>
      expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe('new.example')
    )
    expect(writes()).toHaveLength(1)
    expect((screen.getByLabelText('Send live readings') as HTMLInputElement).checked).toBe(true)
  })
  it('always shows the saved receiver fields and saves only their three values', async () => {
    const user = userEvent.setup()
    const view = show()
    await ready()
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe('hub.local')
    expect((screen.getByLabelText('Receiver port') as HTMLInputElement).value).toBe('8080')
    expect(screen.queryByRole('combobox')).toBeNull()
    expect(screen.queryByText('Advanced Hub connection')).toBeNull()
    expect(screen.queryByText('Return to automatic')).toBeNull()
    expect(writes()).toHaveLength(0)
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'brew-relay.local')
    await user.clear(screen.getByLabelText('Receiver port'))
    await user.type(screen.getByLabelText('Receiver port'), '49152')
    await user.click(screen.getByLabelText('Send live readings'))
    await user.click(screen.getByRole('button', { name: 'Save connection' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('Connection saved.'))
    expect(JSON.parse(String(writes()[0][1]?.body))).toEqual({
      enabled: false,
      host: 'brew-relay.local',
      port: 49152,
    })
    view.unmount()
    show()
    await ready()
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe(
      'brew-relay.local'
    )
    expect((screen.getByLabelText('Receiver port') as HTMLInputElement).value).toBe('49152')
    expect((screen.getByLabelText('Send live readings') as HTMLInputElement).checked).toBe(false)
    expect(writes()).toHaveLength(1)
  })

  it('saves a disconnected machine with no capabilities and shows that applying it is pending', async () => {
    connection = { enabled: true, host: 'saved.local', port: 9000, state: 'pending' }
    const user = userEvent.setup()
    show({ online: false, enabled: false, capabilities: null })
    await ready()
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'offline-edit.local')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('Connection saved.'))
    expect(screen.getByRole('status').textContent).toBe(
      'Saved. Will apply when the machine is available.'
    )
    expect(JSON.parse(String(writes()[0][1]?.body))).toEqual({
      enabled: true,
      host: 'offline-edit.local',
      port: 9000,
    })
  })

  it('keeps unsaved edits when the first settings response arrives late', async () => {
    let finish!: (value: Response) => void
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve
        })
    )
    const user = userEvent.setup()
    show()
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'my-edit.local')
    await user.click(screen.getByLabelText('Send live readings'))
    await act(async () => finish(json(connection)))
    await ready()
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe('my-edit.local')
    expect((screen.getByLabelText('Send live readings') as HTMLInputElement).checked).toBe(false)
    expect(screen.getByRole('status').textContent).toBe('Unsaved changes.')
  })

  it('updates untouched fields from fleet events and ignores an older loading response', async () => {
    let finish!: (value: Response) => void
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve
        })
    )
    const view = show({ telemetry: undefined })
    view.update({
      telemetry: { enabled: true, host: 'resolved.local', port: 9090, state: 'configured' },
    })
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe(
      'resolved.local'
    )
    await act(async () => finish(json(connection)))
    await ready()
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe(
      'resolved.local'
    )
    expect((screen.getByLabelText('Receiver port') as HTMLInputElement).value).toBe('9090')
    view.update({
      telemetry: { enabled: false, host: 'other-tab.local', port: 9001, state: 'pending' },
    })
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe(
      'other-tab.local'
    )
    expect((screen.getByLabelText('Send live readings') as HTMLInputElement).checked).toBe(false)
    expect(writes()).toHaveLength(0)
  })

  it('does not clobber an unsaved edit after save or during fleet refreshes', async () => {
    const user = userEvent.setup()
    const view = show()
    await ready()
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'first.local')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('Connection saved.'))
    await ready()
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'unsaved.local')
    await user.click(screen.getByLabelText('Send live readings'))
    view.update({
      telemetry: { enabled: true, host: 'first.local', port: 8080, state: 'configured' },
    })
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe('unsaved.local')
    expect((screen.getByLabelText('Send live readings') as HTMLInputElement).checked).toBe(false)
    expect(screen.getByRole('status').textContent).toBe('Unsaved changes.')
    expect(writes()).toHaveLength(1)
  })

  it('shows a failed saved-settings read without claiming the connection was saved', async () => {
    fetchMock.mockResolvedValue(
      json({ error: { code: 'storage', message: 'Saved settings could not be read.' } }, 500)
    )
    show({ telemetry: { state: 'error' } })
    await screen.findByText('Saved settings could not be read.')
    expect(screen.queryByRole('status')).toBeNull()
    expect(
      (screen.getByRole('button', { name: 'Save connection' }) as HTMLButtonElement).disabled
    ).toBe(true)
    expect(notify).not.toHaveBeenCalled()
  })

  it('does not submit out-of-range ports even when sending is disabled', async () => {
    connection = { enabled: false, host: '', port: 80, state: 'configured' }
    const user = userEvent.setup()
    show()
    await ready()
    const port = screen.getByLabelText('Receiver port')
    for (const value of ['0', '65536']) {
      await user.clear(port)
      await user.type(port, value)
      await user.click(screen.getByRole('button', { name: 'Save connection' }))
      expect(writes()).toHaveLength(0)
    }
    await user.clear(port)
    await user.type(port, '65535')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('Connection saved.'))
    expect(JSON.parse(String(writes()[0][1]?.body))).toEqual({
      enabled: false,
      host: '',
      port: 65535,
    })
  })

  it('keeps entered settings visible when saving fails', async () => {
    fetchMock.mockImplementation(async (_url, options) =>
      options?.method === 'PUT'
        ? json({ error: { code: 'storage', message: 'The connection could not be saved.' } }, 500)
        : json(connection)
    )
    const user = userEvent.setup()
    show()
    await ready()
    await user.clear(screen.getByLabelText('Receiver host'))
    await user.type(screen.getByLabelText('Receiver host'), 'custom.local')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))
    await screen.findByText('The connection could not be saved.')
    expect((screen.getByLabelText('Receiver host') as HTMLInputElement).value).toBe('custom.local')
    expect(notify).not.toHaveBeenCalled()
  })

  it('keeps saved settings visible for older unsupported firmware without implying they were applied', async () => {
    connection = { ...connection, state: 'unsupported' }
    show({ capabilities: { telemetry_forwarding: false } as Device['capabilities'] })
    await ready()
    expect(screen.getByLabelText('Receiver host')).toBeTruthy()
    expect(screen.getByRole('status').textContent).toBe(
      'Saved. This firmware cannot send live readings.'
    )
  })

  it('keeps virtual brewers local without showing receiver controls', () => {
    show({ adapter: 'simulator' })
    expect(screen.getByText('Live readings stay in this Hub for virtual brewers.')).toBeTruthy()
    expect(screen.queryByLabelText('Receiver host')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
