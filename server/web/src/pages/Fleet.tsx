import './fleet.scss'
import { BrewGroupManager, BrewInvitations } from '../components/BrewGroups'
import { StockBrewerFleet, useStockBrewerFleet } from '../components/StockBrewerFleet'
import { careTasksDue } from '../components/MachineCare'
import { useEffect, useRef, useState } from 'react'
import {
  ArrowUpRight,
  ArrowRight,
  Plus,
  FlaskConical,
  BookOpen,
  Radio,
  Pencil,
  Trash2,
  KeyRound,
} from 'lucide-react'
import { useHub, useResource } from '../context'
import type { BrewDetail, Device, FirmwareUpdates } from '../types'
import {
  Badge,
  Button,
  Empty,
  IconButton,
  Loading,
  PageTitle,
  SectionHeading,
} from '../components/ui'
import { DeviceDialog, RemoveDeviceDialog } from '../components/DeviceDialogs'
import { MachineFirmwareDialog } from '../components/FirmwarePackages'
import { SensorHistory } from '../components/SensorHistory'
import { isAccessory, supports } from '../lib/capabilities'
import { Sparkline, TemperatureChart } from '../components/Charts'
import {
  dateLabel,
  deviceModelLabel,
  deviceAttention,
  deviceState,
  sessionInProgress,
  duration,
  temperature,
} from '../lib/utils'
export default function Fleet({
  addMachine = false,
  usbJobId,
}: {
  addMachine?: boolean
  usbJobId?: string
}) {
  const hub = useHub()
  const { devices, brews, unit, navigate, loading } = hub
  const stock = useStockBrewerFleet(!hub.session.demo)
  const stockCount = stock.picos.length + stock.zs.length
  const [editing, setEditing] = useState<Device | 'new' | null>(addMachine ? 'new' : null)
  const updates = useResource<FirmwareUpdates>(
    devices.some((d) => d.adapter === 'pico-standalone') ? '/firmware/updates' : null,
    60000
  )
  const [firmwareID, setFirmwareID] = useState('')
  const [removing, setRemoving] = useState<Device | null>(null)
  useEffect(() => {
    if (addMachine) setEditing('new')
  }, [addMachine])
  function closeEditor() {
    setEditing(null)
    if (addMachine) navigate('/overview')
  }
  const attention = devices.filter((device) => deviceAttention(device))
  const active = devices.filter(sessionInProgress)
  const lastBrew = brews.find((brew) => brew.state === 'completed')
  const trace = useResource<BrewDetail>(lastBrew ? `/brews/${lastBrew.id}` : null)
  const prioritized = new Set([...attention, ...active])
  const ordered = [...prioritized, ...devices.filter((device) => !prioritized.has(device))]
  const [traces, setTraces] = useState<Record<string, { value: number | null }[]>>({})
  const previous = useRef('')
  useEffect(() => {
    const signature = JSON.stringify(devices.map((device) => [device.id, device.status?.uptime_ms]))
    if (signature === previous.current) return
    previous.current = signature
    setTraces((old) => {
      const next = { ...old }
      for (const device of devices)
        next[device.id] = [
          ...(old[device.id] || []),
          {
            value: device.online
              ? ((isAccessory(device) ? device.status?.temperature_f : device.status?.wort_f) ??
                null)
              : null,
          },
        ].slice(-40)
      return next
    })
  }, [devices])
  if (loading) return <Loading />
  return (
    <>
      <PageTitle
        title="Brewery"
        description={[
          devices.length || !stockCount
            ? `${devices.filter((d) => d.online).length} of ${devices.length} machines connected`
            : '',
          stockCount ? `${stockCount} stock ${stockCount === 1 ? 'brewer' : 'brewers'}` : '',
          `${active.length} monitored session${active.length === 1 ? '' : 's'} in progress`,
          attention.length ? `${attention.length} awaiting attention` : '',
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <>
            <Button variant="secondary" onClick={() => setEditing('new')}>
              <Plus size={16} />
              Add machine
            </Button>
            <Button onClick={() => navigate('/recipes/new')}>
              <Plus size={16} />
              Create recipe
            </Button>
          </>
        }
      />
      {updates.data?.notices?.map((n) => (
        <p key={n.device_id} role="status">
          {n.name} has firmware {n.version} available.{' '}
          <Button variant="secondary" onClick={() => setFirmwareID(n.device_id)}>
            View firmware
          </Button>
        </p>
      ))}
      {firmwareID && (
        <MachineFirmwareDialog deviceID={firmwareID} onClose={() => setFirmwareID('')} />
      )}
      <BrewGroupManager devices={devices} />
      <BrewInvitations devices={devices} />
      {devices.length ? (
        <div className="brewery-list">
          {ordered.map((device) => (
            <MachineCard
              key={device.id}
              device={device}
              values={traces[device.id] || []}
              onEdit={() => setEditing(device)}
              onRemove={() => setRemoving(device)}
            />
          ))}
        </div>
      ) : !stockCount && !stock.loading && !stock.error ? (
        <div className="panel">
          <Empty
            title="Make room for your first brewer."
            description="Add a brewer running stock or standalone firmware."
            action={
              <Button onClick={() => setEditing('new')}>
                <Plus size={16} />
                Connect a machine
              </Button>
            }
          />
        </div>
      ) : null}
      <StockBrewerFleet fleet={stock} />
      <div className="overview-bottom">
        <section className="panel trace-panel">
          <SectionHeading
            title="Last completed brew"
            description={lastBrew ? `${lastBrew.recipe_name} · ${lastBrew.device_name}` : undefined}
            actions={
              lastBrew && (
                <Button variant="ghost" onClick={() => navigate(`/history/${lastBrew.id}`)}>
                  Open journal
                  <ArrowUpRight size={15} />
                </Button>
              )
            }
          />
          {trace.data?.samples?.length ? (
            ['picoferm', 'picostill'].includes(trace.data.model) ? (
              <SensorHistory brew={trace.data} unit={unit} height={255} />
            ) : (
              <TemperatureChart samples={trace.data.samples} unit={unit} height={255} />
            )
          ) : (
            <Empty
              icon={<Radio size={25} />}
              title="No readings yet"
              description="Completed brews with recorded samples appear here."
            />
          )}
        </section>
        <section className="panel recent-panel">
          <SectionHeading
            title="From your brew journal"
            actions={
              <button className="text-button" onClick={() => navigate('/history')}>
                <ArrowRight size={18} />
              </button>
            }
          />
          {brews.length ? (
            <div className="recent-list">
              {brews.slice(0, 4).map((brew) => (
                <button key={brew.id} onClick={() => navigate(`/history/${brew.id}`)}>
                  <span className="brew-icon">
                    <FlaskConical size={19} />
                  </span>
                  <span>
                    <strong>{brew.recipe_name || brew.command || 'Brew session'}</strong>
                    <small>
                      {brew.device_name} · {dateLabel(brew.started_at || brew.observed_at)}
                    </small>
                  </span>
                  <ArrowUpRight size={16} />
                </button>
              ))}
            </div>
          ) : (
            <Empty
              icon={<BookOpen size={23} />}
              title="Your first chapter awaits."
              description="Completed and live brews are collected here automatically."
            />
          )}
        </section>
      </div>
      {editing && (
        <DeviceDialog
          key={editing === 'new' ? usbJobId || 'new' : editing.id}
          usbJobId={
            editing === 'new' && usbJobId && /^[a-f0-9]{32}$/.test(usbJobId) ? usbJobId : undefined
          }
          device={editing === 'new' ? undefined : editing}
          onClose={closeEditor}
        />
      )}
      {removing && <RemoveDeviceDialog device={removing} onClose={() => setRemoving(null)} />}
    </>
  )
}
function MachineCard({
  device,
  values,
  onEdit,
  onRemove,
}: {
  device: Device
  values: { value: number | null }[]
  onEdit: () => void
  onRemove: () => void
}) {
  const { unit } = useHub()
  const status = device.online ? device.status : undefined
  const due = careTasksDue(status?.maintenance)
  const busy = !!status && sessionInProgress(device)
  const accessory = isAccessory(device)
  const readOnly =
    supports(device.capabilities, 'display') &&
    !supports(device.capabilities, 'front_panel') &&
    !device.capabilities?.commands?.length
  const attention = deviceAttention(device)
  const step =
    busy && status.current_step >= 0 && status.current_step < status.step_count
      ? status.current_step + 1
      : 0
  return (
    <article className={`machine-card brewery-row ${busy ? 'brewing' : ''}`}>
      <div className="brewery-identity">
        <div className="row-illustration" aria-hidden="true">
          {accessory ? <FlaskConical size={36} /> : <BrewerIllustration active={busy} />}
        </div>
        <div>
          <small>
            {deviceModelLabel(device)}
            {device.location ? ' · ' + device.location : ''}
          </small>
          <h3>{device.name}</h3>
          <Badge
            tone={
              !device.online
                ? 'neutral'
                : status?.stopped
                  ? 'danger'
                  : busy || attention
                    ? 'copper'
                    : 'green'
            }
          >
            {deviceState(device)}
          </Badge>
        </div>
      </div>
      <div className="brewery-session">
        <small>{attention ? 'Needs attention' : busy ? 'Current session' : 'Session'}</small>
        <strong>
          {attention
            ? status?.recovery?.recipe_name || status?.recipe_name || attention.title
            : busy
              ? status.recipe_name ||
                (device.model === 'picoferm' ? 'Fermentation recording' : status.command) ||
                'Program in progress'
              : !device.online
                ? 'Machine offline'
                : status?.stopped
                  ? 'Outputs stopped'
                  : status?.busy
                    ? deviceState(device)
                    : status?.ready
                      ? 'Ready for the next session'
                      : readOnly && status
                        ? 'Programs start at the brewer'
                        : status?.state === 'monitoring' &&
                            supports(device.capabilities, 'front_panel')
                          ? 'Select a program with the front panel'
                          : 'Waiting for machine status'}
        </strong>
        {attention && <span>{attention.instruction}</span>}
        {due.length > 0 && <span>Maintenance due · {due.join(' · ')}</span>}
        {busy && !attention && (
          <>
            <span>
              {[step ? `Step ${step} of ${status.step_count}` : '', status.step_name]
                .filter(Boolean)
                .join(' · ')}
            </span>
            {step > 0 && (
              <progress aria-label="Current step" value={step} max={status.step_count} />
            )}
            {status.step_seconds > 0 && (
              <small>
                {duration(status.step_seconds * 1000)}{' '}
                {device.adapter === 'simulator' ||
                (device.model.startsWith('pico-') && status.phase === 8)
                  ? 'elapsed in step'
                  : 'left in step'}
              </small>
            )}
          </>
        )}
      </div>
      <div className="brewery-reading">
        <small>{accessory ? 'Temperature' : 'Wort temperature'}</small>
        <strong>{temperature(accessory ? status?.temperature_f : status?.wort_f, unit)}</strong>
        {busy && status.target_f != null && status.target_f > 0 && (
          <span>Target {temperature(status.target_f, unit)}</span>
        )}
        {status?.pressure_kpa != null && <span>{status.pressure_kpa.toFixed(2)} kPa</span>}
        {status?.battery_v != null && <span>{status.battery_v.toFixed(2)} V battery</span>}
        {status && values.length > 1 && <Sparkline values={values} />}
      </div>
      <div className="brewery-actions">
        <a
          className="machine-card-link"
          href={`#/devices/${device.id}`}
          aria-label={readOnly ? `View ${device.name}` : `Open ${device.name} controls`}
        >
          {readOnly ? 'View machine' : 'Open controls'} <ArrowUpRight size={16} />
        </a>
        <div className="machine-card-actions">
          <IconButton label={`Edit ${device.name}`} onClick={onEdit}>
            <Pencil size={16} />
          </IconButton>
          <IconButton label={`Remove ${device.name}`} onClick={onRemove}>
            <Trash2 size={16} />
          </IconButton>
        </div>
      </div>
      {(device.key_reused || device.model_error) && (
        <div className="brewery-alert">
          {device.key_reused && (
            <span>
              <KeyRound size={14} /> Shared API key
            </span>
          )}
          {device.model_error && (
            <span>
              {device.model_error}{' '}
              {device.model_selection_required && (
                <button className="text-button" onClick={onEdit}>
                  Choose machine model
                </button>
              )}
            </span>
          )}
        </div>
      )}
    </article>
  )
}
function BrewerIllustration({ active }: { active: boolean }) {
  return (
    <svg className="brewer-illustration" viewBox="0 0 140 160" aria-hidden="true">
      <ellipse cx="72" cy="148" rx="51" ry="6" fill="#dfd9cf" />
      <path d="M30 25Q30 17 38 17H105Q113 17 113 25V140H30Z" fill="#d9d8d0" />
      <path d="M30 25Q30 17 38 17H105Q113 17 113 25V49H30Z" fill="#45483f" />
      <path d="M38 52H105V136H38Z" fill="#ecebe5" />
      <rect x="45" y="28" width="39" height="12" rx="2" fill="#1c2b23" />
      <path d="M50 34H77M50 37H69" stroke={active ? '#c4dbab' : '#79876d'} strokeWidth="1.5" />
      <circle cx="98" cy="34" r="5" fill="#c4c6bc" />
      <path d="M43 64H102V75H43Z" fill="#cbc9bf" />
      <path d="M48 78H98V119Q98 125 92 125H54Q48 125 48 119Z" fill="#9d8770" />
      <path d="M53 81H93V118H53Z" fill={active ? '#b66c3c' : '#c1aa8f'} opacity=".75" />
      <rect x="62" y="58" width="22" height="5" rx="2" fill="#6e7167" />
      <rect x="35" y="140" width="12" height="5" rx="2" fill="#45483f" />
      <rect x="96" y="140" width="12" height="5" rx="2" fill="#45483f" />
      {active && (
        <>
          <path
            d="M58 11Q53 6 58 1M72 11Q67 6 72 1M86 11Q81 6 86 1"
            fill="none"
            stroke="#bca68f"
            strokeWidth="1.5"
            strokeLinecap="round"
          />
        </>
      )}
    </svg>
  )
}
