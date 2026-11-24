// @vitest-environment jsdom
import { createElement } from 'react'
import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { HubContext, type HubState } from '../context'
import type { Device, DeviceStatus } from '../types'
import Fleet from './Fleet'
import Machine from './Machine'

const fetchMock = vi.fn<typeof fetch>()
const frame = { width: 128, height: 64, format: 'page-lsb-hex', data: 'a5'.repeat(1024) }
const status = {
  device_uid: 'private-enrollment-uid',
  state: 'monitoring',
  uptime_ms: 170000,
  ready: false,
  busy: false,
  stopped: false,
  wort_f: 98.5,
  thermoblock_f: 118.25,
} as DeviceStatus
const device = {
  id: 'paired-unit',
  model: 'zymatic',
  name: 'Countertop Zymatic',
  adapter: 'pico-standalone',
  enabled: true,
  online: true,
  status,
  capabilities: {
    firmware_version: '1.0.0',
    api_version: 1,
    commands: [],
    capabilities: { display: true, front_panel: false, temperature: true },
  },
} as unknown as Device
const pause = 'The display mirror will resume when the program ends.'
function response(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}
function page(d: Device, fleet = false) {
  const hub = {
    devices: [d],
    brews: [],
    profiles: [],
    recipes: [],
    session: { demo: false },
    unit: 'F',
    connected: true,
    loading: false,
    notify: vi.fn(),
    navigate: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  } as unknown as HubState
  return createElement(
    HubContext.Provider,
    { value: hub },
    fleet ? createElement(Fleet, {}) : createElement(Machine, { id: d.id })
  )
}
function displayCalls() {
  return fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/paired-unit/display'))
}
const showModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true
    },
  })
  vi.stubGlobal('fetch', fetchMock)
  vi.useFakeTimers()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    createImageData: (w: number, h: number) => ({ data: new Uint8ClampedArray(w * h * 4) }),
    putImageData: vi.fn(),
  } as unknown as CanvasRenderingContext2D)
})
afterEach(() => {
  cleanup()
  if (showModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModal)
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal')
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  fetchMock.mockReset()
})

it('shows the read-only OLED during an explicit recipe pause and clears it when the brewer becomes unavailable', async () => {
  fetchMock.mockImplementation(async (url) =>
    response(String(url).endsWith('/display') ? frame : {})
  )
  const running = { ...device, status: { ...status, busy: true, state: 'program_running' } }
  const paused = { ...device, status: { ...status, busy: true, state: 'recipe_pause' } }
  const view = render(page(running))
  await act(async () => {})
  expect(displayCalls()).toHaveLength(0)
  view.rerender(page(paused))
  await act(async () => {})
  expect(displayCalls()).toHaveLength(1)
  expect(screen.getByText('LIVE OLED · 128 × 64')).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Press dial' })).toBeNull()
  view.rerender(page({ ...paused, online: false }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  expect(displayCalls()).toHaveLength(1)
  expect(screen.queryByText('LIVE OLED · 128 × 64')).toBeNull()
  view.rerender(page(running))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  expect(displayCalls()).toHaveLength(1)
  expect(screen.getByText(pause)).toBeTruthy()
})

it('renders the real read-only panel, pauses after a busy response, and resumes it when the program ends', async () => {
  let busyResponse = false
  fetchMock.mockImplementation(async (url) => {
    if (!String(url).endsWith('/display')) return response({})
    return busyResponse
      ? response({ error: { code: 'device_display_busy', message: 'Device busy' } }, 503)
      : response(frame)
  })
  const view = render(page(device))
  await act(async () => {})
  expect(screen.getByText('LIVE OLED · 128 × 64')).toBeTruthy()
  expect(screen.getByText('Monitoring')).toBeTruthy()
  expect(screen.getByText('98.5°F')).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Press dial' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Import retained history' })).toBeNull()
  expect(screen.queryByText('Run a program')).toBeNull()
  busyResponse = true
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(screen.getByText(pause)).toBeTruthy()
  expect(screen.queryByText('LIVE OLED · 128 × 64')).toBeNull()
  const before = displayCalls().length
  view.rerender(page({ ...device, status: { ...status, busy: true, state: 'program_running' } }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000)
  })
  expect(displayCalls()).toHaveLength(before)
  expect(screen.getByText('Program running')).toBeTruthy()
  busyResponse = false
  view.rerender(page(device))
  await act(async () => {})
  expect(displayCalls()).toHaveLength(before + 1)
  expect(screen.getByText('LIVE OLED · 128 × 64')).toBeTruthy()
})

it('waits for a full frame before polling again and abandons the old request when disconnected', async () => {
  let finish: ((value: Response) => void) | undefined
  fetchMock.mockImplementation((url) => {
    if (String(url).endsWith('/display'))
      return new Promise((resolve) => {
        finish = resolve
      })
    return Promise.resolve(response({}))
  })
  const view = render(page(device))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(16000)
  })
  expect(displayCalls()).toHaveLength(1)
  view.rerender(page({ ...device, online: false }))
  expect(displayCalls()[0][1]?.signal?.aborted).toBe(true)
  await act(async () => {
    finish?.(response(frame))
  })
  expect(screen.queryByText('LIVE OLED · 128 × 64')).toBeNull()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(16000)
  })
  expect(displayCalls()).toHaveLength(1)
})

it('keeps the last frame visible and waits for a fresh revision before accepting another dial input', async () => {
  const commands: unknown[] = []
  let finishFrame!: (value: Response) => void
  const nextFrame = new Promise<Response>((resolve) => {
    finishFrame = resolve
  })
  const menu = { ...frame, data: '00'.repeat(1024), revision: 17, dial: 'menu' }
  const prompt = { ...frame, data: 'ff'.repeat(1024), revision: 18, dial: 'press' }
  fetchMock.mockImplementation(async (url, init) => {
    if (String(url).endsWith('/commands')) commands.push(JSON.parse(String(init?.body)))
    if (String(url).endsWith('/display')) {
      if (commands.length === 1) return nextFrame
      return response(commands.length ? prompt : menu)
    }
    return response({})
  })
  render(
    page({
      ...device,
      capabilities: {
        ...device.capabilities!,
        commands: ['encoder'],
        capabilities: { front_panel: true, panel_revision: true },
      },
    })
  )
  await act(async () => {})
  const up = screen.getByRole<HTMLButtonElement>('button', { name: 'Move selection up' })
  const down = screen.getByRole<HTMLButtonElement>('button', { name: 'Move selection down' })
  const press = screen.getByRole<HTMLButtonElement>('button', { name: 'Press dial' })
  const canvas = screen.getByLabelText<HTMLCanvasElement>('Live Pico OLED display')
  const draw = vi.mocked(canvas.getContext('2d')!.putImageData)
  expect(draw).toHaveBeenCalledTimes(1)
  const firstPixel = () => Array.from(draw.mock.lastCall![0].data.slice(0, 4))
  const menuPixel = firstPixel()

  await act(async () => down.click())
  expect(commands).toEqual([{ command: 'encoder', delta: 1, display_revision: 17 }])
  expect(displayCalls()).toHaveLength(2)
  expect(canvas.hidden).toBe(false)
  expect(draw).toHaveBeenCalledTimes(1)
  expect([up.disabled, down.disabled, press.disabled]).toEqual([true, true, true])
  await act(async () => press.click())
  expect(commands).toHaveLength(1)

  await act(async () => finishFrame(response(prompt)))
  expect(firstPixel()).not.toEqual(menuPixel)
  expect([up.disabled, down.disabled, press.disabled]).toEqual([true, true, false])
  await act(async () => press.click())
  expect(commands[1]).toEqual({ command: 'encoder', click: true, display_revision: 18 })
})

it('lists a monitored machine without suggesting remote program controls', async () => {
  fetchMock.mockImplementation(async () => response({}))
  render(page(device, true))
  await act(async () => {})
  expect(screen.getByRole('link', { name: 'View Countertop Zymatic' }).textContent).toContain(
    'View machine'
  )
  expect(screen.getByText('Programs start at the brewer')).toBeTruthy()
  expect(screen.queryByText('Waiting for machine status')).toBeNull()
})

it('uses the paired Zymatic front panel and retained history without offering unsupported device settings or brew telemetry', async () => {
  const paired = {
    ...device,
    capabilities: {
      ...device.capabilities!,
      firmware_version: '1.0.0',
      commands: ['encoder'],
      capabilities: {
        display: true,
        front_panel: true,
        panel_revision: true,
        history_persistent: true,
        temperature: true,
      },
    },
  }
  fetchMock.mockImplementation(async (url) =>
    response(String(url).endsWith('/display') ? { ...frame, revision: 4, dial: 'menu' } : {})
  )
  const view = render(page(paired))
  await act(async () => {})
  expect(screen.getByRole('button', { name: 'Import retained history' })).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Press dial' })).toBeTruthy()
  expect(screen.getByText('Machine status')).toBeTruthy()
  expect(screen.queryByText('Step time')).toBeNull()
  expect(screen.queryByText('Target')).toBeNull()
  await act(async () => screen.getByRole('button', { name: 'Manage machine' }).click())
  expect(screen.queryByRole('button', { name: 'Brewer preferences' })).toBeNull()
  expect(
    fetchMock.mock.calls
      .map(([url]) => String(url))
      .some((url) => /\/(settings|telemetry)$/.test(url))
  ).toBe(false)

  await act(async () => screen.getByRole('button', { name: 'Close dialog' }).click())
  view.rerender(page({ ...paired, status: { ...status, state: 'program_running', busy: true } }))
  expect(screen.queryByText('Current step')).toBeNull()
  view.rerender(page(paired, true))
  expect(screen.getByText('Select a program with the front panel')).toBeTruthy()
  expect(screen.queryByText('Waiting for machine status')).toBeNull()
})
