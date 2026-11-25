import type { MachineRecipe } from '../types'
import { Button, Field } from './ui'
import type { ReactNode } from 'react'

export function ZPicoPakFields({
  program,
  onChange,
  library,
  children,
}: {
  program: MachineRecipe
  onChange: (next: MachineRecipe) => void
  children: ReactNode
  library: boolean
}) {
  const pak = program.z_pak!
  return (
    <>
      <h3>Z PicoPak adapter</h3>
      <p>
        On a Z, load the original PicoPak in the PicoPak adapter. Setup calls for 6.81 L of water.
      </p>
      <div className="form-grid">
        {(['abv', 'ibu'] as const).map((kind) => {
          const baseline = pak[kind]
          if (
            baseline < 1 ||
            baseline > 9 ||
            !pak.step_options.some(
              (option, i) =>
                option.action === 0 &&
                option.min_time != null &&
                option.max_time != null &&
                (kind === 'abv'
                  ? program.steps[i]?.location === 2
                  : (program.steps[i]?.location ?? 0) >= 3)
            )
          )
            return null
          const field = kind === 'abv' ? 'selected_abv' : 'selected_ibu'
          return (
            <Field
              key={kind}
              label={kind === 'abv' ? 'Kit strength' : 'Kit bitterness'}
              hint="The Z adjusts the kit’s programmed times."
            >
              <select
                value={pak[field] ?? ''}
                onChange={(event) => {
                  const next = { ...pak }
                  if (event.target.value === '') delete next[field]
                  else next[field] = Number(event.target.value)
                  onChange({ ...program, z_pak: next })
                }}
              >
                <option value="">Original ({baseline} of 10)</option>
                {Array.from({ length: 11 }, (_, i) => i)
                  .filter((i) => i !== baseline)
                  .map((i) => (
                    <option key={i} value={i}>
                      {i} of 10
                    </option>
                  ))}
              </select>
            </Field>
          )
        })}
      </div>
      {children}
      {library && (
        <Button
          type="button"
          variant="secondary"
          onClick={() => {
            if (
              !window.confirm(
                'Remove the Z adapter setup and kit adjustments from this library copy to edit it as a standard Pico program?'
              )
            )
              return
            const { z_pak: _kit, ...copy } = program
            onChange(copy)
          }}
        >
          Edit as a standard Pico program
        </Button>
      )}
    </>
  )
}
