import { useState } from 'react'
import { RefreshCw, Save } from 'lucide-react'
import { useHub } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import { copyProgram } from '../lib/programs'
import type { MachineRecipe } from '../types'
import { Button, ErrorBanner, Field, Modal } from './ui'
import { ProgramFields } from './Program'
import { ZProgramFields } from './ZProgramFields'

export function InstalledProgramDialog({
  deviceID,
  initial,
  onClose,
  onSaved,
}: {
  deviceID: string
  initial: MachineRecipe
  onClose: () => void
  onSaved: () => void
}) {
  const hub = useHub()
  const [program, setProgram] = useState(() => copyProgram(initial))
  const [generation, setGeneration] = useState(0)
  const [expected, setExpected] = useState(() => copyProgram(initial))
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [refreshRequired, setRefreshRequired] = useState(false)
  const target = hub.devices.find((device) => device.id === deviceID)
  const ready =
    target?.online && target.status?.ready && !target.status.busy && !target.status.stopped
  async function save() {
    setBusy('save')
    setError('')
    try {
      await api(`/devices/${deviceID}/recipes/${encodeURIComponent(initial.id)}`, {
        method: 'PUT',
        body: { recipe: program, expected },
      })
      onSaved()
      hub.notify('Saved program verified on the machine.')
      onClose()
    } catch (error) {
      setError(errorMessage(error))
      // These Hub rejections happen before a write. Keep the draft editable.
      const rejectedBeforeWrite =
        error instanceof APIError &&
        ['invalid_json', 'invalid_recipe', 'unsupported_recipe', 'machine_not_idle'].includes(
          error.code
        )
      setRefreshRequired(!rejectedBeforeWrite)
    } finally {
      setBusy('')
    }
  }
  async function reload() {
    setBusy('reload')
    setError('')
    try {
      const result = await api<{ recipes: MachineRecipe[] }>(`/devices/${deviceID}/recipes`)
      const current = result.recipes.find((recipe) => recipe.id === initial.id)
      if (!current)
        throw new Error(
          'This program is no longer on the machine. Close the editor and refresh its recipe list.'
        )
      setGeneration((value) => value + 1)
      setProgram(copyProgram(current))
      setExpected(copyProgram(current))
      setRefreshRequired(false)
      onSaved()
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy('')
    }
  }
  return (
    <Modal
      title="Edit the machine’s saved program"
      wide
      onClose={() => {
        if (!busy) onClose()
      }}
      actions={
        <>
          <Button variant="secondary" disabled={!!busy} onClick={onClose}>
            Cancel
          </Button>
          {refreshRequired ? (
            <Button variant="secondary" disabled={!!busy} onClick={() => void reload()}>
              <RefreshCw size={16} />
              {busy === 'reload' ? 'Reading…' : 'Reload current machine copy'}
            </Button>
          ) : (
            <Button disabled={!!busy || !ready} type="submit" form="installed-program-form">
              <Save size={16} />
              {busy === 'save' ? 'Saving & verifying…' : 'Save to machine'}
            </Button>
          )}
        </>
      }
    >
      <p>Changes are saved to this brewer and are available from its front panel and browser.</p>
      <ErrorBanner message={error} />
      {refreshRequired && (
        <p className="notice">
          Read the current machine copy before saving again. Reloading replaces the editor contents
          with what the brewer reports.
        </p>
      )}
      {!ready && (
        <p className="notice">
          The brewer must be online, idle and ready before its saved programs can be edited.
        </p>
      )}
      <form
        id="installed-program-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (!busy && ready && !refreshRequired) void save()
        }}
      >
        <fieldset disabled={!!busy || refreshRequired} className="program-fieldset">
          {program.z_program ? (
            <ZProgramFields
              key={generation}
              confirmSample
              program={program.z_program}
              machineStepLimit={
                target?.capabilities?.z_program_step_capacity ??
                target?.capabilities?.step_capacity ??
                15
              }
              update={(z) => setProgram({ ...program, name: z.Name, z_program: z })}
            />
          ) : (
            <>
              <Field label="Saved program name">
                <input
                  value={program.name}
                  maxLength={31}
                  onChange={(event) =>
                    setProgram((value) => ({ ...value, name: event.target.value }))
                  }
                />
              </Field>
              <ProgramFields program={program} onChange={setProgram} device model={target?.model} />
            </>
          )}
        </fieldset>
      </form>
    </Modal>
  )
}
