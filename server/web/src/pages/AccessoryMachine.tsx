import { CareNotice, MachineCare } from '../components/MachineCare'
import { MachineManagement } from '../components/MachineManagement'
import { useState } from 'react'
import { ArrowUpRight, RefreshCw, Square } from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import { calibrationPrerequisite, commandLabels, sensorValue, supports } from '../lib/capabilities'
import { deviceModelLabel, deviceState, duration, sessionInProgress } from '../lib/utils'
import type { BrewDetail, Device } from '../types'
import {
  Badge,
  Button,
  Confirm,
  Empty,
  ErrorBanner,
  Field,
  Metric,
  PageTitle,
  SectionHeading,
} from '../components/ui'
import { DeviceDialog, DeviceKeyNotice, RemoveDeviceDialog } from '../components/DeviceDialogs'
import { DevicePreferences } from '../components/DevicePreferences'
import { SensorHistory } from '../components/SensorHistory'
import { PicoFermAssociation } from '../components/PicoFermAssociation'

import { MachineFirmwareDialog } from '../components/FirmwarePackages'
export default function AccessoryMachine({ device }: { device: Device }) {
  const [firmwareOpen, setFirmwareOpen] = useState(false)
  const hub = useHub(),
    id = device.id,
    status = device.status
  const commands = device.capabilities?.commands || []
  const [pending, setPending] = useState(''),
    [error, setError] = useState('')
  const [fanChannel, setFanChannel] = useState(1)
  const selectedFan = status?.actuators?.find((a) => a.id === 'fan' + fanChannel)
  const pumpRunning = status?.actuators?.find((actuator) => actuator.id === 'pump')?.enabled
  const intervalMs = device.capabilities?.history_sample_interval_ms
  const [accepted, setAccepted] = useState<Record<string, number>>({})
  const [editing, setEditing] = useState(false),
    [removing, setRemoving] = useState(false),
    [settings, setSettings] = useState(false)
  const [confirm, setConfirm] = useState<{
    command: string
    values: Record<string, unknown>
    label: string
  } | null>(null)
  const history =
    hub.brews.find((brew) => brew.device_id === id && brew.state === 'recording') ||
    hub.brews.find((brew) => brew.device_id === id)
  const trace = useResource<BrewDetail>(
    history ? `/brews/${history.id}` : null,
    status?.busy ? 10000 : 0
  )
  function allowed(name: string) {
    if (!device.online || pending || !commands.includes(name) || !status) return false
    switch (name) {
      case 'stop':
        return true
      case 'sample':
        return !['initializing', 'sampling', 'calibrating', 'storage-fault'].includes(
          status.state || ''
        )
      case 'set-sampling-interval':
        return !status.busy && status.state !== 'storage-fault'
      case 'set-fan':
      case 'set-pump':
        return status.ready && status.busy && !status.stopped
      default:
        return (
          status.ready &&
          !status.busy &&
          !(name === 'start' && status.state === 'calibration-required')
        )
    }
  }
  const label = (name: string) =>
    name === 'start' && supports(device.capabilities, 'fermentation')
      ? 'Start recording'
      : commandLabels[name] || name
  function request(command: string, values: Record<string, unknown> = {}) {
    setError('')
    setConfirm({ command, values, label: label(command) })
  }
  function coolingRequired(command: string, values: Record<string, unknown>) {
    return (
      device.model === 'picostill' &&
      command === 'set-fan' &&
      values.channel === 2 &&
      pumpRunning !== false &&
      (values.enabled === false || Number(values.level) < 61)
    )
  }
  async function send(command: string, values: Record<string, unknown> = {}) {
    if (!allowed(command) || coolingRequired(command, values)) {
      setConfirm(null)
      setError('The device state changed. Review its controls before trying again.')
      return
    }
    setPending(command)
    setError('')
    try {
      await api(`/devices/${id}/commands`, {
        method: 'POST',
        body: { command, ...values, confirm: true },
        command: true,
      })
      hub.notify(`${label(command)} acknowledged by ${device.name}.`)
      setConfirm(null)
      setAccepted((old) => ({ ...old, [command]: (old[command] || 0) + 1 }))
      await hub.refresh()
      trace.reload()
    } catch (error) {
      setError(
        error instanceof APIError
          ? errorMessage(error)
          : `${errorMessage(error)} Command delivery was not confirmed. Check the device before repeating it.`
      )
      setConfirm(null)
      await hub.refresh().catch(() => {})
    } finally {
      setPending('')
    }
  }
  async function refresh(action: 'test' | 'sync-history') {
    setPending(action)
    setError('')
    try {
      await api(`/devices/${id}/${action}`, { method: 'POST' })
      await hub.refresh()
      trace.reload()
      hub.notify(
        action === 'test' ? 'Device responded successfully.' : 'Retained session history imported.'
      )
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setPending('')
    }
  }
  const sensors = status?.sensors || []
  const intermittentWifi =
    device.model === 'picoferm' && supports(device.capabilities, 'intermittent_wifi')
  const lastReading =
    !device.online && intermittentWifi && device.last_seen
      ? new Date(device.last_seen).toLocaleString()
      : null
  return (
    <>
      <PageTitle
        back={() => hub.navigate('/overview')}
        eyebrow={`${deviceModelLabel(device)} · ${device.location || 'YOUR BREWERY'}`}
        title={device.name}
        description={device.base_url}
        actions={
          <>
            <Badge
              tone={
                !device.online
                  ? 'neutral'
                  : status?.stopped
                    ? 'danger'
                    : status?.busy
                      ? 'copper'
                      : 'green'
              }
            >
              {deviceState(device)}
            </Badge>
            <MachineManagement
              device={device}
              testing={!!pending}
              test={() => void refresh('test')}
              edit={() => setEditing(true)}
              preferences={() => setSettings(true)}
              remove={() => setRemoving(true)}
            />
          </>
        }
      />
      <ErrorBanner message={error || device.error || device.model_error || ''} />
      {intermittentWifi && !device.online && (
        <p className="help-text">
          PicoFerm switches Wi-Fi off between reports. Briefly press its button for two minutes of
          access to its controls and firmware updates.
        </p>
      )}
      <CareNotice maintenance={device.online ? device.status?.maintenance : undefined} />
      {device.key_reused && <DeviceKeyNotice />}
      <div className="machine-metrics accessory-metrics">
        {sensors.map((sensor) => (
          <Metric
            key={sensor.id}
            label={sensor.label}
            value={sensorValue(
              { ...sensor, value: device.online || lastReading ? sensor.value : null },
              hub.unit
            )}
            note={
              lastReading
                ? `Last seen ${lastReading}`
                : sensor.unit === 'raw'
                  ? 'Native raw reading'
                  : sensor.value === null
                    ? 'No current reading'
                    : 'Current reading'
            }
            accent={sensor.kind === 'temperature'}
          />
        ))}
        {!sensors.length && (
          <Metric
            label="Sensor readings"
            value="—"
            note={device.online ? 'Waiting for sensor data' : 'Device offline'}
          />
        )}
        {(status?.actuators || []).map((actuator) => (
          <Metric
            key={actuator.id}
            label={actuator.label}
            value={
              !device.online
                ? '—'
                : actuator.enabled === false
                  ? 'Off'
                  : actuator.level !== null
                    ? `${actuator.level}%`
                    : actuator.enabled === true
                      ? 'On'
                      : '—'
            }
            note={actuator.rpm !== null ? `${Math.round(actuator.rpm)} rpm` : 'Reported output'}
          />
        ))}
      </div>
      {device.model === 'picoferm' && device.adapter === 'pico-standalone' && (
        <PicoFermAssociation key={id} id={id} />
      )}
      <div className="machine-main accessory-main">
        <section className="panel">
          <SectionHeading
            title={
              supports(device.capabilities, 'fermentation')
                ? 'Fermentation session'
                : 'PicoStill session'
            }
            description={
              sessionInProgress(device)
                ? 'The device is running its current session.'
                : status?.busy
                  ? deviceState(device)
                  : 'Choose an available device operation.'
            }
          />
          <div className="accessory-actions">
            {commands
              .filter(
                (name) => !['stop', 'set-fan', 'set-pump', 'set-sampling-interval'].includes(name)
              )
              .filter((name) =>
                name === 'ack-clean-condenser'
                  ? status?.maintenance?.due.includes('clean_condenser')
                  : name === 'ack-sanitize-seal'
                    ? status?.maintenance?.due.includes('sanitize_seal')
                    : true
              )
              .map((name) => (
                <Button
                  key={name}
                  variant={name === 'start' ? 'primary' : 'secondary'}
                  disabled={!allowed(name)}
                  onClick={() => request(name)}
                >
                  {label(name)}
                </Button>
              ))}
          </div>
          <p className="help-text">
            {supports(device.capabilities, 'fermentation')
              ? 'Start and stop control the fermentation recording. Pressure calibration requires the unit to be removed and depressurized.'
              : 'Commands use the device’s native session modes. Review the selected operation before starting.'}
          </p>
          {commands.includes('stop') && (
            <div className="stop-row">
              <div>
                <strong>{status?.stopped ? 'Device stopped' : 'Stop current operation'}</strong>
                <small>
                  {status?.stop_reason && status.stop_reason !== 'none'
                    ? status.stop_reason
                    : supports(device.capabilities, 'pump')
                      ? 'Inhibit outputs and cancel the active operation.'
                      : 'End the active fermentation session.'}
                </small>
              </div>
              <Button
                variant="danger"
                disabled={!allowed('stop')}
                onClick={() => void send('stop')}
              >
                <Square size={14} fill="currentColor" />
                Stop
              </Button>
            </div>
          )}
        </section>
        <section className="panel">
          <SectionHeading
            title="Device controls"
            description="Only controls advertised by this device are available."
          />
          {commands.includes('set-pump') && (
            <ParameterControl
              key={[id, accepted['set-pump']].join('-')}
              command="set-pump"
              enabled={allowed('set-pump')}
              initial={{
                enabled: status?.actuators?.find((item) => item.id === 'pump')?.enabled === true,
              }}
              submit={request}
            />
          )}
          {commands.includes('set-fan') && (
            <ParameterControl
              key={[id, fanChannel, accepted['set-fan']].join('-')}
              command="set-fan"
              enabled={allowed('set-fan')}
              selectChannel={setFanChannel}
              minimumFanLevel={
                device.model === 'picostill' && fanChannel === 2 && pumpRunning !== false ? 61 : 0
              }
              initial={{
                channel: fanChannel,
                level: Math.round(selectedFan?.level ?? 0),
                enabled: selectedFan?.enabled === true,
              }}
              submit={request}
            />
          )}
          {commands.includes('set-sampling-interval') && (
            <ParameterControl
              command="set-sampling-interval"
              enabled={allowed('set-sampling-interval')}
              key={[id, accepted['set-sampling-interval']].join('-')}
              initial={{ value: intervalMs ? intervalMs / 1000 : 300 }}
              submit={request}
            />
          )}
          {!commands.some((name) =>
            ['set-pump', 'set-fan', 'set-sampling-interval'].includes(name)
          ) && <p className="help-text">This device has no additional manual controls.</p>}
        </section>
      </div>
      <section className="panel">
        <SectionHeading
          title="Readings through the session"
          description={
            history
              ? `${history.command || 'Session'} · ${history.sample_count.toLocaleString()} recorded samples`
              : 'Retained device history appears here after a session begins.'
          }
          actions={
            <div className="inline-actions">
              <Button
                variant="ghost"
                disabled={!device.online || !!pending}
                onClick={() => void refresh('sync-history')}
              >
                <RefreshCw size={15} />
                Import retained history
              </Button>
              {history && (
                <Button variant="secondary" onClick={() => hub.navigate(`/history/${history.id}`)}>
                  Open journal
                  <ArrowUpRight size={15} />
                </Button>
              )}
            </div>
          }
        />
        <ErrorBanner message={trace.error} retry={trace.reload} />
        {trace.data?.samples?.length ? (
          <SensorHistory brew={trace.data} unit={hub.unit} />
        ) : (
          <Empty
            title="Waiting for session readings"
            description="Recorded temperature, pressure and available output channels will appear here."
          />
        )}
      </section>
      <MachineCare model={device.model} />
      <div className="machine-footnotes">
        <button className="firmware-link" onClick={() => setFirmwareOpen(true)}>
          Firmware {device.capabilities?.firmware_version || '—'}
        </button>
        {firmwareOpen && (
          <MachineFirmwareDialog deviceID={device.id} onClose={() => setFirmwareOpen(false)} />
        )}
        <span>API v{device.capabilities?.api_version || '—'}</span>
        <span>UID {status?.device_uid || 'Not reported'}</span>
        <span>Uptime {duration(status?.uptime_ms || 0)}</span>
      </div>
      {confirm && (
        <Confirm
          title={`${confirm.label}?`}
          description={
            confirm.command === 'calibrate'
              ? calibrationPrerequisite
              : `Send ${confirm.label.toLowerCase()} to ${device.name}${
                  Object.keys(confirm.values).length
                    ? ` with ${Object.entries(confirm.values)
                        .map(([key, value]) => `${key.replaceAll('_', ' ')}: ${value}`)
                        .join(', ')}`
                    : ''
                }? Confirm the device is prepared for this operation.`
          }
          label={confirm.command === 'calibrate' ? 'Depressurized — calibrate' : confirm.label}
          busy={!!pending}
          onClose={() => !pending && setConfirm(null)}
          onConfirm={() => void send(confirm.command, confirm.values)}
        />
      )}
      {settings && <DevicePreferences id={id} onClose={() => setSettings(false)} />}
      {editing && <DeviceDialog device={device} onClose={() => setEditing(false)} />}
      {removing && (
        <RemoveDeviceDialog
          device={device}
          onClose={() => setRemoving(false)}
          onRemoved={() => hub.navigate('/overview')}
        />
      )}
    </>
  )
}

function ParameterControl({
  command,
  enabled,
  initial,
  selectChannel,
  minimumFanLevel = 0,
  submit,
}: {
  command: string
  enabled: boolean
  initial: Record<string, number | boolean>
  selectChannel?: (channel: number) => void
  minimumFanLevel?: number
  submit: (command: string, values: Record<string, unknown>) => void
}) {
  const [edited, setValues] = useState<typeof initial | null>(null)
  const values = edited ?? initial
  const insufficientCooling =
    minimumFanLevel > 0 && (values.enabled !== true || Number(values.level) < minimumFanLevel)
  return (
    <form
      className="accessory-parameter"
      onSubmit={(event) => {
        event.preventDefault()
        if (enabled && !insufficientCooling) submit(command, values)
      }}
    >
      {command === 'set-fan' && (
        <>
          <Field label="Fan">
            <select
              value={Number(values.channel)}
              disabled={!enabled}
              onChange={(event) => selectChannel?.(Number(event.target.value))}
            >
              <option value={1}>Fan 1</option>
              <option value={2}>Fan 2</option>
            </select>
          </Field>
          <Field
            label="Output level (%)"
            hint={
              minimumFanLevel
                ? 'Keep Fan 2 at 61% or higher until the pump is confirmed off.'
                : undefined
            }
          >
            <input
              type="number"
              min={minimumFanLevel}
              max={100}
              step={1}
              required
              value={Number(values.level)}
              disabled={!enabled}
              onChange={(event) => {
                const level = Number(event.target.value)
                setValues({ ...values, level, enabled: level > 0 })
              }}
            />
          </Field>
        </>
      )}
      {command === 'set-sampling-interval' ? (
        <Field label="Sampling interval (seconds)" hint="60–86,400 seconds.">
          <input
            type="number"
            required
            min={60}
            max={86400}
            step={1}
            value={Number(values.value)}
            disabled={!enabled}
            onChange={(event) => setValues({ ...values, value: Number(event.target.value) })}
          />
        </Field>
      ) : (
        <label className="check-field">
          <input
            type="checkbox"
            checked={values.enabled === true}
            disabled={!enabled || (minimumFanLevel > 0 && values.enabled === true)}
            onChange={(event) => setValues({ ...values, enabled: event.target.checked })}
          />
          {command === 'set-pump' ? 'Pump enabled' : 'Fan enabled'}
        </label>
      )}
      <Button variant="secondary" type="submit" disabled={!enabled || insufficientCooling}>
        {commandLabels[command]}
      </Button>
    </form>
  )
}
