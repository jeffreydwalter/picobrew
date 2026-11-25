import { FermentDialog } from '../components/FermentDialog'
import { MachineCare } from '../components/MachineCare'
import { ConditioningJournal } from '../components/ConditioningJournal'
import { sessionState } from '../lib/sessionState'
import { useEffect, useMemo, useState } from 'react'
import {
  ArrowUpRight,
  Download,
  History,
  Star,
  Save,
  MessageSquarePlus,
  Filter,
  Activity,
  Clock3,
  FlaskConical,
  Sparkles,
  RefreshCw,
} from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, download, errorMessage } from '../lib/api'
import type { BrewDetail } from '../types'
import {
  modelLabel,
  dateLabel,
  duration,
  clockTime,
  slug,
  temperature,
  transitions,
} from '../lib/utils'
import {
  Badge,
  Button,
  Empty,
  ErrorBanner,
  Field,
  Loading,
  Metric,
  PageTitle,
  SearchBox,
  SectionHeading,
} from '../components/ui'
import { SessionChart } from '../components/SessionChart'
import { HistorySamples } from '../components/HistorySamples'
import { AIWorkbench } from './Assistant'
export default function HistoryPage({ id, analyze = false }: { id?: string; analyze?: boolean }) {
  return id ? <BrewPage key={id} id={id} analyze={analyze} /> : <BrewList />
}
function BrewList() {
  const hub = useHub()
  const [query, setQuery] = useState('')
  const [device, setDevice] = useState('')
  const [state, setState] = useState('')
  const items = hub.brews.filter(
    (brew) =>
      (!device || brew.device_id === device) &&
      (!state || brew.state === state) &&
      `${brew.recipe_name} ${brew.device_name} ${brew.notes}`
        .toLowerCase()
        .includes(query.toLowerCase())
  )
  return (
    <>
      <PageTitle
        eyebrow="EVERY BREW TELLS A STORY"
        title="The session journal."
        description="Live traces, completed sessions and the notes that make your next batch better."
        actions={
          <Button variant="secondary" onClick={() => void hub.refresh()}>
            <RefreshCw size={16} />
            Refresh history
          </Button>
        }
      />
      <div className="list-toolbar">
        <SearchBox value={query} onChange={setQuery} placeholder="Search sessions or notes" />
        <div className="filter-controls">
          <select
            aria-label="Filter by machine"
            value={device}
            onChange={(event) => setDevice(event.target.value)}
          >
            <option value="">All machines</option>
            {hub.devices.map((device) => (
              <option key={device.id} value={device.id}>
                {device.name}
              </option>
            ))}
          </select>
          <select
            aria-label="Filter by brew state"
            value={state}
            onChange={(event) => setState(event.target.value)}
          >
            <option value="">All sessions</option>
            {Array.from(new Set(hub.brews.map((brew) => brew.state))).map((state) => (
              <option key={state} value={state}>
                {state}
              </option>
            ))}
          </select>
        </div>
      </div>
      {items.length ? (
        <div className="panel history-list">
          <div className="table-scroll">
            <table className="data-table brew-table">
              <thead>
                <tr>
                  <th>Session</th>
                  <th>Machine</th>
                  <th>Started</th>
                  <th>Duration</th>
                  <th>Samples</th>
                  <th>State</th>
                  <th>Rating</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {items.map((brew) => (
                  <tr key={brew.id}>
                    <td>
                      <button
                        className="table-link"
                        onClick={() => hub.navigate(`/history/${brew.id}`)}
                      >
                        <span className="brew-icon">
                          <FlaskConical size={17} />
                        </span>
                        <strong>{brew.recipe_name || brew.command || 'Brew session'}</strong>
                      </button>
                    </td>
                    <td>{brew.device_name}</td>
                    <td>{dateLabel(brew.started_at || brew.observed_at)}</td>
                    <td>{duration(brew.duration_ms)}</td>
                    <td>{brew.sample_count.toLocaleString()}</td>
                    <td>
                      <Badge
                        tone={
                          brew.state === 'completed'
                            ? 'green'
                            : brew.state === 'active' || brew.state === 'running'
                              ? 'copper'
                              : 'neutral'
                        }
                      >
                        {brew.state}
                      </Badge>
                    </td>
                    <td>
                      <span className="rating-small">
                        {brew.rating ? (
                          <>
                            <Star size={13} fill="currentColor" />
                            {brew.rating}
                          </>
                        ) : (
                          '—'
                        )}
                      </span>
                    </td>
                    <td>
                      <button
                        className="icon-button"
                        aria-label={`Open ${brew.recipe_name || brew.command} history`}
                        onClick={() => hub.navigate(`/history/${brew.id}`)}
                      >
                        <ArrowUpRight size={17} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="panel">
          <Empty
            icon={<History size={26} />}
            title={
              query || device || state
                ? 'No brews match these filters.'
                : 'Your brewing story starts here.'
            }
            description={
              query || device || state
                ? 'Adjust your search to see more sessions.'
                : 'The hub records connected machines and imports their retained brew history automatically.'
            }
          />
        </div>
      )}
    </>
  )
}
function BrewPage({ id, analyze }: { id: string; analyze: boolean }) {
  const hub = useHub()
  const resource = useResource<BrewDetail>(`/brews/${id}`, 10000)
  const brew = resource.data
  const [notes, setNotes] = useState('')
  const [rating, setRating] = useState(0)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [marker, setMarker] = useState<number | null>(null)
  const [showSamples, setShowSamples] = useState(false)
  const [samplePage, setSamplePage] = useState(0)
  const [ferment, setFerment] = useState(false)
  const [analysisOpen, setAnalysisOpen] = useState(analyze)
  useEffect(() => {
    if (analyze) setAnalysisOpen(true)
  }, [analyze])
  useEffect(() => {
    if (brew && !dirty) {
      setNotes(brew.notes || '')
      setRating(brew.rating || 0)
    }
  }, [brew, dirty])
  const events = useMemo(() => transitions(brew?.samples || []), [brew?.samples])
  async function save() {
    setBusy(true)
    setError('')
    try {
      await api(`/brews/${id}`, { method: 'PATCH', body: { notes, rating } })
      resource.setData((current) => (current ? { ...current, notes, rating } : current))
      setDirty(false)
      resource.reload()
      await hub.refresh()
      hub.notify('Brew notes saved.')
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  async function exportBrew(format: string) {
    try {
      await download(
        `/brews/${id}/export?format=${format}`,
        `${slug(brew?.recipe_name || 'brew')}-${id.slice(0, 8)}.${format}`
      )
    } catch (error) {
      hub.notify(errorMessage(error), 'error')
    }
  }
  if (resource.loading && !brew) return <Loading label="Opening the brew journal…" />
  if (!brew) return <ErrorBanner message={resource.error} retry={resource.reload} />
  const accessory = ['picoferm', 'picostill'].includes(brew.model)
  const canFerment =
    brew.source === 'device' &&
    ['brew', 'manual'].includes(brew.command) &&
    hub.devices.some(
      (d) =>
        d.id === brew.device_id &&
        d.adapter === 'pico-standalone' &&
        ['pico-s', 'pico-pro', 'pico-c', 'z-series', 'zymatic'].includes(d.model) &&
        !d.model_error
    )
  const samples = brew.samples || []
  const last = samples.at(-1)
  return (
    <>
      <PageTitle
        back={() => hub.navigate('/history')}
        eyebrow={`${brew.device_name} · ${dateLabel(brew.started_at || brew.observed_at)}`}
        title={brew.recipe_name || brew.command || 'Brew session'}
        description={`${modelLabel(brew.model)} · ${brew.source === 'device' ? 'Machine history' : brew.source === 'simulation' ? 'Simulation' : brew.source}${accessory ? '' : ` · ${brew.recipe_revision ? `Recipe revision ${brew.recipe_revision}` : 'Recipe revision not recorded'}`}`}
        actions={
          <>
            {canFerment && (
              <Button variant="secondary" onClick={() => setFerment(true)}>
                Ferment
              </Button>
            )}
            <Badge tone={brew.state === 'completed' ? 'green' : 'copper'}>{brew.state}</Badge>
            <Button variant="secondary" onClick={() => void exportBrew('csv')}>
              <Download size={16} />
              CSV
            </Button>
            <Button variant="secondary" onClick={() => void exportBrew('json')}>
              <Download size={16} />
              JSON
            </Button>
          </>
        }
      />
      <ErrorBanner message={error || resource.error} />
      {ferment && (
        <FermentDialog
          brewerId={brew.device_id}
          brewId={brew.id}
          onClose={() => setFerment(false)}
          onSaved={resource.reload}
        />
      )}
      {brew.group_session_id && (
        <section className="panel">
          <SectionHeading title={brew.group_name || 'Group brew'} />
          <div className="brew-group-comparison">
            {hub.brews.filter(item => item.group_session_id === brew.group_session_id).map(item => (
              <button key={item.id} className="button secondary" aria-current={item.id === brew.id ? 'page' : undefined}
                onClick={() => hub.navigate('/history/' + encodeURIComponent(item.id))}>
                {item.device_name} · {duration(item.duration_ms)} · {item.state}
              </button>
            ))}
          </div>
        </section>
      )}
      {brew.picoferm_id && (
        <div className="inline-actions">
          <span>PicoFerm · {brew.picoferm_name || 'Assigned monitor'}</span>
          {brew.picoferm_recording_id && (
            <Button
              variant="ghost"
              onClick={() =>
                hub.navigate('/history/' + encodeURIComponent(brew.picoferm_recording_id!))
              }
            >
              View fermentation recording <ArrowUpRight size={15} />
            </Button>
          )}
        </div>
      )}
      {(brew.gap_count > 0 || brew.truncated) && (
        <div className="notice">
          {brew.gap_count > 0 ? `${brew.gap_count} gaps were reported in this session. ` : ''}
          {brew.truncated ? 'The retained machine history was truncated. ' : ''}The chart shows the
          samples available to the hub.
        </div>
      )}
      <div className="machine-metrics">
        <Metric
          label="Session duration"
          value={duration(brew.duration_ms)}
          note="Recorded session"
        />
        <Metric
          label="Sensor samples"
          value={samples.length.toLocaleString()}
          note={`${events.length} phase or state transitions`}
        />
        <Metric
          label={accessory ? 'Last temperature reading' : 'Last wort reading'}
          value={temperature(accessory ? last?.temperature_f : last?.wort_f, hub.unit)}
          note="At the end of the available trace"
          accent
        />
        <Metric
          label="Your rating"
          value={
            rating ? (
              <>
                {rating}
                <small> / 5</small>
              </>
            ) : (
              'Unrated'
            )
          }
          note="A little hindsight helps"
        />
      </div>
      <section className="panel session-panel">
        <SectionHeading
          title="Session measurements"
          description="Click or tap to pin a sample and add a note. Use the arrow keys to inspect adjacent samples."
          actions={
            marker !== null && (
              <Button
                variant="secondary"
                onClick={() => {
                  setNotes((value) => `${value}${value ? '\n' : ''}[${clockTime(marker)}] `)
                  setDirty(true)
                  document.getElementById('brew-notes')?.focus()
                }}
              >
                <MessageSquarePlus size={15} />
                Note at {clockTime(marker)}
              </Button>
            )
          }
        />
        {samples.length ? (
          <SessionChart brew={brew} unit={hub.unit} notes={notes} onSelect={setMarker} />
        ) : (
          <Empty
            icon={<Activity size={24} />}
            title="No sensor samples are available."
            description="This session may have been discovered before a complete history could be imported."
          />
        )}
      </section>
      {brew.state === 'completed' && (accessory || ['brew', 'manual'].includes(brew.command)) && (
        <MachineCare model={brew.model} />
      )}
      {brew.command === 'brew' && (
        <ConditioningJournal
          brew={brew}
          update={(conditioning) =>
            resource.setData((current) => (current ? { ...current, conditioning } : current))
          }
          reload={resource.reload}
        />
      )}
      <div className="journal-bottom">
        <section className="panel">
          <SectionHeading
            title="What did you notice?"
            description="Capture brew-day observations, tasting notes and what you would change."
            actions={
              <Button disabled={!dirty || busy} onClick={() => void save()}>
                <Save size={15} />
                {busy ? 'Saving…' : 'Save notes'}
              </Button>
            }
          />
          <div className="rating-control">
            <span>Rate this brew</span>
            {[1, 2, 3, 4, 5].map((value) => (
              <button
                aria-label={`Rate ${value} out of 5`}
                aria-pressed={rating === value}
                key={value}
                onClick={() => {
                  setRating(rating === value ? 0 : value)
                  setDirty(true)
                }}
              >
                <Star
                  size={23}
                  fill={value <= rating ? 'currentColor' : 'none'}
                  className={value <= rating ? 'filled' : ''}
                />
              </button>
            ))}
          </div>
          <Field
            label="Brew notes"
            hint="Timestamped annotations use [minutes:seconds] at the beginning of a line, for example [25:30] Stable mash temperature."
          >
            <textarea
              id="brew-notes"
              rows={8}
              value={notes}
              onChange={(event) => {
                setNotes(event.target.value)
                setDirty(true)
              }}
              placeholder="Aroma, clarity, flavor, process changes…"
            />
          </Field>
          <Button variant="ghost" onClick={() => setAnalysisOpen(!analysisOpen)}>
            <Sparkles size={15} />
            {analysisOpen ? 'Hide AI analysis' : 'Analyze this brew with AI'}
          </Button>
        </section>
        <section className="panel">
          <SectionHeading
            title="Session timeline"
            description="Changes observed in the machine's state."
          />
          <div className="timeline">
            {events.slice(0, 30).map((sample, index) => (
              <div key={index}>
                <span className="timeline-dot" />
                <time>{clockTime(sample.elapsed_ms)}</time>
                {sample.zymatic_history ? (
                  <div>
                    <strong>
                      {sample.current_step == null
                        ? 'Step not recorded'
                        : `Step ${sessionState(brew, 'current_step', sample.current_step, sample.source)}`}
                    </strong>
                    <small>
                      {sessionState(brew, 'compartment', sample.compartment, sample.source)}
                    </small>
                    <small>{temperature(sample.wort_f, hub.unit)} wort</small>
                  </div>
                ) : (
                  <div>
                    <strong>
                      {accessory
                        ? `Device state ${sessionState(brew, 'device_state', sample.device_state, sample.source, true)}`
                        : `Phase ${sessionState(brew, 'phase', sample.phase, sample.source)} · Step ${sessionState(brew, 'current_step', sample.current_step, sample.source)}`}
                    </strong>
                    <small>
                      {!accessory &&
                        `Controller ${sessionState(brew, 'controller_status', sample.controller_status, sample.source)} · Compartment ${sessionState(brew, 'compartment', sample.compartment, sample.source)} · `}
                      {sessionState(brew, 'flags', sample.flags, sample.source, true)}
                    </small>
                    <small>
                      {temperature(accessory ? sample.temperature_f : sample.wort_f, hub.unit)}
                      {accessory
                        ? ''
                        : ` wort · Device state ${sessionState(brew, 'device_state', sample.device_state, sample.source, true)}`}
                    </small>
                  </div>
                )}
              </div>
            ))}
            {events.length > 30 && (
              <p className="help-text">
                {events.length - 30} more transitions are included in the raw samples and exports.
              </p>
            )}
            {brew.state === 'interrupted' && (
              <div>
                <span className="timeline-dot" />
                <time>{clockTime(brew.duration_ms)}</time>
                <div>
                  <strong>Session interrupted</strong>
                  <small>
                    {brew.recovered_after_restart
                      ? 'The brewer restarted while the session was open. This is its last saved checkpoint.'
                      : 'The brewer recorded an interruption without a specific cause.'}
                  </small>
                </div>
              </div>
            )}
          </div>
        </section>
      </div>
      {analysisOpen && (
        <AIWorkbench
          key={id}
          mode="analysis"
          brewID={id}
          initialAdvice={brew.ai_advice}
          contextName={brew.recipe_name || brew.command || 'this brew'}
          embedded
        />
      )}
      <section className="panel">
        <SectionHeading
          title="Every recorded reading"
          description="Elapsed time, all available sensor values and machine state."
          actions={
            <Button variant="secondary" onClick={() => setShowSamples(!showSamples)}>
              {showSamples ? 'Hide' : 'Show'} raw samples
            </Button>
          }
        />
        {showSamples && (
          <>
            <HistorySamples brew={brew} page={samplePage} unit={hub.unit} />
            <div className="pagination">
              <Button
                variant="secondary"
                disabled={!samplePage}
                onClick={() => setSamplePage((page) => page - 1)}
              >
                Previous
              </Button>
              <span>
                Page {samplePage + 1} of {Math.max(1, Math.ceil(samples.length / 100))}
              </span>
              <Button
                variant="secondary"
                disabled={(samplePage + 1) * 100 >= samples.length}
                onClick={() => setSamplePage((page) => page + 1)}
              >
                Next
              </Button>
            </div>
          </>
        )}
      </section>
    </>
  )
}
