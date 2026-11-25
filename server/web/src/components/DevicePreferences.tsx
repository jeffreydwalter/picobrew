import { useEffect, useState } from 'react'
import { useHub, useResource } from '../context'
import { api, errorMessage } from '../lib/api'
import { isAccessory, preferenceFields } from '../lib/capabilities'
import type { DeviceSettings } from '../types'
import { Button, ErrorBanner, Field, Loading, Modal } from './ui'
import { PicoStillLink } from './PicoStillLink'
import { HubConnectionSettings } from './HubConnectionSettings'

export function DevicePreferences({ id, onClose }: { id: string; onClose: () => void }) {
  const hub = useHub()
  const device = hub.devices.find((item) => item.id === id)
  const fields = preferenceFields(device)
  const resource = useResource<Partial<DeviceSettings>>(
    device?.online && fields.length ? `/devices/${id}/settings` : null
  )
  const [form, setForm] = useState<Partial<DeviceSettings> | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    if (resource.data) setForm(resource.data)
  }, [resource.data])
  async function save() {
    if (!form) return
    setBusy(true)
    setError('')
    try {
      const body = Object.fromEntries(Object.entries(form).filter(([key]) => fields.includes(key)))
      await api(`/devices/${id}/settings`, { method: 'PATCH', body })
      hub.notify('Device preferences saved.')
      resource.reload()
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title={device && isAccessory(device) ? 'Device preferences' : 'Brewer preferences'}
      onClose={onClose}
      wide
    >
      <ErrorBanner message={error || resource.error} />
      {device?.online && form && fields.length > 0 ? (
        <div className="settings-row compact">
          <div>
            <h3>Device preferences</h3>
            <p>Saved on the device.</p>
          </div>
          <div className="settings-fields">
            <div className="form-grid">
              {fields.includes('temperature_unit') && (
                <Field label="Temperature unit">
                  <select
                    value={form.temperature_unit || 'F'}
                    onChange={(event) =>
                      setForm({ ...form, temperature_unit: event.target.value as 'F' | 'C' })
                    }
                  >
                    <option value="F">Fahrenheit (°F)</option>
                    <option value="C">Celsius (°C)</option>
                  </select>
                </Field>
              )}
              {fields.includes('language') && (
                <Field label="Front-panel language">
                  <select
                    value={form.language || 'en'}
                    onChange={(event) => setForm({ ...form, language: event.target.value })}
                  >
                    <option value="en">English</option>
                    <option value="es">Español</option>
                    <option value="fr">Français</option>
                  </select>
                </Field>
              )}
            </div>
            {fields.includes('auto_cycle') && (
              <label className="check-field">
                <input
                  type="checkbox"
                  checked={form.auto_cycle === true}
                  onChange={(event) => setForm({ ...form, auto_cycle: event.target.checked })}
                />
                Automatically cycle brew status screens
              </label>
            )}
            {fields.includes('detailed_status') && (
              <label className="check-field">
                <input
                  type="checkbox"
                  checked={form.detailed_status === true}
                  onChange={(event) => setForm({ ...form, detailed_status: event.target.checked })}
                />
                Show detailed status
              </label>
            )}
            <Button disabled={busy} onClick={() => void save()}>
              {busy ? 'Saving…' : 'Save preferences'}
            </Button>
          </div>
        </div>
      ) : resource.loading ? (
        <Loading />
      ) : fields.length ? (
        <p className="help-text">
          {device && isAccessory(device)
            ? 'Connect the device to change its preferences.'
            : 'Connect the machine to change its display preferences.'}
        </p>
      ) : null}
      {device?.model === 'z-series' && device.capabilities?.picostill_brewer_link_v1 && (
        <PicoStillLink brewer={device} />
      )}
      {device && <HubConnectionSettings device={device} />}
    </Modal>
  )
}
