// @vitest-environment jsdom
import { createElement, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HubContext, type HubState } from '../context'
import { setCSRF } from '../lib/api'
import type { FirmwarePackage, FirmwareSettings } from '../types'
import { FirmwareManager } from './FirmwareManager'
import { MachineFirmwareDialog } from './FirmwarePackages'

const fetchMock = vi.fn<typeof fetch>()
const notify = vi.fn()
const navigate = vi.fn()
const close = vi.fn()
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal')
const defaultRepository = 'jeffreydwalter/picobrew'
const commit = 'a'.repeat(40)
const candidate = {
  model: 'pico-s',
  model_family: 'pico-s-pro',
  firmware_version: '1.1.0',
  binary_sha256: 'b'.repeat(64),
  binary_bytes: 180000,
}
const uploaded: FirmwarePackage = {
  ...candidate,
  id: 'c'.repeat(64),
  package_bytes: 210000,
  created_at: '2026-11-19T12:00:00Z',
  validation: 'passed',
  provenance: { source: 'upload' },
}
let source: FirmwareSettings

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}
async function response(path: RequestInfo | URL, options?: RequestInit) {
  const url = String(path)
  if (url.endsWith('/firmware/settings')) {
    if (options?.method === 'PUT') source = JSON.parse(String(options.body))
    return json(source)
  }
  if (url.endsWith('/firmware/updates')) return json({ releases: [], notices: [] })
  throw new Error(`Unexpected request: ${url}`)
}
function show(child: ReactNode = createElement(FirmwareManager)) {
  const hub = { notify, navigate } as unknown as HubState
  return render(createElement(HubContext.Provider, { value: hub }, child))
}
function calls(path: string, method = 'GET') {
  return fetchMock.mock.calls.filter(
    ([url, options]) => String(url).endsWith(path) && (options?.method || 'GET') === method
  )
}
beforeEach(() => {
  source = { repository: defaultRepository, ref: '' }
  fetchMock.mockReset()
  fetchMock.mockImplementation(response)
  notify.mockReset()
  navigate.mockReset()
  close.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  setCSRF('test-session')
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

describe('Hub firmware packages', () => {
  it('keeps only source controls in settings', async () => {
    show()
    await screen.findByDisplayValue(defaultRepository)
    expect(screen.queryByRole('button', { name: 'Check for Updates' })).toBeNull()
    expect(screen.queryByText('Packages in this Hub')).toBeNull()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Branch or tag'), 'stable')
    await user.click(screen.getByRole('button', { name: 'Save source' }))
    await waitFor(() => expect(source.ref).toBe('stable'))
  })
  it('browses old versions and stages only the selected update before installation', async () => {
    const user = userEvent.setup()
    const release = {
      ...candidate,
      model: 'pico-s',
      kind: 'standalone',
      archive_sha256: uploaded.id,
      firmware_version: '1.3.0',
      hub_ota_protocol: 1,
    }
    const usbOnly = {
      ...release,
      firmware_version: '1.2.0',
      hub_ota_protocol: 0,
      archive_sha256: '2'.repeat(64),
    }
    const downgrade = {
      ...uploaded,
      id: '4'.repeat(64),
      firmware_version: '1.2.8',
      hub_ota_protocol: 1,
    }
    const installed = { ...release, firmware_version: '1.2.9', archive_sha256: '3'.repeat(64) }
    let current = installed.firmware_version
    const path = '/devices/pico-s/firmware'
    fetchMock.mockImplementation(async (url, opts) => {
      if (String(url).endsWith(path))
        return json({
          model: 'pico-s',
          device_uid: 'test-uid',
          current_version: current,
          install_supported: true,
          packages: [
            { ...uploaded, ...release },
            downgrade,
            { ...downgrade, id: '5'.repeat(64), model: 'pico-pro' },
            { ...uploaded, id: '6'.repeat(64) },
            { ...downgrade, id: 'a'.repeat(64), model_family: 'pico-c' },
            { ...downgrade, id: 'b'.repeat(64), display_variant: 'ssd1325' },
          ],
        })
      if (String(url).endsWith('/firmware/updates'))
        return json({
          releases: [
            usbOnly,
            installed,
            release,
            { ...release, archive_sha256: '7'.repeat(64), model: 'pico-s-pro', kind: 'historical' },
            { ...release, archive_sha256: '8'.repeat(64), model: 'pico-pro' },
            { ...release, archive_sha256: '9'.repeat(64), model: 'z-series', kind: 'reference' },
          ],
          notices: [],
        })
      if (String(url).endsWith('/stage')) return json(uploaded)
      if (String(url).endsWith('/install')) {
        current =
          JSON.parse(String(opts?.body)).package_id === downgrade.id
            ? downgrade.firmware_version
            : release.firmware_version
        return json({ state: 'running' }, 202)
      }
      return response(url, opts)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 'pico-s', onClose: close }))
    const versions = await screen.findByLabelText('Firmware version')
    await waitFor(() =>
      expect(Array.from((versions as HTMLSelectElement).options, (o) => o.value)).toEqual([
        release.archive_sha256,
        installed.archive_sha256,
        downgrade.id,
      ])
    )
    await user.selectOptions(versions, '1.2.9 · Current version')
    const disabledInstall = screen.getByRole('button', { name: 'Already installed' })
    expect((disabledInstall as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Download' }) as HTMLButtonElement).disabled).toBe(
      false
    )
    await user.click(disabledInstall)
    expect(calls('/stage', 'POST')).toHaveLength(0)
    expect(calls(path + '/install', 'POST')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Check for Updates' }))
    expect(calls('/firmware/updates', 'POST')).toHaveLength(1)
    const stage = '/firmware/releases/' + uploaded.id + '/stage'
    expect(calls(stage, 'POST')).toHaveLength(0)
    await user.selectOptions(versions, release.archive_sha256)
    await user.click(screen.getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(calls(path + '/install', 'POST')).toHaveLength(1))
    expect(calls(stage, 'POST')).toHaveLength(1)
    expect(JSON.parse(String(calls(path + '/install', 'POST')[0][1]?.body))).toEqual({
      package_id: uploaded.id,
      device_uid: 'test-uid',
      confirm: true,
    })
    expect(
      ((await screen.findByRole('button', { name: 'Already installed' })) as HTMLButtonElement)
        .disabled
    ).toBe(true)
    await user.selectOptions(versions, downgrade.id)
    await user.click(screen.getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(calls(path + '/install', 'POST')).toHaveLength(2))
    expect(calls('/stage', 'POST')).toHaveLength(1)
    expect(JSON.parse(String(calls(path + '/install', 'POST')[1][1]?.body)).package_id).toBe(
      downgrade.id
    )
  })
  it.each([
    ['picostill', 'reference', 'esp32-factory-v1'],
    ['z-series', 'reference', 'esp32-factory-v1'],
    ['picoferm', 'picoferm-ab', 'esp8266-ab-v1'],
  ])(
    'offers the wireless %s application and hides USB-only packages',
    async (model, kind, protocol) => {
      const path = `/devices/${model}/firmware`
      const release = {
        ...candidate,
        model,
        model_family: model,
        kind,
        firmware_version: '1.2.7',
        archive_sha256: '7'.repeat(64),
        ota_protocol: protocol,
      }
      fetchMock.mockImplementation(async (url, opts) => {
        if (String(url).endsWith(path))
          return json({
            model,
            current_version: '1.2.6',
            device_uid: 'machine',
            install_supported: true,
            packages: [],
          })
        if (String(url).endsWith('/firmware/updates'))
          return json({
            releases: [
              release,
              {
                ...release,
                archive_sha256: '8'.repeat(64),
                ota_protocol: undefined,
                firmware_version: '1.2.5',
              },
            ],
            notices: [],
          })
        return response(url, opts)
      })
      show(createElement(MachineFirmwareDialog, { deviceID: model, onClose: close }))
      const selection = await screen.findByLabelText('Firmware version')
      await waitFor(() =>
        expect(Array.from((selection as HTMLSelectElement).options, (o) => o.text)).toEqual([
          '1.2.7',
        ])
      )
      expect((screen.getByRole('button', { name: 'Install' }) as HTMLButtonElement).disabled).toBe(
        false
      )
    }
  )
  it('only offers a revised build for the exact package authorized by the Hub', async () => {
    const user = userEvent.setup()
    const path = '/devices/pico-s/firmware'
    const revisedID = 'e9341cad98f2ca21eaffb6b3d713e4d7bae16112ad9964960cee55f38857e6ea'
    const revised = { ...uploaded, id: revisedID, firmware_version: '1.0.0', hub_ota_protocol: 1 }
    const original = { ...revised, kind: 'standalone', archive_sha256: 'd'.repeat(64) }
    let approved: string | undefined = revisedID
    fetchMock.mockImplementation(async (url, opts) => {
      if (String(url).endsWith(path))
        return json({
          model: 'pico-s',
          current_version: '1.0.0',
          install_supported: true,
          revised_package_id: approved,
          device_uid: 'paired-brewer',
          packages: [revised],
        })
      if (String(url).endsWith('/firmware/updates')) return json({ releases: [original] })
      if (String(url).endsWith('/install')) {
        approved = undefined
        return json({ state: 'running' }, 202)
      }
      return response(url, opts)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 'pico-s', onClose: close }))
    const select = (await screen.findByLabelText('Firmware version')) as HTMLSelectElement
    const release = await screen.findByRole('option', {
      name: '1.0.0 · Release catalog (dddddddddddd)',
    })
    const local = screen.getByRole('option', { name: '1.0.0 · Local upload (e9341cad98f2)' })
    expect(select.value).toBe(revised.id)
    await user.selectOptions(select, release)
    const blocked = screen.getByRole('button', { name: 'Already installed' }) as HTMLButtonElement
    expect(blocked.disabled).toBe(true)
    await user.click(blocked)
    expect(calls(path + '/install', 'POST')).toHaveLength(0)
    await user.selectOptions(select, local)
    await user.click(screen.getByRole('button', { name: 'Install revised build' }))
    await waitFor(() => expect(calls(path + '/install', 'POST')).toHaveLength(1))
    expect(JSON.parse(String(calls(path + '/install', 'POST')[0][1]?.body))).toEqual({
      device_uid: 'paired-brewer',
      package_id: revised.id,
      confirm: true,
    })
    expect(calls('/stage', 'POST')).toHaveLength(0)
    expect(
      ((await screen.findByRole('button', { name: 'Already installed' })) as HTMLButtonElement)
        .disabled
    ).toBe(true)
  })

  it('keeps a newer release ahead of the permitted same-version revision', async () => {
    const path = '/devices/pico-s/firmware'
    const revised = { ...uploaded, hub_ota_protocol: 1, firmware_version: '1.0.0' }
    const newer = { ...revised, id: 'd'.repeat(64), firmware_version: '1.0.1' }
    fetchMock.mockImplementation(async (url, opts) => {
      if (String(url).endsWith(path))
        return json({
          model: 'pico-s',
          current_version: '1.0.0',
          install_supported: true,
          revised_package_id: revised.id,
          packages: [revised, newer],
        })
      return response(url, opts)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 'pico-s', onClose: close }))
    const select = (await screen.findByLabelText('Firmware version')) as HTMLSelectElement
    expect(select.value).toBe(newer.id)
    expect(screen.getByRole('button', { name: 'Install' })).toBeTruthy()
  })

  it('allows finishing a pinned interrupted update even when the trial app reports the target version', async () => {
    const path = '/devices/pico-s/firmware'
    fetchMock.mockImplementation(async (url, opts) => {
      if (String(url).endsWith(path))
        return json({
          model: 'pico-s',
          device_uid: 'test-uid',
          current_version: uploaded.firmware_version,
          install_supported: true,
          retry_package_id: uploaded.id,
          packages: [
            { ...uploaded, hub_ota_protocol: 1 },
            { ...uploaded, id: 'd'.repeat(64), firmware_version: '9.9.0', hub_ota_protocol: 1 },
          ],
        })
      if (String(url).endsWith('/firmware/updates'))
        return json({
          releases: [
            { ...candidate, archive_sha256: uploaded.id, kind: 'standalone', hub_ota_protocol: 1 },
          ],
          notices: [],
        })
      if (String(url).endsWith('/stage')) return json(uploaded)
      if (String(url).endsWith('/install')) return json({ state: 'running' }, 202)
      return response(url, opts)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 'pico-s', onClose: close }))
    const user = userEvent.setup()
    const versions = await screen.findByLabelText('Firmware version')
    expect((versions as HTMLSelectElement).value).toBe(uploaded.id)
    await user.selectOptions(versions, '9.9.0')
    expect(screen.queryByRole('button', { name: 'Install' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Resume update' })).toBeNull()
    await user.selectOptions(versions, uploaded.id)
    await user.click(await screen.findByRole('button', { name: 'Resume update' }))
    await waitFor(() => expect(calls(path + '/install', 'POST')).toHaveLength(1))
    expect(calls('/stage', 'POST')).toHaveLength(1)
  })
  it.each([false, true])(
    'gates Pico C installation on controller verification and recovery support (OTA %s)',
    async (ota) => {
      const user = userEvent.setup()
      let verified = false
      fetchMock.mockImplementation(async (url, options) => {
        if (String(url).endsWith('/devices/c/firmware/hardware')) {
          verified = true
          return json({
            device_uid: 'pinned-c',
            display_variant: 'ssd1309',
            verified_at: '2026-11-20T00:00:00Z',
          })
        }
        if (String(url).endsWith('/devices/c/firmware'))
          return json({
            device_id: 'c',
            device_uid: 'pinned-c',
            model: 'pico-c',
            current_version: '1.2.0',
            display_variant: 'ssd1309',
            verified_display_variant: verified ? 'ssd1309' : '',
            hardware_verification_required: !verified,
            install_supported: verified && ota,
            reason:
              verified && ota
                ? 'Ready to install.'
                : 'Verify the controller and provision compatible recovery.',
            packages: [
              {
                ...uploaded,
                model: 'pico-c',
                model_family: 'pico-c',
                display_variant: 'ssd1309',
                hub_ota_protocol: ota ? 1 : 0,
              },
            ],
          })
        return response(url, options)
      })
      show(createElement(MachineFirmwareDialog, { deviceID: 'c', onClose: close }))
      const select = await screen.findByLabelText('Verified display controller')
      expect(screen.queryByRole('button', { name: /^Install$/ })).toBeNull()
      expect((select as HTMLSelectElement).value).toBe('')
      const save = screen.getByRole('button', { name: 'Save verified controller' })
      expect((save as HTMLButtonElement).disabled).toBe(true)
      await user.selectOptions(select, 'ssd1309')
      expect((save as HTMLButtonElement).disabled).toBe(true)
      await user.click(
        screen.getByRole('checkbox', {
          name: /I checked this machine's display controller independently/,
        })
      )
      await user.click(save)
      await waitFor(() => expect(notify).toHaveBeenCalledWith('Verified display controller saved.'))
      expect(
        ((await screen.findByLabelText('Firmware version')) as HTMLSelectElement).disabled
      ).toBe(!ota)
      expect(JSON.parse(String(calls('/devices/c/firmware/hardware', 'PUT')[0][1]?.body))).toEqual({
        device_uid: 'pinned-c',
        display_variant: 'ssd1309',
      })
      expect(
        fetchMock.mock.calls.filter(([url]) => /install|commands/.test(String(url)))
      ).toHaveLength(0)
      if (ota)
        expect(
          (screen.getByRole('button', { name: /^Install$/ }) as HTMLButtonElement).disabled
        ).toBe(false)
      else expect(screen.queryByRole('button', { name: /^Install$/ })).toBeNull()
    }
  )
  it('keeps another brewer’s USB prompt outside a machine-scoped restoration', async () => {
    fetchMock.mockImplementation(async (url, opts) => {
      if (String(url).endsWith('/devices/s/firmware'))
        return json({
          packages: [],
          stock_restore_guide: true,
          usb_restore_target: { model: 'pico-s', device_uid: 'saved' },
        })
      if (String(url).endsWith('/usb/devices'))
        return json({
          available: true,
          devices: [],
          job: {
            id: 'job',
            model: 'pico-s',
            device_id: 'other',
            action: 'restore',
            state: 'waiting',
            confirmation: 'prompt',
            message: 'Other machine confirmation',
          },
        })
      return response(url, opts)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 's', onClose: close }))
    await userEvent
      .setup()
      .click(await screen.findByRole('button', { name: 'Restore original firmware via USB' }))
    expect(await screen.findByText(/Another USB operation is in progress/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull()
    expect(screen.queryByText('Other machine confirmation')).toBeNull()
  })
  it('requires both confirmations before restoring over Wi-Fi and leaves USB available', async () => {
    let writes = 0
    fetchMock.mockImplementation(async (url, options) => {
      if (String(url).endsWith('/devices/s/firmware'))
        return json({
          model: 'pico-s',
          device_uid: 'paired',
          packages: [],
          stock_restore_wireless: true,
          stock_restore_guide: true,
          usb_restore_target: { model: 'pico-s', device_uid: 'paired' },
        })
      if (String(url).endsWith('/devices/s/firmware/restore-stock')) {
        expect(options?.method).toBe('POST')
        expect(JSON.parse(String(options?.body))).toEqual({
          device_uid: 'paired',
          acknowledge_loss_of_hub: true,
          confirm_restore: true,
        })
        writes++
        return json({}, 202)
      }
      return response(url, options)
    })
    show(createElement(MachineFirmwareDialog, { deviceID: 's', onClose: close }))
    const u = userEvent.setup()
    expect(
      await screen.findByRole('button', { name: 'Restore original firmware via USB' })
    ).toBeTruthy()
    await u.click(screen.getByRole('button', { name: 'Restore original firmware over Wi-Fi' }))
    const review = await screen.findByRole('button', { name: 'Review restoration' })
    expect((review as HTMLButtonElement).disabled).toBe(true)
    await u.click(screen.getByRole('checkbox'))
    await u.click(review)
    expect(writes).toBe(0)
    await u.click(await screen.findByRole('button', { name: 'Restore and restart brewer' }))
    await waitFor(() => expect(writes).toBe(1))
  })
  it.each(['pico-s', 'pico-c', 'picoferm', 'picostill', 'z-series'])(
    'starts a restore bound to the registered %s and awaits hardware verification',
    async (model) => {
      const user = userEvent.setup()
      fetchMock.mockImplementation(async (url, options) => {
        if (String(url).endsWith('/devices/s/firmware'))
          return json({
            packages: [],
            stock_restore_guide: true,
            usb_restore_target: { model, device_uid: 'saved-device' },
          })
        if (String(url).endsWith('/usb/devices'))
          return json({
            available: true,
            devices: [{ port: 'port-1', label: 'Brewer USB', models: [model] }],
          })
        if (String(url).endsWith('/usb/jobs'))
          return json(
            {
              id: 'job-1',
              device_id: 's',
              model,
              port: 'port-1',
              action: 'restore',
              state: 'waiting',
              message: 'Verify the brewer is idle and connected.',
              confirmation: 'hardware-1',
            },
            202
          )
        return response(url, options)
      })
      show(createElement(MachineFirmwareDialog, { deviceID: 's', onClose: close }))
      await user.click(
        await screen.findByRole('button', { name: 'Restore original firmware via USB' })
      )
      const start = await screen.findByRole('button', { name: 'Restore original firmware' })
      if (model === 'pico-c') {
        expect((start as HTMLButtonElement).disabled).toBe(true)
        await user.selectOptions(
          screen.getByLabelText('Physical Pico C display controller'),
          'ssd1325'
        )
      }
      expect(calls('/usb/jobs', 'POST')).toHaveLength(0)
      await user.click(start)
      expect(await screen.findByRole('button', { name: 'Continue' })).toBeTruthy()
      const [, options] = calls('/usb/jobs', 'POST')[0]
      expect(JSON.parse(String(options?.body))).toEqual({
        action: 'restore',
        model,
        port: 'port-1',
        device_id: 's',
        ...(model === 'pico-c' ? { display: 'ssd1325' } : {}),
      })
      expect(calls('/confirm', 'POST')).toHaveLength(0)
    }
  )
  it.each([
    { model: 'pico-s', device_uid: 's' },
    { model: 'pico-pro', stock_restore_guide: true },
    { model: 'unrecognized', device_uid: 'z', stock_restore_guide: true },
  ])(
    'hides the USB restore guide without a supported model and pinned identity: %j',
    async (device) => {
      fetchMock.mockImplementation(async (url, opts) =>
        String(url).endsWith('/devices/m/firmware')
          ? json({ ...device, current_version: '1.3.4', packages: [] })
          : response(url, opts)
      )
      show(createElement(MachineFirmwareDialog, { deviceID: 'm', onClose: close }))
      await screen.findByLabelText('Firmware version')
      expect(screen.queryByRole('button', { name: 'Restore original firmware via USB' })).toBeNull()
    }
  )
})
