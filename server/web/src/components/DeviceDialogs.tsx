import { useEffect, useRef, useState, type FormEvent } from 'react'
import { KeyRound, Loader2, Plus, RefreshCw, Save, Trash2 } from 'lucide-react'
import { useHub } from '../context'
import { api, errorMessage } from '../lib/api'
import { deviceModelLabel, liveReadingsStatus, modelLabel } from '../lib/utils'
import type { Device } from '../types'
import { Button, ErrorBanner, Field, Modal } from './ui'
import { StockSetupGuide } from './StockSetupGuide'
import { StockBrewerSetup } from './StockBrewerFleet'
import { NetworkDiscovery, type NetworkCandidate } from './NetworkDiscovery'

export function DeviceKeyNotice() {
  return (
    <div className="connection-advisory" role="status">
      <KeyRound size={16} />
      <p>
        The hub has the same saved API key for another machine. Use each brewer's own provisioned
        key when configuring its connection.
      </p>
    </div>
  )
}

export function HubConnectionStatus({ device }: { device: Device }) {
  return (
    <p className="help-text" role="status">
      {liveReadingsStatus(device.telemetry)}
    </p>
  )
}

export function DeviceDialog({
  device,
  usbJobId,
  onClose,
}: {
  device?: Device
  usbJobId?: string
  onClose: () => void
}) {
  const hub = useHub()
  const live = hub.devices.find((item) => item.id === device?.id) || device
  const [observed, setObserved] = useState<Device | null>(null)
  const current = observed || live
  useEffect(() => setObserved(null), [live])
  const [form, setForm] = useState({
    name: device?.name || '',
    model: device?.model || (hub.session.demo ? 'pico-pro' : ''),
    adapter: device?.adapter || (hub.session.demo ? 'simulator' : 'pico-standalone'),
    base_url: device?.base_url || '',
    api_key: '',
    location: device?.location || '',
    enabled: device?.enabled ?? true,
  })
  const [busy, setBusy] = useState(false)
  const [identifying, setIdentifying] = useState(false)
  const [error, setError] = useState('')
  const [usbSetup, setUSBSetup] = useState(false)
  const [stockSetup, setStockSetup] = useState<NetworkCandidate | true | null>(null)
  const [manualKey, setManualKey] = useState(false)
  const [candidate, setCandidate] = useState<NetworkCandidate | null>(null)
  const [pairCode, setPairCode] = useState('')
  const request = useRef<AbortController | null>(null)
  useEffect(() => () => request.current?.abort(), [])
  const locked = busy || identifying
  const physical = form.adapter === 'pico-standalone'
  const selected = physical && candidate?.base_url === form.base_url ? candidate : null
  const keyOnly = selected?.model === 'picoferm' || selected?.model === 'picostill'
  const invalidIdentity = ['invalid_model', 'unsupported_model'].includes(
    current?.model_error_code || ''
  )
  const manualModel =
    physical &&
    !invalidIdentity &&
    !!(current?.model_selection_required || current?.model_source === 'manual')
  const connectionChanged = !!form.api_key || form.base_url !== device?.base_url
  function selectCandidate(item: NetworkCandidate) {
    if (item.source === 'pico-register' || (item.source === 'stock' && item.model === 'z-series')) {
      setStockSetup(item)
      return
    }
    if (!item.base_url) return
    const address = item.base_url
    setCandidate(item)
    setManualKey(false)
    setError('')
    setForm((previous) => ({
      ...previous,
      name:
        previous.name && previous.name !== candidate?.name
          ? previous.name
          : item.name || modelLabel(item.model),
      base_url: address,
      api_key: '',
    }))
  }
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (locked) return
    setBusy(true)
    setError('')
    try {
      const controller = new AbortController()
      request.current = controller
      const body = { ...form, model: physical && !manualModel ? '' : form.model }
      let addedDevice: Device | undefined
      if (!device && physical && !manualKey && !keyOnly && !usbJobId) {
        const start = await api<{ pairing_id: string; code: string }>('/devices/pair', {
          method: 'POST',
          body: { base_url: form.base_url },
          signal: controller.signal,
        })
        setPairCode(start.code)
        for (;;) {
          if (controller.signal.aborted) return
          const result = await api<Device | { pairing: 'pending' }>('/devices', {
            method: 'POST',
            body: { ...body, pairing_id: start.pairing_id },
            signal: controller.signal,
          })
          if (!('pairing' in result)) break
          await new Promise((resolve) => setTimeout(resolve, 1500))
        }
      } else {
        addedDevice = await api<Device>(device ? `/devices/${device.id}` : '/devices', {
          method: device ? 'PUT' : 'POST',
          body: { ...body, ...(physical && !device && usbJobId ? { usb_job_id: usbJobId } : {}) },
          signal: controller.signal,
        })
      }
      await hub.refresh()
      hub.notify(
        device
          ? 'Machine connection updated.'
          : physical
            ? 'Machine added. The Hub is identifying it and setting up live readings.'
            : 'Machine added. The hub is connecting now.'
      )
      onClose()
      if (
        !device &&
        addedDevice &&
        (selected?.model === 'picoferm' || addedDevice.model === 'picoferm')
      ) {
        hub.navigate(`/devices/${addedDevice.id}`)
      }
    } catch (error) {
      if (!request.current?.signal.aborted) setError(errorMessage(error))
    } finally {
      setPairCode('')
      setBusy(false)
    }
  }
  async function identify() {
    if (!device || locked || connectionChanged) return
    setIdentifying(true)
    setError('')
    try {
      setObserved(await api<Device>(`/devices/${device.id}/test`, { method: 'POST' }))
      await hub.refresh()
      hub.notify('Machine identity checked.')
    } catch (error) {
      setError(errorMessage(error))
      try {
        setObserved(await api<Device>(`/devices/${device.id}`))
        await hub.refresh()
      } catch {
        // Keep the existing identity when the connection itself is unavailable.
      }
    } finally {
      setIdentifying(false)
    }
  }
  const models =
    form.adapter === 'simulator'
      ? hub.profiles.length
        ? hub.profiles.map((profile) => ({ id: profile.id, name: profile.name }))
        : [
            { id: 'pico-pro', name: 'Pico Pro' },
            { id: 'pico-s', name: 'Pico S' },
            { id: 'z', name: 'Pico Z' },
          ]
      : [
          { id: 'pico-pro', name: 'Pico Pro' },
          { id: 'pico-s', name: 'Pico S' },
        ]
  if (stockSetup)
    return (
      <StockBrewerSetup
        candidate={stockSetup === true ? undefined : stockSetup}
        onBack={() => setStockSetup(null)}
        onClose={onClose}
      />
    )
  if (usbSetup) return <StockSetupGuide onClose={() => setUSBSetup(false)} />
  return (
    <Modal
      title={device ? 'Edit machine connection' : 'Add machine'}
      onClose={() => {
        if (!locked || pairCode) onClose()
      }}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={locked && !pairCode}>
            Cancel
          </Button>
          <Button form="device-form" type="submit" disabled={locked}>
            {busy ? (
              <Loader2 size={16} className="spin" />
            ) : device ? (
              <Save size={16} />
            ) : (
              <Plus size={16} />
            )}{' '}
            {device ? 'Save machine' : 'Add machine'}
          </Button>
        </>
      }
    >
      {!device && physical && !hub.session.demo && (
        <>
          <NetworkDiscovery disabled={locked} onSelect={selectCandidate} onClose={onClose} />
          <Button variant="secondary" disabled={locked} onClick={() => setStockSetup(true)}>
            Add a stock brewer
          </Button>
        </>
      )}
      {!device && physical && !hub.session.demo && (
        <Button
          className="stock-setup-entry"
          variant="secondary"
          disabled={locked}
          onClick={() => setUSBSetup(true)}
        >
          USB setup and recovery
        </Button>
      )}
      <form id="device-form" onSubmit={submit}>
        <div className="form-grid">
          <Field label="Machine name">
            <input
              autoFocus
              required
              maxLength={100}
              disabled={locked}
              value={form.name}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
              placeholder="Brewer · Brew studio"
            />
          </Field>
          <Field label="Location">
            <input
              maxLength={160}
              disabled={locked}
              value={form.location}
              onChange={(event) => setForm({ ...form, location: event.target.value })}
              placeholder="Brew studio"
            />
          </Field>
          <Field
            label="Connection type"
            hint={device ? 'Fixed for this machine record.' : undefined}
          >
            <select
              disabled={!!device || locked}
              value={form.adapter}
              onChange={(event) => {
                setCandidate(null)
                setForm({
                  ...form,
                  adapter: event.target.value,
                  model: event.target.value === 'simulator' ? 'pico-pro' : '',
                  base_url: '',
                  api_key: '',
                })
              }}
            >
              {!hub.session.demo && (
                <option value="pico-standalone">Pico standalone firmware</option>
              )}
              <option value="simulator">Virtual brewer / simulator</option>
            </select>
          </Field>
          <Field
            label="Machine model"
            hint={
              physical
                ? manualModel
                  ? 'This firmware does not identify the model. Choose it once, or install the firmware for your machine to enable automatic identification.'
                  : current?.model_source === 'firmware'
                    ? 'Reported by the machine. The Hub checks it when connecting.'
                    : 'The Hub reads the model from the machine when it connects.'
                : undefined
            }
          >
            {!physical || manualModel ? (
              <select
                required
                disabled={(!physical && !!device) || locked}
                value={form.model}
                onChange={(event) => setForm({ ...form, model: event.target.value })}
              >
                {manualModel && <option value="">Choose the machine model</option>}
                {models.map((model) => (
                  <option key={model.id} value={model.id}>
                    {model.name}
                  </option>
                ))}
              </select>
            ) : (
              <input
                readOnly
                value={
                  current
                    ? deviceModelLabel(current)
                    : selected?.model
                      ? modelLabel(selected.model)
                      : 'Detect automatically'
                }
              />
            )}
          </Field>
        </div>
        {form.adapter === 'pico-standalone' && (
          <>
            <Field
              label="Machine URL"
              hint="The hub must be able to reach this address on your network."
            >
              <input
                type="url"
                required
                maxLength={1024}
                disabled={locked}
                value={form.base_url}
                onChange={(event) => {
                  setCandidate(null)
                  setForm({ ...form, base_url: event.target.value })
                }}
                placeholder="http://picobrew.local"
              />
            </Field>
            {usbJobId ? (
              <p className="help-text">
                The Hub will use the key it saved during USB setup and check the machine’s identity.
              </p>
            ) : (
              <>
                <p className="help-text" role="status">
                  {pairCode ? (
                    <>
                      On a Pico S, Pro, C or Z, open{' '}
                      <strong>Settings → About → Pair with Hub</strong>. On a Zymatic, open{' '}
                      <strong>About Zymatic</strong> and wait for the pairing prompt. Compare{' '}
                      <strong>{pairCode}</strong> with the code on the brewer. If they match, turn
                      and press the dial to approve.
                    </>
                  ) : device?.key_configured ? (
                    'Paired. The Hub has this machine’s API key for its controls and local website.'
                  ) : keyOnly ? (
                    'Use the device key from USB setup to connect this accessory.'
                  ) : (
                    'The Hub saves the device API key when you confirm pairing on the brewer.'
                  )}
                </p>
                {!keyOnly && (
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={locked}
                    aria-expanded={manualKey}
                    onClick={() => {
                      setManualKey(!manualKey)
                      setForm({ ...form, api_key: '' })
                    }}
                  >
                    {manualKey
                      ? 'Cancel key entry'
                      : device?.key_configured
                        ? 'Replace saved key'
                        : 'Use an existing key instead'}
                  </Button>
                )}
                {(manualKey || keyOnly) && (
                  <>
                    <Field
                      label="USB setup key file"
                      hint="For older firmware, select the api.key file saved during setup."
                    >
                      <input
                        type="file"
                        disabled={locked}
                        onChange={async (event) => {
                          const file = event.target.files?.[0]
                          if (!file) return
                          if (file.size > 128) {
                            setError('Choose the small api.key file from USB setup.')
                            return
                          }
                          try {
                            const value = (await file.text()).trim()
                            if (!/^[0-9a-f]{64}$/i.test(value)) {
                              setError('This file does not contain a device API key.')
                              return
                            }
                            setError('')
                            setForm((v) => ({ ...v, api_key: value }))
                          } catch {
                            setError('The browser could not read the key file.')
                          }
                        }}
                      />
                    </Field>
                    <Field label="Machine API key">
                      <input
                        type="password"
                        autoComplete="new-password"
                        maxLength={1024}
                        disabled={locked}
                        required={!device?.key_configured}
                        value={form.api_key}
                        onChange={(event) => setForm({ ...form, api_key: event.target.value })}
                      />
                    </Field>
                  </>
                )}
              </>
            )}
            {device?.key_reused && <DeviceKeyNotice />}
            {current?.model_error && (
              <p className="help-text" role="status">
                {current.model_error}
              </p>
            )}
            {device && (
              <>
                <Button
                  type="button"
                  variant="secondary"
                  disabled={locked || connectionChanged || !current?.enabled}
                  onClick={() => void identify()}
                >
                  <RefreshCw size={16} className={identifying ? 'spin' : ''} />
                  Identify machine
                </Button>
                {connectionChanged && (
                  <p className="help-text">Save connection changes before checking the machine.</p>
                )}
                {current && <HubConnectionStatus device={current} />}
              </>
            )}
          </>
        )}
        <label className="check-field">
          <input
            type="checkbox"
            disabled={locked}
            checked={form.enabled}
            onChange={(event) => setForm({ ...form, enabled: event.target.checked })}
          />
          Monitor this machine
        </label>
        <ErrorBanner message={error} />
      </form>
    </Modal>
  )
}

export function RemoveDeviceDialog({
  device,
  onClose,
  onRemoved,
}: {
  device: Device
  onClose: () => void
  onRemoved?: () => void
}) {
  const hub = useHub()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function remove() {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await api(`/devices/${device.id}`, { method: 'DELETE' })
      await hub.refresh()
      hub.notify('Machine removed from the brewery. Brew history is retained.')
      onClose()
      onRemoved?.()
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title={`Remove ${device.name}?`}
      onClose={() => {
        if (!busy) onClose()
      }}
      actions={
        <>
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="danger" disabled={busy} onClick={() => void remove()}>
            {busy ? <Loader2 size={16} className="spin" /> : <Trash2 size={16} />}
            Remove machine
          </Button>
        </>
      }
    >
      <p>
        The hub will stop monitoring {device.name}. Its recorded brew history stays in your brewery.
      </p>
      <p className="help-text">
        This removes the connection from the hub. It does not erase the brewer, change its recipes
        or stop an operation on the machine.
      </p>
      <ErrorBanner message={error} />
    </Modal>
  )
}
