import { useEffect, useRef, useState } from 'react'
import { useHub, useResource } from '../context'
import { api, errorMessage } from '../lib/api'
import { liveReadingsStatus } from '../lib/utils'
import type { Device, Telemetry } from '../types'
import { Button, ErrorBanner, Field } from './ui'

type ConnectionFields = Pick<Telemetry, 'enabled' | 'host' | 'port'>

function fields(value?: Partial<ConnectionFields>): ConnectionFields {
  return { enabled: value?.enabled ?? false, host: value?.host || '', port: value?.port || 8080 }
}

export function HubConnectionSettings({ device }: { device: Device }) {
  if (device.adapter !== 'pico-standalone') {
    return <p className="help-text">Live readings stay in this Hub for virtual brewers.</p>
  }
  return <ConnectionForm key={device.id} device={device} />
}

function ConnectionForm({ device }: { device: Device }) {
  const hub = useHub()
  const connection = useResource<Telemetry>(`/devices/${device.id}/telemetry`)
  const [form, setForm] = useState<ConnectionFields>(() => fields(device.telemetry))
  const [status, setStatus] = useState<Partial<Telemetry> | undefined>(device.telemetry)
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dirty = useRef(false)
  const latestFleet = useRef(device.telemetry)
  const requestedWithFleet = useRef(device.telemetry)
  useEffect(() => {
    if (device.telemetry === latestFleet.current) return
    latestFleet.current = device.telemetry
    if (device.telemetry) {
      if (!dirty.current) setForm(fields(device.telemetry))
      setStatus(device.telemetry)
    }
  }, [device.telemetry])
  useEffect(() => {
    if (!connection.data) return
    if (latestFleet.current === requestedWithFleet.current) {
      if (!dirty.current) setForm(fields(connection.data))
      setStatus(connection.data)
    }
    setLoaded(true)
  }, [connection.data])

  function reload() {
    requestedWithFleet.current = latestFleet.current
    connection.reload()
  }

  function edit(next: ConnectionFields) {
    dirty.current = true
    setForm(next)
    setError('')
  }

  async function save(useHubReceiver = false) {
    if (busy || connection.loading || !loaded) return
    setBusy(true)
    setError('')
    try {
      const result = await api<Telemetry>(`/devices/${device.id}/telemetry`, {
        method: 'PUT',
        body: useHubReceiver ? { mode: 'automatic' } : form,
      })
      setForm(fields(result))
      setStatus(result)
      dirty.current = false
      await hub.refresh()
      hub.notify('Connection saved.')
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="settings-row compact" aria-label="Live readings">
      <div>
        <h3>Live readings</h3>
      </div>
      <div className="settings-fields">
        <form
          onSubmit={(event) => {
            event.preventDefault()
            void save()
          }}
        >
          <div className="form-grid">
            <Field label="Receiver host">
              <input
                value={form.host}
                required={form.enabled}
                maxLength={127}
                disabled={busy}
                onChange={(event) => edit({ ...form, host: event.target.value })}
              />
            </Field>
            <Field label="Receiver port">
              <input
                type="number"
                min="1"
                max="65535"
                required
                value={form.port}
                disabled={busy}
                onChange={(event) => edit({ ...form, port: Number(event.target.value) })}
              />
            </Field>
          </div>
          <label className="check-field">
            <input
              type="checkbox"
              checked={form.enabled}
              disabled={busy}
              onChange={(event) => edit({ ...form, enabled: event.target.checked })}
            />
            Send live readings
          </label>
          <div className="inline-actions">
            <Button type="submit" disabled={busy || connection.loading || !loaded}>
              {busy ? 'Saving…' : 'Save connection'}
            </Button>
            {device.model === 'picoferm' && (
              <Button
                type="button"
                variant="secondary"
                disabled={busy || connection.loading || !loaded}
                onClick={() => void save(true)}
              >
                Use this Hub
              </Button>
            )}
          </div>
          {(dirty.current || connection.loading || !connection.error) && (
            <p className="help-text" role="status">
              {dirty.current
                ? 'Unsaved changes.'
                : connection.loading
                  ? 'Loading saved connection…'
                  : (device.model === 'picoferm' && status?.message) || liveReadingsStatus(status)}
            </p>
          )}
          <ErrorBanner
            message={error || connection.error}
            retry={connection.error ? reload : undefined}
          />
        </form>
      </div>
    </section>
  )
}
