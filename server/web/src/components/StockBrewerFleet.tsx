import { useEffect, useState, type FormEvent } from 'react'
import { Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, errorMessage } from '../lib/api'
import { routeLabel } from '../lib/programs'
import { dateLabel, duration, modelLabel, temperature } from '../lib/utils'
import { Button, Confirm, ErrorBanner, Field, IconButton, Modal, SectionHeading } from './ui'
import { StockRecipeTransfer, type StockTarget } from './StockRecipeTransfer'
import type { NetworkCandidate } from './NetworkDiscovery'

type MenuItem = { recipe_id: string; revision: number; rfid?: string; program: { name: string } }
type StockPicoReport = {
  last_state?: number
  state_at?: string
  last_error?: { error_code?: number; rfid?: string }
  error_at?: string
}
type StockPico = {
  uid: string
  model: string
  name: string
  items: MenuItem[]
  report?: StockPicoReport
}
type StockZ = { id: string; name: string; base_url: string }
const changeEvent = 'stock-brewers-changed'

export function useStockBrewerFleet(enabled: boolean) {
  const picos = useResource<{ machines: StockPico[]; recent_uids: string[] }>(
    enabled ? '/stock-picos' : null,
    30000
  )
  const zs = useResource<{ machines: StockZ[] }>(enabled ? '/stock-z-programs' : null)
  useEffect(() => {
    if (!enabled) return
    function changed() {
      picos.reload()
      zs.reload()
    }
    window.addEventListener(changeEvent, changed)
    return () => window.removeEventListener(changeEvent, changed)
  }, [enabled, picos.reload, zs.reload])
  return {
    picos: picos.data?.machines || [],
    zs: zs.data?.machines || [],
    loading: enabled && (picos.loading || zs.loading),
    error: picos.error || zs.error,
  }
}

export function StockBrewerSetup({
  candidate,
  onBack,
  onClose,
}: {
  candidate?: NetworkCandidate
  onBack: () => void
  onClose: () => void
}) {
  const hub = useHub()
  const [model, setModel] = useState(candidate?.model === 'z-series' ? 'z-series' : '')
  const [name, setName] = useState('')
  const [uid, setUID] = useState(candidate?.serial || '')
  const [address, setAddress] = useState(candidate?.base_url || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const z = model === 'z-series'
  const recent = useResource<{ recent_uids: string[] }>(
    !z && !hub.session.demo ? '/stock-picos' : null
  )
  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await api(z ? '/stock-z-programs' : '/stock-picos', {
        method: 'POST',
        body: z
          ? { name: name.trim(), base_url: address.trim() }
          : { name: name.trim(), model, uid: uid.trim() },
      })
      window.dispatchEvent(new Event(changeEvent))
      hub.notify(`${name.trim()} added to the brewery.`)
      onClose()
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title="Add a stock brewer"
      onClose={() => {
        if (!busy) onBack()
      }}
      actions={
        <>
          <Button variant="secondary" disabled={busy} onClick={onBack}>
            Back
          </Button>
          <Button type="submit" form="stock-brewer-form" disabled={busy}>
            <Plus size={16} />
            Add stock brewer
          </Button>
        </>
      }
    >
      <form id="stock-brewer-form" onSubmit={submit}>
        <Field label="Model on the brewer">
          <select value={model} required disabled={busy} onChange={(e) => setModel(e.target.value)}>
            <option value="">Choose a model</option>
            {['pico-c', 'pico-s', 'pico-pro', 'z-series'].map((v) => (
              <option key={v} value={v}>
                {modelLabel(v)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Machine name">
          <input
            value={name}
            disabled={busy}
            required
            maxLength={100}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        {z ? (
          <Field
            label="Program Maker address"
            hint="Enter the stock Z module’s local HTTP address. The Hub checks for Program Maker before adding it."
          >
            <input
              type="url"
              required
              disabled={busy}
              placeholder="http://192.168.1.42"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
            />
          </Field>
        ) : (
          <>
            <Field
              label="Stock brewer UID"
              hint="Use the 32 hexadecimal characters from this brewer’s registration request."
            >
              <input
                required
                disabled={busy}
                maxLength={32}
                pattern="[0-9A-Fa-f]{32}"
                value={uid}
                onChange={(e) => setUID(e.target.value)}
              />
            </Field>
            {!!recent.data?.recent_uids?.length && (
              <Field label="Recent registration requests">
                <select
                  value={recent.data.recent_uids.includes(uid) ? uid : ''}
                  disabled={busy}
                  onChange={(e) => setUID(e.target.value)}
                >
                  <option value="">Choose a UID</option>
                  {recent.data.recent_uids.map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            <p className="help-text">
              On the brewer’s network, point picobrew.com to the Hub. Forward HTTP port 80 requests
              for /API/pico/ to the Hub, then connect the brewer to Wi-Fi. Match its registration
              code to the last eight characters of the UID.
            </p>
            <ErrorBanner message={recent.error} />
          </>
        )}
        <ErrorBanner message={error} />
      </form>
    </Modal>
  )
}

type State = {
  activity: string
  wort_c: number | null
  thermoblock_c: number | null
  ambient_c: number | null
  drain_c: number | null
  target_c: number | null
  step_name?: string
  elapsed_seconds: number
  remaining_seconds: number
  keg_pump_on: boolean
  drain_pump_on: boolean
  pause_reason: number
  location: number
  valve_position: number
  rssi: number
  picopak_mode: boolean
}
// PauseReason ordinals from the original Z controller.
const stockZPauses = [
  'None',
  'Program pause',
  'User pause',
  'Step filter',
  'Temperature too high',
  'Temperature too low',
  'Thermoblock and wort temperature difference',
  'Wort and drain temperature difference',
  'Thermoblock temperature rise',
  'Temperature input',
  'Fine time input',
  'Coarse time input',
  'PicoStill error',
  'Still pump off',
  'System error',
  'MSP controller error',
  'Cannot run',
  'Still T2 overtemperature',
]

function stockZValveDetail(data: State): string | undefined {
  // Home and rest have no flow route. At the shared ports two mechanical
  // positions can report the same route, depending on the PicoPak mode.
  if (data.location === 0) {
    if (data.valve_position === 1) return 'Home (1)'
    if (data.valve_position === 3) return 'Rest (3)'
    if (data.valve_position !== 0) return String(data.valve_position)
  }
  if ((data.picopak_mode && data.location === 3) || (!data.picopak_mode && data.location === 4))
    return String(data.valve_position)
}

function StockZReadings({ machine, onClose }: { machine: StockZ; onClose: () => void }) {
  const { unit } = useHub()
  const { data, error, loading, reload } = useResource<State>(
    `/stock-z-programs/${machine.id}/state`,
    15000
  )
  const temp = (c: number | null) => temperature(c == null ? null : (c * 9) / 5 + 32, unit)
  const valveDetail = data && stockZValveDetail(data)
  const activity: Record<string, string> = {
    active: 'Pump active',
    paused: 'Paused',
    outputs_quiet: 'Outputs quiet',
    unknown: 'Unknown',
  }
  return (
    <Modal
      title={`${machine.name} readings`}
      onClose={onClose}
      actions={
        <Button disabled={loading} onClick={reload}>
          <RefreshCw size={15} />
          Refresh readings
        </Button>
      }
    >
      <ErrorBanner message={error} />
      {loading ? (
        <p role="status">Reading the brewer…</p>
      ) : (
        data &&
        !error && (
          <dl className="stock-brewer-readings">
            {Object.entries({
              Activity: activity[data.activity] || 'Unknown',
              Step: data.step_name || '—',
              Wort: temp(data.wort_c),
              Thermoblock: temp(data.thermoblock_c),
              Ambient: temp(data.ambient_c),
              Drain: temp(data.drain_c),
              Target: data.target_c === 0 ? 'Unheated' : temp(data.target_c),
              Elapsed: duration(data.elapsed_seconds * 1000),
              Remaining: duration(data.remaining_seconds * 1000),
              'Keg pump': data.keg_pump_on ? 'On' : 'Off',
              'Drain pump': data.drain_pump_on ? 'On' : 'Off',
              'Pause reason': stockZPauses[data.pause_reason] ?? `Unknown (${data.pause_reason})`,
              Location: routeLabel(data.location, 'z'),
              ...(valveDetail && { 'Valve position': valveDetail }),
              'Wi-Fi signal': `${data.rssi} dBm`,
              'PicoPak mode': data.picopak_mode ? 'On' : 'Off',
            }).map(([key, value]) => (
              <div key={key}>
                <dt>{key}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        )
      )}
    </Modal>
  )
}

export function StockBrewerFleet({ fleet }: { fleet: ReturnType<typeof useStockBrewerFleet> }) {
  const hub = useHub()
  const [transfer, setTransfer] = useState<StockTarget | null>(null)
  const [readings, setReadings] = useState<StockZ | null>(null)
  const [removal, setRemoval] = useState<{
    path: string
    title: string
    description: string
    label: string
  } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  if (!fleet.picos.length && !fleet.zs.length && !fleet.error) return null
  async function remove() {
    if (!removal || busy) return
    setBusy(true)
    setError('')
    try {
      await api(removal.path, { method: 'DELETE' })
      window.dispatchEvent(new Event(changeEvent))
      setRemoval(null)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const rows = [
    ...fleet.picos.map((p) => ({
      id: 'stock-pico:' + p.uid,
      name: p.name,
      model: p.model,
      path: '/stock-picos/' + p.uid,
      items: p.items,
      uid: p.uid,
      report: p.report,
      z: null as StockZ | null,
    })),
    ...fleet.zs.map((z) => ({
      id: 'stock-z:' + z.id,
      name: z.name,
      model: 'z-series',
      path: '/stock-z-programs/' + z.id,
      items: [] as MenuItem[],
      uid: '',
      report: undefined,
      z,
    })),
  ]
  return (
    <section aria-label="Stock brewers" className="brewery-list">
      <SectionHeading title="Stock brewers" />
      <ErrorBanner message={fleet.error} />
      {rows.map((row) => (
        <article className="panel brewery-row" key={row.id}>
          <div className="brewery-session">
            <small>{modelLabel(row.model)} · Factory firmware</small>
            <h3>{row.name}</h3>
            <span>{row.z?.base_url || row.uid}</span>
          </div>
          <div className="brewery-session">
            {row.z ? (
              <span>Start the loaded program at the dial.</span>
            ) : (
              <details>
                <summary>Recipe menu ({row.items.length})</summary>
                {row.items.length ? (
                  <ul>
                    {row.items.map((item) => (
                      <li key={item.recipe_id}>
                        <strong>{item.program.name}</strong>{' '}
                        <small>
                          Revision {item.revision}
                          {item.rfid && ` · Tag ${item.rfid}`}
                        </small>{' '}
                        <IconButton
                          label={`Remove ${item.program.name} from ${row.name} menu`}
                          onClick={() => {
                            setError('')
                            setRemoval({
                              path: `${row.path}/recipes/${encodeURIComponent(item.recipe_id)}`,
                              title: `Remove ${item.program.name}?`,
                              description: `Remove this recipe from ${row.name}’s published menu.`,
                              label: 'Remove from menu',
                            })
                          }}
                        >
                          <Trash2 size={14} />
                        </IconButton>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p>No published recipes.</p>
                )}
              </details>
            )}
          </div>
          <div className="brewery-reading">
            {row.report?.last_state != null && row.report.state_at && (
              <>
                <small>Latest reported state</small>
                <span>
                  {row.report.last_state} · {dateLabel(row.report.state_at)}
                </span>
              </>
            )}
            {row.report?.last_error?.error_code != null && row.report.error_at && (
              <>
                <small>Latest reported error</small>
                <span>
                  Code {row.report.last_error.error_code} · {dateLabel(row.report.error_at)}
                </span>
              </>
            )}
            {row.z && (
              <Button variant="ghost" onClick={() => setReadings(row.z)}>
                View readings
              </Button>
            )}
          </div>
          <div className="brewery-actions">
            <Button
              variant="secondary"
              onClick={() =>
                setTransfer({
                  id: row.id,
                  model: row.model,
                  name: row.name,
                  base_url: row.z?.base_url,
                })
              }
            >
              {row.z ? 'Load program' : 'Send recipe'}
            </Button>
            <IconButton
              label={`Remove ${row.name}`}
              onClick={() => {
                setError('')
                setRemoval({
                  path: row.path,
                  title: `Remove ${row.name}?`,
                  description: `Remove this stock brewer from the Hub. Recorded sessions remain in the brew journal.`,
                  label: 'Remove machine',
                })
              }}
            >
              <Trash2 size={16} />
            </IconButton>
          </div>
        </article>
      ))}
      {transfer && (
        <StockRecipeTransfer
          target={transfer}
          recipes={hub.recipes}
          onClose={() => setTransfer(null)}
        />
      )}
      {readings && <StockZReadings machine={readings} onClose={() => setReadings(null)} />}
      {removal && (
        <Confirm
          title={removal.title}
          description={removal.description}
          busy={busy}
          danger
          label={removal.label}
          onClose={() => {
            if (!busy) setRemoval(null)
          }}
          onConfirm={() => void remove()}
        >
          <ErrorBanner message={error} />
        </Confirm>
      )}
    </section>
  )
}
