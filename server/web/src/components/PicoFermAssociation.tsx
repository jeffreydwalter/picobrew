import { useState } from 'react'
import { FermentDialog } from './FermentDialog'
import { ArrowUpRight } from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import { Button, ErrorBanner, Field, SectionHeading } from './ui'

type Association = { brewer_id: string; revision: number }
const brewerModels = ['pico-s', 'pico-pro', 'pico-c', 'z-series', 'zymatic']

export function PicoFermAssociation({ id }: { id: string }) {
  const hub = useHub()
  const path = `/devices/${encodeURIComponent(id)}/brewer`
  const link = useResource<Association>(path)
  const [selection, select] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const brewers = hub.devices.filter(
    (d) => d.adapter === 'pico-standalone' && brewerModels.includes(d.model) && !d.model_error
  )
  const current = link.data?.brewer_id || ''
  async function save() {
    if (!link.data || selection === null || pending) return
    setPending(true)
    setError('')
    try {
      link.setData(
        await api<Association>(path, {
          method: 'PUT',
          body: { brewer_id: selection, revision: link.data.revision },
        })
      )
      select(null)
      await hub.refresh()
      hub.notify(selection ? 'PicoFerm linked to brewer.' : 'PicoFerm unlinked.')
    } catch (err) {
      setError(errorMessage(err))
      if (err instanceof APIError && err.status === 409) {
        select(null)
        link.reload()
      }
    } finally {
      setPending(false)
    }
  }
  return (
    <section className="panel">
      <SectionHeading title="Linked brewer" />
      <ErrorBanner message={error || link.error} retry={link.error ? link.reload : undefined} />
      <div className="inline-actions association-controls">
        <Field label="Brewer for this PicoFerm">
          <select
            value={selection ?? current}
            disabled={pending || link.loading || !!link.error || !link.data}
            onChange={(event) => select(event.target.value)}
          >
            <option value="">Unassigned</option>
            {brewers.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </Field>
        <Button
          variant="secondary"
          disabled={
            pending || link.loading || !!link.error || selection === null || selection === current
          }
          onClick={save}
        >
          Save link
        </Button>
        {current && (
          <Button variant="ghost" onClick={() => hub.navigate(`/devices/${current}`)}>
            Open brewer <ArrowUpRight size={15} />
          </Button>
        )}
      </div>
      {!brewers.length && (
        <p className="help-text">Add a brewer to the Hub to link this PicoFerm.</p>
      )}
    </section>
  )
}

export function LinkedPicoFerms({ brewerId }: { brewerId: string }) {
  const hub = useHub()
  const devices = hub.devices.filter(
    (device) => device.model === 'picoferm' && device.brewer_id === brewerId
  )
  const [ferment, setFerment] = useState(false)
  const brewer = hub.devices.find((device) => device.id === brewerId)
  if (
    brewer?.adapter !== 'pico-standalone' ||
    !brewerModels.includes(brewer.model) ||
    brewer.model_error
  )
    return null
  return (
    <section className="panel">
      <SectionHeading
        title="Fermentation"
        actions={
          <Button variant="secondary" onClick={() => setFerment(true)}>
            Ferment
          </Button>
        }
      />
      {ferment && <FermentDialog brewerId={brewerId} onClose={() => setFerment(false)} />}
      {!devices.length && (
        <p className="help-text">Choose a PicoFerm to monitor a keg from this brewer.</p>
      )}
      <div className="inline-actions">
        {devices.map((device) => (
          <Button
            key={device.id}
            variant="secondary"
            onClick={() => hub.navigate(`/devices/${device.id}`)}
          >
            {device.name} <ArrowUpRight size={15} />
          </Button>
        ))}
      </div>
    </section>
  )
}
