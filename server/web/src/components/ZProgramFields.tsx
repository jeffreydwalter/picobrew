import { useRef, useState } from 'react'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useHub } from '../context'
import { activeZStep, sampleZProgram } from '../lib/zProgram'
import { routeLabel } from '../lib/programs'
import type { Unit, ZProgram, ZProgramStep } from '../types'
import { Button, Field, IconButton, SectionHeading } from './ui'

function DurationInput({
  label,
  seconds,
  max,
  change,
}: {
  label: string
  seconds: number
  max: number
  change: (s: number) => void
}) {
  const [text, setText] = useState<string | null>(null)
  const formatted = Number.isFinite(seconds)
    ? Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0')
    : ''
  return (
    <Field label={label}>
      <input
        required
        placeholder="0:00"
        value={text ?? formatted}
        onBlur={() => {
          if (Number.isFinite(seconds)) setText(null)
        }}
        onChange={(e) => {
          const value = e.target.value
          setText(value)
          const match = /^(\d{1,4})(?::([0-5]\d))?$/.exec(value)
          const total = match ? Number(match[1]) * 60 + Number(match[2] || 0) : NaN
          const valid = Number.isFinite(total) && total <= max * 60
          e.target.setCustomValidity(
            valid ? '' : `Enter up to ${max} minutes, using minutes or minutes:seconds.`
          )
          change(valid ? total : NaN)
        }}
      />
    </Field>
  )
}

function TemperatureInput({
  celsius,
  unit,
  change,
}: {
  celsius: number | null
  unit: Unit
  change: (n: number | null) => void
}) {
  const [text, setText] = useState<string | null>(null)
  const display = celsius === null ? NaN : unit === 'C' ? celsius : celsius * 1.8 + 32
  return (
    <Field
      label={`Target (°${unit})`}
      hint={celsius !== null && celsius <= 0 ? 'Heating is off for this step.' : undefined}
    >
      <input
        placeholder="Unheated"
        type="number"
        step="any"
        min={unit === 'C' ? -29 : -20.2}
        max={unit === 'C' ? 98 : 208.4}
        value={text ?? (Number.isFinite(display) ? Number(display.toFixed(6)) : '')}
        onBlur={() => setText(null)}
        onChange={(e) => {
          setText(e.target.value)
          const n = e.target.valueAsNumber
          if (!e.target.value && !e.target.validity.badInput) return change(null)
          change(unit === 'C' || !Number.isFinite(n) ? n : Number(((n - 32) / 1.8).toFixed(10)))
        }}
      />
    </Field>
  )
}

export function ZProgramFields({
  program,
  update,
  confirmSample,
  machineStepLimit,
}: {
  program: ZProgram
  update: (next: ZProgram) => void
  confirmSample: boolean
  machineStepLimit?: number
}) {
  const hub = useHub()
  const libraryStepLimit =
    hub.profiles.find((profile) => profile.id === 'z')?.z_program_max_steps ?? 15
  const stepLimit = Math.min(libraryStepLimit, machineStepLimit ?? libraryStepLimit)
  const savedUnit = program.IsMetric === undefined ? '' : program.IsMetric ? 'C' : 'F'
  const unit = savedUnit || hub.unit
  const stepKeys = useRef(program.Steps.map((_, i) => i))
  const nextKey = useRef(stepKeys.current.length)
  function changeStep(i: number, step: ZProgramStep) {
    update({ ...program, Steps: program.Steps.map((s, n) => (n === i ? step : s)) })
  }
  function move(i: number, j: number) {
    const steps = program.Steps.slice()
    ;[steps[i], steps[j]] = [steps[j], steps[i]]
    ;[stepKeys.current[i], stepKeys.current[j]] = [stepKeys.current[j], stepKeys.current[i]]
    update({ ...program, Steps: steps })
  }
  function loadSample() {
    const example = sampleZProgram()
    stepKeys.current = example.Steps.map(() => nextKey.current++)
    update(example)
  }
  return (
    <>
      <section className="panel">
        <SectionHeading
          title="Program settings"
          actions={
            <Button
              variant="secondary"
              type="button"
              onClick={() => {
                if (
                  !confirmSample ||
                  window.confirm('Replace these steps and settings with the sample program?')
                )
                  loadSample()
              }}
            >
              Load sample
            </Button>
          }
        />
        <div className="form-grid">
          <Field label="Program name">
            <input
              required
              maxLength={20}
              pattern="[ -~]{1,20}"
              value={program.Name}
              onChange={(e) => update({ ...program, Name: e.target.value })}
            />
          </Field>
          <Field label="Temperature units">
            <select
              value={savedUnit}
              onChange={(e) => {
                const next = { ...program }
                if (e.target.value) next.IsMetric = e.target.value === 'C'
                else delete next.IsMetric
                update(next)
              }}
            >
              <option value="">Use Hub preference (°{hub.unit})</option>
              <option value="C">Celsius (°C)</option>
              <option value="F">Fahrenheit (°F)</option>
            </select>
          </Field>
        </div>
        <label className="checkbox-row">
          <input
            type="checkbox"
            checked={program.IsBeer}
            onChange={(e) => update({ ...program, IsBeer: e.target.checked })}
          />
          Show beer setup instructions on the Z
        </label>
        <label className="checkbox-row">
          <input
            type="checkbox"
            checked={program.UsePicoPakMode}
            onChange={(e) => update({ ...program, UsePicoPakMode: e.target.checked })}
          />
          Use PicoPak adjunct compartments
        </label>
      </section>
      <section className="panel">
        <SectionHeading
          title="Program steps"
          description="Enter times as minutes or minutes:seconds. A zero hold advances when the target is reached."
        />
        {program.Steps.map((s, i) => (
          <div className="recipe-step" key={stepKeys.current[i]}>
            <span className="step-number">{i + 1}</span>
            <div className="step-fields">
              <div className="form-grid">
                <Field label="Step name">
                  <input
                    required
                    maxLength={20}
                    pattern="[ -~]{1,20}"
                    value={s.Name}
                    onChange={(e) => changeStep(i, { ...s, Name: e.target.value })}
                  />
                </Field>
                <Field label="Step type">
                  <select
                    value={s.StepType}
                    onChange={(e) =>
                      changeStep(
                        i,
                        e.target.value === '2' ? { Name: s.Name, StepType: 2 } : activeZStep(s.Name)
                      )
                    }
                  >
                    <option value="1">Active step</option>
                    <option value="2">Pause for operator</option>
                  </select>
                </Field>
              </div>
              {s.StepType === 1 ? (
                <div className="form-grid">
                  <Field label="Flow path">
                    <select
                      value={s.Location}
                      onChange={(e) => changeStep(i, { ...s, Location: Number(e.target.value) })}
                    >
                      {[1, 2, 3, 4, 5, 6].map((n) => (
                        <option key={n} value={n}>
                          {routeLabel(n, 'z')}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <TemperatureInput
                    celsius={s.Temp}
                    unit={unit}
                    change={(Temp) => changeStep(i, { ...s, Temp })}
                  />
                  <DurationInput
                    label="Hold (min:sec)"
                    max={1440}
                    seconds={s.Time}
                    change={(Time) => changeStep(i, { ...s, Time })}
                  />
                  <DurationInput
                    label="Drain (min:sec)"
                    max={120}
                    seconds={s.Drain}
                    change={(Drain) => changeStep(i, { ...s, Drain })}
                  />
                </div>
              ) : (
                <p className="help-text">The Z waits here until the operator continues.</p>
              )}
            </div>
            <div className="step-actions">
              <IconButton
                type="button"
                label={`Move step ${i + 1} up`}
                disabled={!i}
                onClick={() => move(i, i - 1)}
              >
                <ArrowUp size={16} />
              </IconButton>
              <IconButton
                type="button"
                label={`Move step ${i + 1} down`}
                disabled={i + 1 === program.Steps.length}
                onClick={() => move(i, i + 1)}
              >
                <ArrowDown size={16} />
              </IconButton>
              <IconButton
                type="button"
                label={`Remove step ${i + 1}`}
                disabled={program.Steps.length === 1}
                onClick={() => {
                  stepKeys.current.splice(i, 1)
                  update({ ...program, Steps: program.Steps.filter((_, n) => n !== i) })
                }}
              >
                <Trash2 size={16} />
              </IconButton>
            </div>
          </div>
        ))}
        <Button
          variant="secondary"
          type="button"
          disabled={program.Steps.length >= stepLimit}
          onClick={() => {
            stepKeys.current.push(nextKey.current++)
            update({ ...program, Steps: [...program.Steps, activeZStep('New step', 1, 0, 0)] })
          }}
        >
          <Plus size={16} />
          Add step
        </Button>
        {program.Steps.length >= stepLimit && (
          <span className="help-text"> Step limit reached ({stepLimit}).</span>
        )}
      </section>
    </>
  )
}
