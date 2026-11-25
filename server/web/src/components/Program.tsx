import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import type { MachineRecipe, MachineStep, RecipeProvenance } from '../types'
import { archivePause, externalURL, programTiming, routeLabel } from '../lib/programs'
import { zDuration } from '../lib/zProgram'
import { Button, Field, IconButton, SectionHeading } from './ui'

export function ProgramTable({
  program,
  model,
  archived = false,
}: {
  program: MachineRecipe
  model?: string
  archived?: boolean
}) {
  if (program.z_cloud) {
    archived = true
    model = 'z'
  }
  return (
    <div className="table-scroll">
      {program.z_cloud && (
        <p>Starting water {(program.z_cloud.start_water_ml / 1000).toLocaleString()} L</p>
      )}
      <table className="program-table">
        <thead>
          <tr>
            <th>Step</th>
            <th>Flow path</th>
            <th>Target</th>
            <th>Hold</th>
            <th>Drain</th>
          </tr>
        </thead>
        <tbody>
          {program.z_program?.Steps.map((step, i) => (
            <tr key={i}>
              <td>
                {i + 1}. {step.Name}
              </td>
              {step.StepType === 2 ? (
                <td colSpan={4}>Pause until operator continues</td>
              ) : (
                <>
                  <td>{routeLabel(step.Location, 'z')}</td>
                  <td>{(step.Temp ?? 0) > 0 ? step.Temp + ' °C' : 'Unheated'}</td>
                  <td>
                    {step.Time
                      ? zDuration(step.Time)
                      : (step.Temp ?? 0) > 0
                        ? 'Reach target'
                        : 'Advance immediately'}
                  </td>
                  <td>{zDuration(step.Drain)}</td>
                </>
              )}
            </tr>
          ))}
          {!program.z_program &&
            program.steps.map((step, index) => {
              const path = routeLabel(step.location, model, archived)
              return (
                <tr key={index}>
                  <td>
                    <span className="muted">{index + 1}. </span>
                    {step.name}
                  </td>
                  {archived && archivePause(step.location, model) ? (
                    <td colSpan={4}>{path}</td>
                  ) : (
                    <>
                      <td>{path}</td>
                      <td>
                        {step.target_f === 0
                          ? program.z_cloud
                            ? 'No new target'
                            : 'Unheated'
                          : `${step.target_f} °F`}
                      </td>
                      <td>
                        {step.minutes === 0
                          ? step.target_f === 0
                            ? 'Advance immediately'
                            : 'Reach target'
                          : `${step.minutes} min`}
                      </td>
                      <td>{step.drain_minutes} min</td>
                    </>
                  )}
                </tr>
              )
            })}
        </tbody>
      </table>
      <p className="help-text">
        {program.z_program?.Steps.length ?? program.steps.length} steps ·{' '}
        {programTiming(program, archived ? model : undefined)}. Heating time depends on the brewer.
      </p>
    </div>
  )
}

export function ProgramFields({
  program,
  onChange,
  device = false,
  targetModel,
  title = 'Brew schedule',
  model,
  archived = false,
}: {
  program: MachineRecipe
  onChange: (program: MachineRecipe) => void
  device?: boolean
  targetModel?: string
  title?: string
  model?: string
  archived?: boolean
}) {
  const stock = targetModel === 'zymatic'
  const cloud = program.z_cloud
  const bounded = device || !!targetModel || !!cloud
  const maxSteps = stock ? 25 : bounded ? 15 : 256
  function updateStep(index: number, patch: Partial<MachineStep>) {
    onChange({
      ...program,
      steps: program.steps.map((step, i) => (i === index ? { ...step, ...patch } : step)),
    })
  }
  function move(index: number, delta: number) {
    const steps = [...program.steps]
    ;[steps[index], steps[index + delta]] = [steps[index + delta], steps[index]]
    onChange({ ...program, steps })
  }
  return (
    <>
      <SectionHeading
        title={title}
        description="A zero-minute hold reaches the target temperature, then advances."
        actions={
          <Button
            type="button"
            variant="secondary"
            disabled={program.steps.length >= maxSteps}
            onClick={() =>
              onChange({
                ...program,
                steps: [
                  ...program.steps,
                  {
                    name: 'New step',
                    location: stock || cloud ? 1 : 2,
                    target_f: 152,
                    minutes: 10,
                    drain_minutes: 0,
                  },
                ],
              })
            }
          >
            <Plus size={15} />
            Add program step
          </Button>
        }
      />
      <Field
        label="Machine recipe ID"
        hint="This ID identifies the saved slot. Changing it in a library copy creates a separate program when transferred."
      >
        <input
          value={program.id}
          readOnly={device}
          onChange={(event) => onChange({ ...program, id: event.target.value })}
          maxLength={20}
        />
      </Field>
      {cloud && (
        <div className="form-grid">
          <Field label="Starting water (L)">
            <input
              type="number"
              min="0.001"
              max="16.163"
              step="0.001"
              required
              value={cloud.start_water_ml / 1000 || ''}
              onChange={(e) =>
                onChange({
                  ...program,
                  z_cloud: { ...cloud, start_water_ml: Math.round(Number(e.target.value) * 1000) },
                })
              }
            />
          </Field>
          <label className="check-field">
            <input
              type="checkbox"
              checked={cloud.is_beer}
              onChange={(e) =>
                onChange({ ...program, z_cloud: { ...cloud, is_beer: e.target.checked } })
              }
            />{' '}
            Show beer setup instructions
          </label>
        </div>
      )}
      <div className="recipe-steps">
        {program.steps.map((step, index) => (
          <div className="recipe-step" key={index}>
            <span className="step-number">{String(index + 1).padStart(2, '0')}</span>
            <div className="step-fields">
              <Field label={`Program step ${index + 1} name`}>
                <input
                  value={step.name}
                  maxLength={stock ? 20 : bounded ? 21 : 512}
                  onChange={(event) => updateStep(index, { name: event.target.value })}
                />
              </Field>
              <div className="form-grid quarters">
                <Field label={`Step ${index + 1} flow path`}>
                  <select
                    value={step.location}
                    onChange={(event) => {
                      const location = Number(event.target.value)
                      updateStep(
                        index,
                        stock && location === 6
                          ? { location, target_f: 0, minutes: 0, drain_minutes: 0 }
                          : { location }
                      )
                    }}
                  >
                    {[0, 1, 2, 3, 4, 5, 6]
                      .filter((n) => n !== 0 || !!cloud || (!device && !model?.startsWith('pico')))
                      .map((n) => (
                        <option key={n} value={n}>
                          {routeLabel(n, cloud ? 'z' : model, archived || !!cloud)}
                        </option>
                      ))}
                  </select>
                </Field>
                <Field
                  label={`Step ${index + 1} target (°F)`}
                  hint={cloud ? '0 = no new target' : '0 = unheated'}
                >
                  <input
                    type="number"
                    min="0"
                    disabled={(stock || !!cloud) && step.location === 6}
                    max={stock ? 207 : 212}
                    step="1"
                    value={step.target_f}
                    onChange={(event) =>
                      updateStep(index, { target_f: Number(event.target.value) })
                    }
                  />
                </Field>
                <Field label={`Step ${index + 1} hold (min)`} hint="0 = reach target">
                  <input
                    type="number"
                    min="0"
                    disabled={(stock || !!cloud) && step.location === 6}
                    max={stock ? 255 : bounded ? 599 : 43200}
                    step="1"
                    value={step.minutes}
                    onChange={(event) => updateStep(index, { minutes: Number(event.target.value) })}
                  />
                </Field>
                <Field label={`Step ${index + 1} drain (min)`}>
                  <input
                    type="number"
                    min="0"
                    disabled={(stock || !!cloud) && step.location === 6}
                    max={stock ? 255 : bounded ? 120 : 1440}
                    step="1"
                    value={step.drain_minutes}
                    onChange={(event) =>
                      updateStep(index, { drain_minutes: Number(event.target.value) })
                    }
                  />
                </Field>
              </div>
            </div>
            <div className="step-actions">
              <IconButton
                type="button"
                label={`Move program step ${index + 1} up`}
                disabled={!index}
                onClick={() => move(index, -1)}
              >
                <ArrowUp size={15} />
              </IconButton>
              <IconButton
                type="button"
                label={`Move program step ${index + 1} down`}
                disabled={index === program.steps.length - 1}
                onClick={() => move(index, 1)}
              >
                <ArrowDown size={15} />
              </IconButton>
              <IconButton
                type="button"
                label={`Remove program step ${index + 1}`}
                disabled={program.steps.length <= 1}
                onClick={() =>
                  onChange({ ...program, steps: program.steps.filter((_, i) => i !== index) })
                }
              >
                <Trash2 size={15} />
              </IconButton>
            </div>
          </div>
        ))}
      </div>
    </>
  )
}

export function Provenance({ value }: { value?: RecipeProvenance }) {
  if (!value) return null
  const url = externalURL(value.source_url)
  return (
    <details className="source-provenance">
      <summary>Source & attribution</summary>
      <dl>
        <div>
          <dt>Archive</dt>
          <dd>{value.source_id}</dd>
        </div>
        <div>
          <dt>Original record</dt>
          <dd>
            {url ? (
              <a href={url} target="_blank" rel="noreferrer">
                {value.path}
              </a>
            ) : (
              value.path
            )}
          </dd>
        </div>
        <div>
          <dt>Source revision</dt>
          <dd>
            <code>{value.commit}</code>
          </dd>
        </div>
        <div>
          <dt>License</dt>
          <dd>{value.license}</dd>
        </div>
        <div>
          <dt>Source SHA-256</dt>
          <dd>
            <code>{value.sha256}</code>
          </dd>
        </div>
      </dl>
      {value.warnings?.length > 0 && (
        <ul>
          {value.warnings.map((warning, index) => (
            <li key={index}>{warning}</li>
          ))}
        </ul>
      )}
    </details>
  )
}
