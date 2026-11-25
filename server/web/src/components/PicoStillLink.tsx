import { useState } from 'react'
import { useHub, useResource } from '../context'
import { api, errorMessage } from '../lib/api'
import type { Device } from '../types'
import { Button, ErrorBanner, Field } from './ui'

type LinkState = {
  paired: boolean
  verified: boolean
  checking: boolean
  uid: string
  host: string
  port: number
  session_logging: boolean
  sample?: {
    temperature_c: number[]
    pressure_psi: number
    pump_running: boolean
    fan_2_running: boolean
    program_running: boolean
  }
  error?: string
}

export function PicoStillLink({ brewer }: { brewer: Device }) {
  const { devices } = useHub()
  const path = '/devices/' + encodeURIComponent(brewer.id) + '/picostill'
  const resource = useResource<LinkState>(brewer.online ? path : null, 3000)
  const [chosen, setChosen] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const link = resource.data
  const stills = devices.filter(
    (d) =>
      d.adapter === 'pico-standalone' &&
      d.model === 'picostill' &&
      d.capabilities?.picostill_brewer_link_v1
  )
  const current = stills.find((d) => d.status?.device_uid === link?.uid)
  async function change(method: 'PUT' | 'DELETE' | 'PATCH', body?: Record<string, unknown>) {
    setError('')
    setBusy(true)
    try {
      const next = await api<LinkState>(path, { method, body })
      resource.setData(next)
      setChosen('')
    } catch (e) {
      setError(errorMessage(e))
      resource.reload()
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="settings-row compact">
      <div>
        <h3>PicoStill</h3>
        <p>
          {link?.checking
            ? 'The Z is verifying the accessory.'
            : link?.paired
              ? `Paired with ${current?.name || link.host}.`
              : 'Pair a PicoStill to use it from this Z.'}
        </p>
      </div>
      <div className="settings-fields">
        <ErrorBanner message={error || resource.error || link?.error || ''} />
        {brewer.online && !resource.error && link?.sample && (
          <p>
            {link.sample.temperature_c
              .map(
                (value, i) =>
                  ['Inlet', 'Outlet', 'Pot', 'Ambient'][i] + ' ' + value.toFixed(1) + ' °C'
              )
              .join(' · ')}
            {' · Pressure ' + link.sample.pressure_psi.toFixed(2) + ' psi'}
            {' · Pump ' + (link.sample.pump_running ? 'on' : 'off')}
            {' · Cooling fan ' + (link.sample.fan_2_running ? 'on' : 'off')}
          </p>
        )}
        <Field label="PicoStill">
          <select value={chosen} onChange={(e) => setChosen(e.target.value)}>
            <option value="">Choose an accessory</option>
            {stills.map((d) => (
              <option
                key={d.id}
                value={d.id}
                disabled={!d.online || d.status?.busy || d.status?.device_uid === link?.uid}
              >
                {d.name}
                {!d.online ? ' · Offline' : ''}
              </option>
            ))}
          </select>
        </Field>
        <div className="inline-actions">
          <Button
            variant="secondary"
            disabled={
              !link || !chosen || busy || link.checking || !brewer.online || brewer.status?.busy
            }
            onClick={() => void change('PUT', { picostill_id: chosen })}
          >
            Pair PicoStill
          </Button>
          {link?.paired && (
            <Button
              variant="ghost"
              disabled={busy || link.checking || !brewer.online || brewer.status?.busy}
              onClick={() => void change('DELETE')}
            >
              Unpair
            </Button>
          )}
        </div>
        {link?.paired && (
          <label className="check-field">
            <input
              type="checkbox"
              disabled={busy || link.checking || !brewer.online || brewer.status?.busy}
              checked={link.session_logging}
              onChange={(e) => void change('PATCH', { session_logging: e.target.checked })}
            />{' '}
            Record PicoStill sessions
          </label>
        )}
      </div>
    </div>
  )
}
