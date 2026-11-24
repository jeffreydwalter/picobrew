import { useState } from 'react'
import { useHub, useResource } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import { dateLabel } from '../lib/utils'
import { Button, ErrorBanner, Field, Loading, Modal } from './ui'

interface FermentChoices {
  picoferms: { id: string; name: string; brewer_id: string; revision: number }[]
  brews: {
    id: string
    recipe_name: string
    observed_at: string
    picoferm_id?: string
    picoferm_recording_id?: string
  }[]
  recordings: { id: string; device_id: string; observed_at: string; linked_brew_id?: string }[]
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
  const canSave =
    sensor &&
    (selectedBrew
      ? batch && (!batch.picoferm_id || (recordingId && !batch.picoferm_recording_id))
      : !attached)
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
      recordingId
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
                  <option value="">Link to brewer only</option>
                  {choices.data.brews.map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.recipe_name || 'Manual brew'} · {dateLabel(b.observed_at)}
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
                {choices.data.picoferms.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.name}
                    {f.brewer_id === brewerId ? ' · Linked here' : ''}
                  </option>
                ))}
              </select>
            </Field>
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
                      {dateLabel(r.observed_at)} · {sensor.name}
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
