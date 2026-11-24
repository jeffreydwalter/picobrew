import { useState } from 'react'
import { useHub, useResource } from '../context'
import type { Device } from '../types'
import { api, APIError, errorMessage } from '../lib/api'
import { dateLabel } from '../lib/utils'
import { Button, ErrorBanner, Field, Loading, Modal } from './ui'

interface SessionChoice {
  id: string
  session_number?: string
  observed_at: string
  started_at?: string
}

function sessionLabel(session: SessionChoice) {
  const time = session.started_at
    ? 'Started ' + dateLabel(session.started_at)
    : 'First seen ' + dateLabel(session.observed_at)
  // Older imports without a native session number can still be distinguished.
  const reference = session.session_number
    ? 'Session ' + session.session_number
    : 'Hub record ' + session.id
  return reference + ' · ' + time
}

interface FermentChoices {
  picoferms: { id: string; name: string; brewer_id: string; revision: number }[]
  brews: (SessionChoice & {
    recipe_name: string
    picoferm_id?: string
    picoferm_recording_id?: string
  })[]
  recordings: (SessionChoice & { device_id: string; linked_brew_id?: string })[]
}

function monitorOptions(monitors: FermentChoices['picoferms'], devices: Device[]) {
  const options = monitors.map((monitor) => {
    let label = monitor.name
    if (monitors.some((other) => other.id !== monitor.id && other.name === monitor.name)) {
      const device = devices.find((d) => d.id === monitor.id)
      const address = device?.base_url.replace(/^https?:\/\//i, '').replace(/\/$/, '')
      label = [label, device?.location, address].filter(Boolean).join(' · ')
    }
    return { ...monitor, label }
  })
  return options.map((option) =>
    options.some((other) => other.id !== option.id && other.label === option.label)
      ? { ...option, label: option.label + ' · ' + option.id }
      : option
  )
}

export function FermentDialog({
  brewerId,
  brewId,
  onClose,
  onSaved,
}: {
  brewerId: string
  brewId?: string
  onClose: () => void
  onSaved?: () => void
}) {
  const hub = useHub()
  const path = '/devices/' + encodeURIComponent(brewerId) + '/picoferms'
  const choices = useResource<FermentChoices>(path)
  const [selectedBrew, setBrew] = useState(brewId || '')
  const [sensorId, setSensor] = useState('')
  const [recordingId, setRecording] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const brewer = hub.devices.find((d) => d.id === brewerId)
  const batch = choices.data?.brews.find((b) => b.id === selectedBrew)
  const sensor = choices.data?.picoferms.find((f) => f.id === (batch?.picoferm_id || sensorId))
  const recordings =
    choices.data?.recordings.filter(
      (r) => r.device_id === sensor?.id && (!r.linked_brew_id || r.linked_brew_id === selectedBrew)
    ) || []
  const previousBrewer =
    !batch?.picoferm_id && sensor?.brewer_id && sensor.brewer_id !== brewerId
      ? hub.devices.find((d) => d.id === sensor.brewer_id)?.name || 'another brewer'
      : ''
  const attached = sensor?.brewer_id === brewerId
  const unlink = !selectedBrew && attached
  const canSave =
    sensor &&
    (!selectedBrew ||
      (batch && (!batch.picoferm_id || (recordingId && !batch.picoferm_recording_id))))
  async function save() {
    if (pending || !sensor || !canSave) return
    setPending(true)
    setError('')
    try {
      await api(path, {
        method: 'PUT',
        body: {
          picoferm_id: sensor.id,
          revision: sensor.revision,
          ...(unlink && { unlink: true }),
          ...(selectedBrew && { brew_id: selectedBrew }),
          ...(selectedBrew && recordingId && { recording_id: recordingId }),
        },
      })
    } catch (err) {
      setError(errorMessage(err))
      if (err instanceof APIError && err.status === 409) {
        setSensor('')
        setRecording('')
        choices.reload()
      }
      setPending(false)
      return
    }
    hub.notify(
      unlink
        ? 'PicoFerm unlinked from this brewer.'
        : recordingId
          ? 'Fermentation recording attached.'
          : batch
            ? 'PicoFerm assigned to this batch.'
            : 'PicoFerm linked to brewer.'
    )
    onSaved?.()
    onClose()
    void hub.refresh().catch((err) => hub.notify(errorMessage(err), 'error'))
  }
  return (
    <Modal
      title="Ferment"
      onClose={() => {
        if (!pending) onClose()
      }}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            Cancel
          </Button>
          <Button
            disabled={pending || choices.loading || !!choices.error || !canSave}
            onClick={() => void save()}
          >
            {pending
              ? 'Saving…'
              : unlink
                ? 'Unlink from brewer'
                : previousBrewer
                  ? 'Move PicoFerm here'
                  : batch?.picoferm_id
                    ? 'Attach recording'
                    : selectedBrew
                      ? 'Assign to batch'
                      : 'Link to brewer'}
          </Button>
        </>
      }
    >
      <ErrorBanner
        message={error || choices.error}
        retry={choices.error ? choices.reload : undefined}
      />
      {choices.loading ? (
        <Loading label="Looking up PicoFerms…" />
      ) : (
        choices.data &&
        (!choices.data.picoferms.length ? (
          <p>Add your PicoFerm to the Hub to use it with {brewer?.name || 'this brewer'}.</p>
        ) : (
          <>
            {brewId ? (
              <p>{batch?.recipe_name || (batch ? 'Manual brew' : 'This batch is unavailable.')}</p>
            ) : (
              <Field label="Batch">
                <select
                  value={selectedBrew}
                  disabled={pending}
                  onChange={(e) => {
                    setBrew(e.target.value)
                    setSensor('')
                    setRecording('')
                  }}
                >
                  <option value="">Brewer only</option>
                  {choices.data.brews.map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.recipe_name || 'Manual brew'} · {sessionLabel(b)}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            <Field label="PicoFerm">
              <select
                disabled={pending || !!batch?.picoferm_id}
                value={sensor?.id || ''}
                onChange={(e) => {
                  setSensor(e.target.value)
                  setRecording('')
                }}
              >
                <option value="">Choose a PicoFerm</option>
                {monitorOptions(choices.data.picoferms, hub.devices).map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.label}
                    {f.brewer_id === brewerId ? ' · Linked here' : ''}
                  </option>
                ))}
              </select>
            </Field>
            {unlink && (
              <p className="help-text">
                Unlinking this PicoFerm keeps its batches and recordings in the brew journal.
              </p>
            )}
            {previousBrewer && (
              <p className="notice">
                This moves {sensor?.name} from {previousBrewer} to {brewer?.name || 'this brewer'}.
                Earlier batches keep their recordings.
              </p>
            )}
            {batch && sensor && !!recordings.length && (
              <Field label="Fermentation recording">
                <select
                  disabled={pending || !!batch.picoferm_recording_id}
                  value={batch.picoferm_recording_id || recordingId}
                  onChange={(e) => setRecording(e.target.value)}
                >
                  <option value="">Choose later</option>
                  {recordings.map((r) => (
                    <option key={r.id} value={r.id}>
                      {sessionLabel(r)}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            {selectedBrew && (
              <p className="help-text">
                Start recording on the PicoFerm when it’s on the keg. You can attach its recording
                to this batch here.
              </p>
            )}
          </>
        ))
      )}
    </Modal>
  )
}
