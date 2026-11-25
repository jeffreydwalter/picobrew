import { useEffect, useRef, useState } from 'react'
import { RefreshCw, Usb } from 'lucide-react'
import { useHub } from '../context'
import type { MachineRecipe } from '../types'
import { api, errorMessage } from '../lib/api'
import { Button, ErrorBanner, Field, Loading, Modal } from './ui'
import { usbLabel, usbModels, type USBAction } from './USBInstructions'

interface Port {
  port: string
  label: string
  models: string[]
}
interface Job {
  id: string
  action: USBAction
  model: string
  port: string
  state: 'running' | 'waiting' | 'complete' | 'failed' | 'canceled'
  message: string
  confirmation?: string
  device_id?: string
  pairing_ready?: boolean
  offline_program?: { id: string }
}
interface Discovery {
  available: boolean
  message?: string
  devices: Port[]
  job?: Job
}
const actions: Record<USBAction, string> = {
  install: 'Install standalone firmware',
  backup: 'Back up original firmware',
  restore: 'Restore original firmware',
}
const active = (job: Job) => job.state === 'running' || job.state === 'waiting'

export function StockSetupGuide({
  onClose,
  model: restoreModel,
  program,
  display: initialDisplay,
  deviceID,
}: {
  onClose: () => void
  model?: string
  program?: MachineRecipe
  display?: string
  deviceID?: string
}) {
  const hub = useHub()
  const lockedModel = program ? 'zymatic' : restoreModel
  const [file, setFile] = useState<File>()
  const [discovery, setDiscovery] = useState<Discovery>()
  const [refresh, setRefresh] = useState(0)
  const [reading, setReading] = useState(true)
  const [port, setPort] = useState('')
  const [model, setModel] = useState(lockedModel || '')
  const [display, setDisplay] = useState(initialDisplay || '')
  const [action, setAction] = useState<USBAction>(restoreModel ? 'restore' : 'install')
  const [job, setJob] = useState<Job>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dismissed = useRef('')
  useEffect(() => {
    const controller = new AbortController()
    setReading(true)
    setFile(undefined)
    setError('')
    api<Discovery>('/usb/devices', { signal: controller.signal })
      .then((result) => {
        if (controller.signal.aborted) return
        setDiscovery(result)
        const discovered = (result.devices || []).filter(
          (d) => !lockedModel || d.models.includes(lockedModel)
        )
        setPort((previous) =>
          discovered.some((d) => d.port === previous)
            ? previous
            : discovered.length === 1
              ? discovered[0].port
              : ''
        )
        if (
          result.job &&
          result.job.id !== dismissed.current &&
          (active(result.job) || (!program && (!deviceID || result.job.device_id === deviceID)))
        )
          setJob(result.job)
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(errorMessage(e))
      })
      .finally(() => {
        if (!controller.signal.aborted) setReading(false)
      })
    return () => controller.abort()
  }, [refresh, lockedModel, deviceID, program])
  useEffect(() => {
    if (!job || !active(job) || busy) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const next = await api<Job>('/usb/jobs/' + job.id, { signal: controller.signal })
        if (!controller.signal.aborted) {
          if (deviceID && next.device_id !== deviceID && !active(next)) {
            setJob(undefined)
            setRefresh((n) => n + 1)
          } else {
            setJob(next)
          }
          setError('')
        }
      } catch (e) {
        if (!controller.signal.aborted) setError(errorMessage(e))
      }
      if (!controller.signal.aborted) timer = setTimeout(poll, 1200)
    }
    timer = setTimeout(poll, 1000)
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [job?.id, job?.state, busy, deviceID])
  const ports = (discovery?.devices || []).filter(
    (d) => !lockedModel || d.models.includes(lockedModel)
  )
  const selected = ports.find((d) => d.port === port)
  const choices = usbModels.filter((m) => selected?.models.includes(m))
  const selectedModel =
    lockedModel || (choices.length === 1 ? choices[0] : choices.includes(model) ? model : '')
  async function change(path: string, body: unknown) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const data = typeof body === 'function' ? await body() : body
      setJob(await api<Job>(path, { method: 'POST', body: data }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  async function installationInput() {
    let offlineProgram: unknown
    if (selectedModel === 'zymatic' && action === 'install') {
      if (program) offlineProgram = { ...program, model: 'zymatic' }
      else if (file) {
        if (file.size > 65536) throw new Error('Choose a Zymatic program JSON file under 64 KiB.')
        try {
          offlineProgram = JSON.parse(await file.text())
        } catch {
          throw new Error('Choose a readable Zymatic offline program JSON file.')
        }
        if (
          !offlineProgram ||
          typeof offlineProgram !== 'object' ||
          !('model' in offlineProgram) ||
          offlineProgram.model !== 'zymatic'
        )
          throw new Error('Choose an offline program exported for the Zymatic.')
      }
    }
    return {
      action,
      model: selectedModel,
      port,
      ...(selectedModel === 'pico-c' ? { display } : {}),
      ...(deviceID ? { device_id: deviceID } : {}),
      ...(offlineProgram ? { offline_program: offlineProgram } : {}),
    }
  }
  const start = () => change('/usb/jobs', installationInput)
  const anotherDevice = Boolean(
    job &&
    ((deviceID && job.device_id !== deviceID) ||
      (program &&
        (job.action !== 'install' ||
          job.model !== 'zymatic' ||
          job.offline_program?.id !== program.id)))
  )
  const paired =
    !anotherDevice && job?.state === 'complete' && job.action === 'install' && job.pairing_ready
  return (
    <Modal
      title={restoreModel ? 'Restore original firmware' : 'USB setup and recovery'}
      onClose={onClose}
      actions={
        <Button variant="secondary" onClick={onClose}>
          {program
            ? 'Back to recipe preview'
            : restoreModel
              ? 'Back to machine firmware'
              : 'Back to Add machine'}
        </Button>
      }
    >
      <ErrorBanner
        message={error}
        retry={job && active(job) ? undefined : () => setRefresh((n) => n + 1)}
      />
      {job ? (
        <>
          <h3>
            {actions[job.action]} · {usbLabel(job.model)}
          </h3>
          <p role="status" aria-live="polite">
            {anotherDevice
              ? 'Another USB operation is in progress. Open USB setup and recovery from Add machine to continue it.'
              : job.message}
          </p>
          {!anotherDevice && job.state === 'waiting' && job.confirmation && (
            <div className="actions">
              <Button
                disabled={busy}
                onClick={() =>
                  change('/usb/jobs/' + job.id + '/confirm', { confirm: job.confirmation })
                }
              >
                Continue
              </Button>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() =>
                  change('/usb/jobs/' + job.id + '/cancel', { confirm: job.confirmation })
                }
              >
                Cancel operation
              </Button>
            </div>
          )}
          {paired && (
            <Button
              onClick={() =>
                hub.navigate('/overview/new-device?usb_job_id=' + encodeURIComponent(job.id))
              }
            >
              Add machine
            </Button>
          )}
          {!active(job) && (
            <Button
              variant="secondary"
              onClick={() => {
                dismissed.current = job.id
                setJob(undefined)
                setRefresh((n) => n + 1)
              }}
            >
              New USB operation
            </Button>
          )}
        </>
      ) : reading ? (
        <Loading label="Checking the Hub’s USB ports…" />
      ) : discovery?.available ? (
        <>
          <p>Connect the idle machine to the computer running the Hub.</p>
          {ports.length ? (
            <Field label="USB connection">
              <select
                value={port}
                onChange={(e) => {
                  setPort(e.target.value)
                  setModel(lockedModel || '')
                  setFile(undefined)
                }}
              >
                <option value="">Choose a connection</option>
                {ports.map((d) => (
                  <option key={d.port} value={d.port}>
                    {d.label}
                  </option>
                ))}
              </select>
            </Field>
          ) : (
            <p role="status">No matching USB adapter detected.</p>
          )}
          <Button variant="ghost" onClick={() => setRefresh((n) => n + 1)}>
            <RefreshCw size={15} /> Refresh USB devices
          </Button>
          {selected && (
            <>
              {lockedModel || choices.length === 1 ? (
                <p>
                  <strong>{usbLabel(selectedModel)}</strong>
                </p>
              ) : (
                <Field label="Machine model">
                  <select
                    value={selectedModel}
                    onChange={(e) => {
                      setModel(e.target.value)
                      setDisplay('')
                      setFile(undefined)
                    }}
                  >
                    <option value="">Choose your machine</option>
                    {choices.map((m) => (
                      <option key={m} value={m}>
                        {usbLabel(m)}
                      </option>
                    ))}
                  </select>
                </Field>
              )}
              {selectedModel === 'pico-c' && (
                <Field label="Physical Pico C display controller">
                  <select value={display} onChange={(e) => setDisplay(e.target.value)}>
                    <option value="">Choose the installed display</option>
                    <option value="ssd1309">SSD1309</option>
                    <option value="ssd1325">SSD1325</option>
                  </select>
                </Field>
              )}
              {!lockedModel && selectedModel && (
                <Field label="USB operation">
                  <select
                    value={action}
                    onChange={(e) => {
                      setAction(e.target.value as USBAction)
                      setFile(undefined)
                    }}
                  >
                    {Object.entries(actions).map(([key, label]) => (
                      <option value={key} key={key}>
                        {label}
                      </option>
                    ))}
                  </select>
                </Field>
              )}
              {selectedModel === 'zymatic' &&
                action === 'install' &&
                (program ? (
                  <p>
                    Offline recipe: <strong>{program.name}</strong>
                  </p>
                ) : (
                  <Field
                    label="Offline recipe (optional)"
                    hint="Choose a Zymatic program exported from Recipes. Leave empty to keep the installed recipe or use American Brown Ale on first setup."
                  >
                    <input
                      type="file"
                      accept=".json,application/json"
                      onChange={(e) => setFile(e.target.files?.[0])}
                    />
                  </Field>
                ))}
              {selectedModel && (
                <p className="help-text">
                  {action === 'restore'
                    ? 'Returning to stock removes standalone Hub controls. Converting this machine again requires USB.'
                    : action === 'backup'
                      ? 'The Hub compares two reads and keeps your machine’s verified backup locally.'
                      : 'The Hub verifies the original firmware before writing. Keep the machine connected through readback.'}
                </p>
              )}
              {selectedModel && (
                <Button
                  variant={action === 'restore' ? 'danger' : 'primary'}
                  disabled={busy || (selectedModel === 'pico-c' && !display)}
                  onClick={start}
                >
                  <Usb size={16} />
                  {actions[action]}
                </Button>
              )}
            </>
          )}
        </>
      ) : (
        discovery && (
          <p role="status">
            {discovery.message || 'This Hub build does not include USB setup for this computer.'}
          </p>
        )
      )}
    </Modal>
  )
}
