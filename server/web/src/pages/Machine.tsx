import { CareNotice, MachineCare } from '../components/MachineCare'
import { MachineManagement } from '../components/MachineManagement'
import { useEffect, useState } from 'react'
import {
  ArrowDown,
  ArrowUp,
  ArrowUpRight,
  RefreshCw,
  Square,
  Play,
  Radio,
  BookOpen,
  Clock3,
  Copy,
  Loader2,
  Trash2,
  Pencil,
} from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import type { Display, MachineRecipe, DeviceSettings, BrewDetail } from '../types'
import {
  Badge,
  Button,
  Confirm,
  Empty,
  ErrorBanner,
  Field,
  Loading,
  Metric,
  PageTitle,
  SectionHeading,
} from '../components/ui'
import OLED from '../components/OLED'
import { TransferDialog } from '../components/TransferDialog'
import { InstalledProgramDialog } from '../components/InstalledProgramDialog'
import { BrewAIMonitor, type AIMonitorState } from '../components/BrewAIMonitor'
import { DeviceDialog, DeviceKeyNotice, RemoveDeviceDialog } from '../components/DeviceDialogs'
import { MachineFirmwareDialog } from '../components/FirmwarePackages'
import { TemperatureChart } from '../components/Charts'
import { dateLabel, deviceModelLabel, deviceState, duration, temperature } from '../lib/utils'
import AccessoryMachine from './AccessoryMachine'
import { DevicePreferences } from '../components/DevicePreferences'
import { brewerOutputsIdle, isAccessory, sensorValue, supports } from '../lib/capabilities'
import { SensorHistory } from '../components/SensorHistory'
import { LinkedPicoFerms } from '../components/PicoFermAssociation'
const programNames: Record<string, string> = {
  'sous-vide': 'Sous vide',
  'cold-brew': 'Cold brew',
  manual: 'Manual brew',
  rinse: 'Rinse',
  'first-rinse': 'First rinse',
  'deep-clean': 'Deep Clean',
  descale: 'Descale',
  circulate: 'Circulate',
  'drain-reservoir': 'Drain reservoir',
  'drain-filter': 'Drain filter',
  'rack-beer': 'Rack beer',
  clean: 'Clean',
  drain: 'Drain',
  chill: 'Chill',
  utilities: 'Utilities',
  settings: 'Settings',
}
const lockReasons: Record<string, string> = {
  'filter-removed': 'The filter was removed.',
  'sensor-io': 'The brewer reported a sensor error.',
  'recovery-storage': 'The brew checkpoint could not be saved.',
  initializing: 'The brewer is starting up.',
}
const displayPaused = 'The display mirror will resume when the program ends.'

function useBrewerDisplay(path: string | null, interval: number) {
  const [frame, setFrame] = useState<{
    path: string
    version: number
    data: Display | null
    error: string
  } | null>(null)
  const [version, setVersion] = useState(0)
  useEffect(() => {
    if (!path) {
      setFrame(null)
      return
    }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function readFrame(source: string) {
      let delay = interval
      try {
        const data = await api<Display>(source, { signal: controller.signal })
        if (!controller.signal.aborted) setFrame({ path: source, version, data, error: '' })
      } catch (failure) {
        if (controller.signal.aborted) return
        const busy = failure instanceof APIError && failure.code === 'device_display_busy'
        setFrame({
          path: source,
          version,
          data: null,
          error: busy ? displayPaused : errorMessage(failure),
        })
        if (busy) delay = Math.max(5000, interval)
      }
      if (!controller.signal.aborted) timer = setTimeout(() => void readFrame(source), delay)
    }
    void readFrame(path)
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [path, interval, version])
  return {
    data: frame?.path === path ? frame.data : null,
    error: frame?.path === path ? frame.error : '',
    // Keep displaying the last frame during a dial refresh. Its revision can
    // authorize another guarded input only after the new request succeeds.
    current: !!path && frame?.path === path && frame.version === version && !!frame.data,
    reload: () => setVersion((n) => n + 1),
  }
}

type MachineProps = { id: string; initialRecipe?: string }
export default function Machine({ id, initialRecipe }: MachineProps) {
  const hub = useHub()
  const device = hub.devices.find((item) => item.id === id)
  return device && isAccessory(device) ? (
    <AccessoryMachine device={device} />
  ) : (
    <BrewerMachine id={id} initialRecipe={initialRecipe} />
  )
}
function BrewerMachine({ id, initialRecipe }: MachineProps) {
  const hub = useHub()
  const device = hub.devices.find((device) => device.id === id)
  const simulated = device?.adapter === 'simulator'
  const hasPanel = supports(device?.capabilities, 'front_panel')
  const hasMirror = supports(device?.capabilities, 'display')
  const readOnly = hasMirror && !hasPanel && !device?.capabilities?.commands?.length
  const pausedMirror =
    device?.model === 'zymatic' &&
    readOnly &&
    !!device.status?.busy &&
    device.status.state !== 'recipe_pause'
  const display = useBrewerDisplay(
    device?.online && (hasPanel || hasMirror) && !pausedMirror ? `/devices/${id}/display` : null,
    readOnly ? 5000 : 2500
  )
  const guardedDial = supports(device?.capabilities, 'panel_revision')
  const turnEnabled =
    !guardedDial ||
    (display.current && display.data?.revision != null && display.data.dial === 'menu')
  const pressEnabled =
    turnEnabled ||
    (display.current && display.data?.revision != null && display.data.dial === 'press')
  const native = useResource<MachineRecipe[] | { recipes: MachineRecipe[] }>(
    device?.online && supports(device.capabilities, 'recipes') ? `/devices/${id}/recipes` : null
  )
  const [chosenProgram, setProgram] = useState('rinse')
  const [task, setTask] = useState<'recipes' | 'programs'>('recipes')
  const [monitorNextBrew, setMonitorNextBrew] = useState(false)
  const monitor = useResource<AIMonitorState>(device ? `/devices/${id}/ai-monitor` : null, 5000)
  const [selection, setRecipeID] = useState(initialRecipe || '')
  const [pending, setPending] = useState('')
  const [lastCommandAt, setLastCommandAt] = useState(0)
  const [confirm, setConfirm] = useState<{
    command: string
    recipe_id?: string
    label: string
  } | null>(null)
  const [settings, setSettings] = useState(false)
  const [firmware, setFirmware] = useState(false)
  const [editing, setEditing] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [testingConnection, setTestingConnection] = useState(false)
  const [removeRecipe, setRemoveRecipe] = useState(false)
  const [editRecipe, setEditRecipe] = useState<MachineRecipe | null>(null)
  const [transferRecipes, setTransferRecipes] = useState(false)
  const [error, setError] = useState('')
  const history =
    hub.brews.find((brew) => brew.device_id === id && brew.state === 'recording') ||
    hub.brews.find((brew) => brew.device_id === id)
  const trace = useResource<BrewDetail>(
    history ? `/brews/${history.id}` : null,
    device?.status?.busy ? 10000 : 0
  )
  const recipes = Array.isArray(native.data) ? native.data : native.data?.recipes || []
  const recipeID = recipes.find((recipe) => recipe.id === selection)?.id || ''
  function requestStart(value: NonNullable<typeof confirm>) {
    setMonitorNextBrew(false)
    setError('')
    setConfirm(value)
  }
  async function command(command: string, extra: Record<string, unknown> = {}) {
    setPending(command)
    setError('')
    const changesActivity = command !== 'encoder' || extra.click === true
    function awaitStatusAfter(time?: string | null) {
      const timestamp = Date.parse(time ?? '')
      if (changesActivity && Number.isFinite(timestamp))
        setLastCommandAt((previous) => Math.max(previous, timestamp))
    }
    awaitStatusAfter(device?.last_seen)
    try {
      const receipt = await api<{ at?: string }>(`/devices/${id}/commands`, {
        method: 'POST',
        body: {
          command,
          ...extra,
          ...(command === 'encoder' && guardedDial
            ? { display_revision: display.data?.revision }
            : {}),
        },
        command: true,
      })
      awaitStatusAfter(receipt?.at)
      if (command !== 'encoder')
        hub.notify(
          command === 'stop'
            ? 'Output lock acknowledged.'
            : command === 'clear-stop'
              ? 'Outputs unlocked.'
              : 'Command acknowledged by the brewer.'
        )
      setConfirm(null)
      if (command === 'brew' || command === 'manual') setMonitorNextBrew(false)
      if (command === 'restore-starter-recipes') native.reload()
      monitor.reload()
      display.reload()
      await hub.refresh()
    } catch (error) {
      if (command === 'encoder' && guardedDial) display.reload()
      setError(
        error instanceof APIError
          ? errorMessage(error)
          : `${errorMessage(error)} Command delivery was not confirmed. Inspect the machine before issuing another command.`
      )
      if (
        !(error instanceof APIError) ||
        ['ai_monitor_receipt_error', 'receipt_error', 'command_outcome_unknown'].includes(
          error.code
        )
      ) {
        setConfirm(null)
        setMonitorNextBrew(false)
        monitor.reload()
        display.reload()
        await hub.refresh()
      }
    } finally {
      setPending('')
    }
  }
  async function sync() {
    setPending('sync')
    try {
      await api(`/devices/${id}/sync-history`, { method: 'POST' })
      await hub.refresh()
      hub.notify('Retained brew history imported.')
    } catch (error) {
      hub.notify(errorMessage(error), 'error')
    } finally {
      setPending('')
    }
  }
  async function testConnection() {
    setTestingConnection(true)
    try {
      await api(`/devices/${id}/test`, { method: 'POST' })
      await hub.refresh()
      hub.notify('Machine responded successfully.')
    } catch (error) {
      await hub.refresh().catch(() => {})
      hub.notify(errorMessage(error), 'error')
    } finally {
      setTestingConnection(false)
    }
  }
  if (hub.loading) return <Loading />
  if (!device)
    return (
      <Empty
        title="Machine not found"
        description="This machine may have been removed from your brewery."
        action={<Button onClick={() => hub.navigate('/overview')}>Return to Brewery</Button>}
      />
    )
  const status = device.status
  const commands = device.capabilities?.commands || []
  const programs = Object.keys(programNames).filter((name) => commands.includes(name))
  const program = programs.includes(chosenProgram) ? chosenProgram : programs[0] || ''
  const hasRecipes = supports(device.capabilities, 'recipes')
  const hasPrograms = hasRecipes || programs.length > 0
  const showRecipes = hasRecipes && (task === 'recipes' || !programs.length)
  const live = device.online ? status : undefined
  const stepNumber =
    live?.busy && live.current_step >= 0 && live.current_step < live.step_count
      ? `Step ${live.current_step + 1} of ${live.step_count}`
      : ''
  const allowed = (name: string) => device.online && commands.includes(name)
  const cc3200 = ['pico-s', 'pico-pro', 'pico-c'].includes(device.model) && !simulated
  const controlsReady = !cc3200 || !!live?.ready
  const recovery = status?.recovery
  const interrupted = !!recovery && (recovery.available || !recovery.valid)
  const state = live?.stopped ? 'Outputs locked' : deviceState(device)
  const idle = hub.connected && !pending && brewerOutputsIdle(device, lastCommandAt)
  const lockLabel = live?.stopped
    ? 'Outputs locked'
    : idle
      ? 'Lock outputs'
      : 'Stop and lock outputs'
  const reason = live?.stop_reason
  const lockReason =
    reason && !['none', 'requested'].includes(reason)
      ? lockReasons[reason] || `The brewer reported ${reason}.`
      : ''
  const lockDescription = live?.stopped
    ? 'Heating and pumping stay off until you choose Unlock outputs.'
    : (idle
        ? 'Prevent heating and pumping until you choose Unlock outputs.'
        : 'Stop heating and pumping now and keep them off until you choose Unlock outputs.') +
      (device.model === 'z-series' && interrupted
        ? ' Locking outputs also discards the interrupted brew checkpoint.'
        : '')
  return (
    <>
      <PageTitle
        className="machine-heading"
        back={() => hub.navigate('/overview')}
        eyebrow={`${deviceModelLabel(device)} · ${device.location || 'YOUR BREWERY'}`}
        title={device.name}

        actions={
          <>
            <Badge
              tone={
                !device.online
                  ? 'neutral'
                  : status?.stopped
                    ? 'danger'
                    : status?.busy || interrupted
                      ? 'copper'
                      : 'green'
              }
            >
              {state}
            </Badge>
            <MachineManagement
              device={device}
              testing={testingConnection}
              test={() => void testConnection()}
              edit={() => setEditing(true)}
              preferences={() => setSettings(true)}
              preferencesDisabled={!device.online && device.adapter !== 'pico-standalone'}
              remove={() => setRemoving(true)}
            />
            {commands.includes('stop') && (
              <Button
                variant="danger"
                disabled={
                  !controlsReady || !allowed('stop') || pending === 'stop' || !!live?.stopped
                }
                onClick={() => void command('stop')}
                title={lockDescription}
              >
                <Square aria-hidden="true" size={14} fill="currentColor" /> {lockLabel}
              </Button>
            )}
          </>
        }
      />
      {device.error && <ErrorBanner message={device.error} />}
      <CareNotice maintenance={device.online ? device.status?.maintenance : undefined} />
      {device.model_error && (
        <div className="connection-advisory" role="status">
          <p>{device.model_error}</p>
          {device.model_selection_required && (
            <Button variant="secondary" onClick={() => setEditing(true)}>
              Choose machine model
            </Button>
          )}
        </div>
      )}
      {device.key_reused && <DeviceKeyNotice />}
      <ErrorBanner message={error} />
      {device.online && interrupted && (
        <section className="panel" aria-labelledby="recovery-heading">
          <SectionHeading
            title="Interrupted brew"
            description={
              recovery.valid
                ? `${recovery.recipe_name} · ${recovery.step_name || 'Preparation'}`
                : 'The saved checkpoint could not be verified. Discard it before starting another recipe.'
            }
          />
          <h3 id="recovery-heading">Review the saved checkpoint</h3>
          {recovery.valid && (
            <p>
              {recovery.step_seconds > 0
                ? `${duration(recovery.step_seconds * 1000)} remaining in the saved step`
                : 'Preparation or untimed phase'}
              {' · '}
              {duration(recovery.elapsed_seconds * 1000)} saved elapsed time
            </p>
          )}
          <p className="help-text">
            Verify the water, vessel and filter before resuming. Saved progress can be up to 30
            seconds behind the interruption. Resuming uses the saved remaining step time and may
            return to preparation prompts.
          </p>
          <div className="inline-actions">
            {recovery.valid && commands.includes('resume-interrupted') && (
              <Button
                variant="primary"
                disabled={
                  !allowed('resume-interrupted') ||
                  !recovery.available ||
                  (device.model === 'z-series' &&
                    (!status?.ready || status.filter_inserted === false)) ||
                  !!status?.busy ||
                  !!status?.stopped ||
                  !!pending
                }
                onClick={() =>
                  requestStart({ command: 'resume-interrupted', label: recovery.recipe_name })
                }
              >
                Resume interrupted brew
              </Button>
            )}
            {commands.includes('discard-interrupted') && (
              <Button
                variant="secondary"
                disabled={!allowed('discard-interrupted') || !!status?.busy || !!pending}
                onClick={() =>
                  requestStart({
                    command: 'discard-interrupted',
                    label: 'the interrupted checkpoint',
                  })
                }
              >
                Discard checkpoint
              </Button>
            )}
          </div>
        </section>
      )}
      <div className="machine-main">
        <section className="panel front-panel">
          <SectionHeading
            title={simulated ? 'Simulator controls' : 'Front panel'}
            actions={live?.filter_inserted === false && <Badge tone="amber">Filter removed</Badge>}
          />
          <OLED
            simulated={simulated}
            display={display.data}
            error={
              !device.online ? 'Machine offline' : pausedMirror ? displayPaused : display.error
            }
          />
          {commands.includes('encoder') && (
            <div className="dial-controls">
              <Button
                variant="secondary"
                aria-label="Move selection up"
                disabled={!controlsReady || !allowed('encoder') || !!pending || !turnEnabled}
                onClick={() => void command('encoder', { delta: -1 })}
              >
                <ArrowUp size={16} /> Up
              </Button>
              <Button
                disabled={!controlsReady || !allowed('encoder') || !!pending || !pressEnabled}
                onClick={() => void command('encoder', { click: true })}
              >
                Press dial
              </Button>
              <Button
                variant="secondary"
                aria-label="Move selection down"
                disabled={!controlsReady || !allowed('encoder') || !!pending || !turnEnabled}
                onClick={() => void command('encoder', { delta: 1 })}
              >
                Down <ArrowDown size={16} />
              </Button>
            </div>
          )}

          <footer className="machine-footnotes" aria-label="Machine information">
            {device.adapter === 'pico-standalone' ? (
              <button className="firmware-link" onClick={() => setFirmware(true)}>
                Firmware {device.capabilities?.firmware_version || '—'}
              </button>
            ) : (
              <span>Firmware {device.capabilities?.firmware_version || '—'}</span>
            )}
            <span>API v{device.capabilities?.api_version || '—'}</span>
            <span>UID {status?.device_uid || 'Not reported'}</span>
            <span>Uptime {live?.uptime_ms == null ? '—' : duration(live.uptime_ms)}</span>
          </footer>
        </section>
        <section className="panel program-panel">
          <SectionHeading
            title={
              !device.online
                ? 'Machine offline'
                : live?.stopped
                  ? 'Outputs locked'
                  : live?.busy
                    ? 'Current session'
                    : live
                      ? hasPrograms
                        ? 'Run a program'
                        : 'Machine status'
                      : 'Waiting for machine status'
            }
            description={
              !device.online
                ? device.enabled
                  ? 'Power on the brewer to reconnect.'
                  : 'Monitoring is off for this machine.'
                : live?.stopped
                  ? [lockReason, 'Heating and pumping stay off until you unlock the outputs.']
                      .filter(Boolean)
                      .join(' ')
                  : live?.busy
                    ? live.recipe_name || programNames[live.command] || live.command
                    : undefined
            }
          />
          {!device.online && device.last_seen && (
            <p className="help-text">Last connected {dateLabel(device.last_seen)}</p>
          )}
          {live?.busy && (stepNumber || live.step_name || live.step_seconds > 0) && (
            <div className="session-step">
              <span>{stepNumber || 'Current step'}</span>
              <strong>{live.step_name || 'Check the brewer display'}</strong>
              {live.step_seconds > 0 && (
                <span>
                  {duration(live.step_seconds * 1000)}{' '}
                  {device.adapter === 'simulator' ||
                  (device.model.startsWith('pico-') && live.phase === 8)
                    ? 'elapsed in this step'
                    : 'remaining in this step'}
                </span>
              )}
            </div>
          )}
          {live?.stopped && (
            <Button
              variant="secondary"
              className="full-width"
              disabled={
                !allowed('clear-stop') ||
                live.busy ||
                (cc3200 && (!live.ready || live.phase !== 4)) ||
                !!pending
              }
              onClick={() => requestStart({ command: 'clear-stop', label: 'unlock outputs' })}
            >
              Unlock outputs
            </Button>
          )}
          {live?.busy && (commands.includes('pause') || commands.includes('resume')) && (
            <div className="inline-actions">
              {['pause', 'resume']
                .filter((name) => commands.includes(name))
                .map((name) => (
                  <Button
                    key={name}
                    variant="secondary"
                    disabled={
                      !allowed(name) ||
                      !live.busy ||
                      live.stopped ||
                      (device.model === 'z-series' && live.state?.toLowerCase() !== 'running') ||
                      !!pending
                    }
                    onClick={() => void command(name, { confirm: true })}
                  >
                    {name === 'pause' ? 'Pause session' : 'Resume session'}
                  </Button>
                ))}
            </div>
          )}
          {live && !live.busy && !live.stopped && (
            <>
              {hasRecipes && programs.length > 0 && (
                <div className="program-switch" role="group" aria-label="Program type">
                  <button
                    type="button"
                    aria-pressed={showRecipes}
                    onClick={() => setTask('recipes')}
                  >
                    Saved recipes
                  </button>
                  <button
                    type="button"
                    aria-pressed={!showRecipes}
                    onClick={() => setTask('programs')}
                  >
                    Other programs
                  </button>
                </div>
              )}
              {showRecipes && (
                <>
                  {recipes.length > 0 ? (
                    <>
                      {initialRecipe && selection === initialRecipe && !recipeID && native.data && (
                        <p className="notice">
                          The sent recipe is no longer in this brewer’s recipe list.
                        </p>
                      )}
                      <Field label="Recipe on this machine">
                        <select
                          value={recipeID}
                          onChange={(event) => setRecipeID(event.target.value)}
                        >
                          <option value="">Choose a saved recipe</option>
                          {recipes.map((recipe) => (
                            <option key={recipe.id} value={recipe.id}>
                              {recipe.name}
                            </option>
                          ))}
                        </select>
                      </Field>
                      <Button
                        className="full-width"
                        disabled={
                          !recipeID ||
                          !allowed('brew') ||
                          !live.ready ||
                          !!pending ||
                          !!status?.busy ||
                          interrupted
                        }
                        onClick={() =>
                          requestStart({
                            command: 'brew',
                            recipe_id: recipeID,
                            label:
                              recipes.find((recipe) => recipe.id === recipeID)?.name ||
                              'this recipe',
                          })
                        }
                      >
                        <Play size={16} />
                        Start recipe
                      </Button>
                    </>
                  ) : (
                    <p>
                      {native.loading
                        ? 'Reading saved recipes…'
                        : native.error
                          ? 'Saved recipes are unavailable.'
                          : 'No recipes on this machine yet.'}
                    </p>
                  )}
                  <button
                    className="text-button recipe-link"
                    onClick={() => setTransferRecipes(true)}
                  >
                    <BookOpen size={15} />
                    Send recipes from the library
                    <ArrowUpRight size={14} />
                  </button>
                  <ErrorBanner message={native.error} retry={native.reload} />
                  {commands.includes('restore-starter-recipes') && (
                    <Button
                      variant="secondary"
                      disabled={!live.ready || !!pending}
                      onClick={() =>
                        requestStart({
                          command: 'restore-starter-recipes',
                          label: 'starter recipes',
                        })
                      }
                    >
                      Restore missing starter recipes
                    </Button>
                  )}
                  {native.data && !native.error && (
                    <div className="native-library">
                      <small>
                        {recipes.length} / {device.capabilities?.recipe_capacity || '—'} recipe
                        slots used
                      </small>
                      <Button
                        variant="ghost"
                        disabled={
                          !recipeID ||
                          !live.ready ||
                          !device.online ||
                          !status?.ready ||
                          !!status?.busy ||
                          !!status?.stopped ||
                          !!pending
                        }
                        onClick={() =>
                          setEditRecipe(recipes.find((recipe) => recipe.id === recipeID) || null)
                        }
                      >
                        <Pencil size={13} />
                        Edit saved program
                      </Button>
                      <Button
                        variant="ghost"
                        className="danger-text"
                        disabled={
                          !recipeID ||
                          !live.ready ||
                          !device.online ||
                          !!status?.busy ||
                          !!status?.stopped ||
                          !!pending
                        }
                        onClick={() => setRemoveRecipe(true)}
                      >
                        <Trash2 size={13} />
                        Remove selected copy
                      </Button>
                    </div>
                  )}
                </>
              )}
              {!showRecipes && programs.length > 0 && (
                <>
                  <Field label="Other programs">
                    <select value={program} onChange={(event) => setProgram(event.target.value)}>
                      {programs.map((command) => (
                        <option key={command} value={command}>
                          {programNames[command]}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Button
                    variant="secondary"
                    className="full-width"
                    disabled={
                      !allowed(program) || !live.ready || !!pending || !!status?.busy || interrupted
                    }
                    onClick={() =>
                      requestStart({ command: program, label: programNames[program] || program })
                    }
                  >
                    Open program
                    <ArrowUpRight size={16} />
                  </Button>
                </>
              )}
              {!hasRecipes && !programs.length && (
                <p>
                  {readOnly
                    ? 'Select and start programs at the brewer.'
                    : 'Use the front panel to select a program.'}
                </p>
              )}
            </>
          )}
          {live && (
            <section className="console-readings" aria-label="Live readings">
              <h3>Live readings</h3>
              <div className="machine-metrics">
                <Metric
                  label="Wort temperature"
                  value={temperature(live.wort_f, hub.unit)}
                  accent
                />
                <Metric label="Thermoblock" value={temperature(live.thermoblock_f, hub.unit)} />
                {hasPrograms && (
                  <>
                    <Metric
                      label="Target"
                      value={
                        live.target_f == null
                          ? '—'
                          : live.target_f
                            ? temperature(live.target_f, hub.unit)
                            : 'Unheated'
                      }
                      note={live.step_name || ''}
                    />
                    <Metric
                      label="Step time"
                      value={
                        live.busy && live.step_seconds > 0
                          ? duration(live.step_seconds * 1000)
                          : '—'
                      }
                      note={stepNumber}
                    />
                  </>
                )}
              </div>
            </section>
          )}
        </section>
      </div>
      {!!live?.sensors?.length && (
        <div className="machine-metrics accessory-metrics">
          {live.sensors.map((sensor) => (
            <Metric
              key={sensor.id}
              label={sensor.label}
              value={sensorValue(sensor, hub.unit)}
              note="Device sensor"
            />
          ))}
        </div>
      )}
      <section className="panel">
        <SectionHeading
          title="Readings through the brew"
          description={
            history
              ? `${history.recipe_name || history.command} · ${history.sample_count.toLocaleString()} recorded samples`
              : 'The hub records available telemetry while it is connected.'
          }
          actions={
            <div className="inline-actions">
              {(simulated || supports(device.capabilities, 'history_persistent')) && (
                <Button
                  variant="ghost"
                  disabled={!device.online || !!pending}
                  onClick={() => void sync()}
                >
                  <RefreshCw size={15} />
                  Import retained history
                </Button>
              )}
              {history && (
                <Button variant="secondary" onClick={() => hub.navigate(`/history/${history.id}`)}>
                  Open journal
                  <ArrowUpRight size={15} />
                </Button>
              )}
            </div>
          }
        />
        {trace.data?.samples?.length ? (
          (trace.data.sample_fields?.length || 0) > 11 ? (
            <SensorHistory brew={trace.data} unit={hub.unit} />
          ) : (
            <TemperatureChart samples={trace.data.samples} unit={hub.unit} />
          )
        ) : (
          <Empty
            icon={<Radio size={24} />}
            title="Waiting for brew readings"
            description={
              hasPrograms
                ? 'Run a program to begin recording temperatures, phases and controller state.'
                : 'Recorded temperatures appear here during a program.'
            }
          />
        )}
      </section>
      <LinkedPicoFerms brewerId={device.id} />
      <MachineCare model={device.model} />
      <BrewAIMonitor
        deviceID={id}
        deviceName={device.name}
        monitor={monitor.data}
        error={monitor.error}
        loading={monitor.loading}
        reload={monitor.reload}
      />
      {confirm && (
        <Confirm
          title={
            confirm.command === 'restore-starter-recipes'
              ? 'Restore missing starter recipes?'
              : confirm.command === 'clear-stop'
                ? 'Unlock outputs?'
                : confirm.command === 'resume-interrupted'
                  ? `Resume ${confirm.label}?`
                  : confirm.command === 'discard-interrupted'
                    ? 'Discard the interrupted checkpoint?'
                    : `Start ${confirm.label}?`
          }
          description={
            confirm.command === 'restore-starter-recipes'
              ? 'Add the firmware’s missing starter recipes to the brewer. Existing recipes are preserved.'
              : confirm.command === 'clear-stop'
                ? 'Unlocking allows the brewer to heat and pump again. Confirm the operation has ended and the brewer is ready.'
                : confirm.command === 'resume-interrupted'
                  ? 'Verify the water, vessel and filter before resuming. Saved progress can be up to 30 seconds behind the interruption. The brewer resumes with its saved remaining step time and may return to preparation prompts.'
                  : confirm.command === 'discard-interrupted'
                    ? 'This saved operation cannot be resumed afterward. Discard its checkpoint?'
                    : `This sends a command to ${device.name}. Confirm the brewer is prepared.${monitorNextBrew && ['brew', 'manual'].includes(confirm.command) ? ' AI monitoring is requested for this brew.' : ''}`
          }
          label={
            confirm.command === 'restore-starter-recipes'
              ? 'Restore recipes'
              : confirm.command === 'clear-stop'
                ? 'Unlock outputs'
                : confirm.command === 'resume-interrupted'
                  ? 'Resume brew'
                  : confirm.command === 'discard-interrupted'
                    ? 'Discard checkpoint'
                    : 'Start program'
          }
          danger={confirm.command === 'discard-interrupted'}
          busy={!!pending}
          onClose={() => !pending && setConfirm(null)}
          onConfirm={() =>
            void command(confirm.command, {
              ...(confirm.recipe_id ? { recipe_id: confirm.recipe_id } : {}),
              ...(['brew', 'manual'].includes(confirm.command)
                ? { ai_monitor: monitorNextBrew }
                : {}),
              confirm: true,
            })
          }
        >
          <ErrorBanner message={error} />
          {['brew', 'manual'].includes(confirm.command) && (
            <>
              <label className="check-field">
                <input
                  type="checkbox"
                  checked={monitorNextBrew}
                  disabled={
                    !monitor.data?.configured || !!monitor.error || monitor.loading || !!pending
                  }
                  onChange={(event) => setMonitorNextBrew(event.target.checked)}
                />
                Monitor this brew with AI
              </label>
              <p className="help-text">
                {monitor.data?.configured ? (
                  'Optional observations for this session. Monitoring ends with this brew; it does not control heating or pumping.'
                ) : (
                  <>
                    To enable optional brew monitoring,{' '}
                    <a href="#/settings">configure an AI provider in Settings</a>.
                  </>
                )}
              </p>
              <ErrorBanner message={monitor.error} retry={monitor.reload} />
            </>
          )}
        </Confirm>
      )}
      {removeRecipe && (
        <Confirm
          title="Remove this recipe from the machine?"
          description={`This frees a saved-recipe slot on ${device.name}. The shared recipe library remains available.`}
          label="Remove machine copy"
          danger
          busy={pending === 'remove-recipe'}
          onClose={() => setRemoveRecipe(false)}
          onConfirm={async () => {
            setPending('remove-recipe')
            setError('')
            try {
              await api(`/devices/${id}/recipes/${encodeURIComponent(recipeID)}`, {
                method: 'DELETE',
              })
              setRecipeID('')
              native.reload()
              setRemoveRecipe(false)
              hub.notify('Saved machine recipe removed.')
            } catch (error) {
              setError(errorMessage(error))
              setRemoveRecipe(false)
            } finally {
              setPending('')
            }
          }}
        />
      )}{' '}
      {settings && <DevicePreferences id={id} onClose={() => setSettings(false)} />}
      {firmware && <MachineFirmwareDialog deviceID={id} onClose={() => setFirmware(false)} />}
      {transferRecipes && (
        <TransferDialog
          recipes={hub.recipes}
          initialDeviceID={id}
          onClose={() => {
            setTransferRecipes(false)
            native.reload()
          }}
          onComplete={native.reload}
        />
      )}
      {editRecipe && (
        <InstalledProgramDialog
          deviceID={id}
          initial={editRecipe}
          onClose={() => setEditRecipe(null)}
          onSaved={native.reload}
        />
      )}
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
