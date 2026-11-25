import { zDuration } from './zProgram'
import type { MachineRecipe, Recipe } from '../types'

export function isNative(recipe: Recipe) {
  return recipe.kind === 'native-program' || recipe.kind === 'maintenance'
}
export function kindLabel(kind?: string, sourceModel?: string) {
  if (kind === 'maintenance') return 'Cleaning program'
  if (kind !== 'native-program') return 'Ingredient recipe'
  if (['pico', 'pico-s', 'pico-pro', 'pico-c'].includes(sourceModel || '')) return 'PicoPak'
  if (sourceModel === 'z') return 'ZPak'
  if (sourceModel === 'zymatic') return 'Zymatic program'
  return 'Brew program'
}
// Typical uses in the bundled PicoPak starter schedules, not physical valve names.
export const picoPakStepUses = [
  'Heat water',
  'Mash',
  'Hop addition 1',
  'Hop addition 2',
  'Hop addition 3',
  'Hop addition 4',
]

// Archived Z/Zymatic schedules encode valves from zero and reserve 6 for a pause.
export function archivePause(location: number, model?: string) {
  return location === 6 && (model === 'z' || model === 'zymatic')
}
export function routeLabel(location: number, model?: string, archived = false) {
  if (archived && (model === 'z' || model === 'zymatic'))
    return archivePause(location, model)
      ? 'Pause'
      : location >= 0 && location <= 5
        ? [
            'Keg circulation',
            'Mash',
            'Hop addition 1',
            'Hop addition 2',
            'Hop addition 3',
            'Hop addition 4',
          ][location]
        : `Valve position ${location + 1}`
  if (location === 0) return 'Unassigned'
  // Z local programs route to the keg, mash compartment, or one of four adjuncts.
  if (
    (model === 'z' || model === 'z-series' || model === 'zymatic') &&
    location >= 1 &&
    location <= 6
  )
    return location === 1 ? 'Keg circulation' : picoPakStepUses[location - 1]
  if (
    ['pico', 'pico-s', 'pico-pro', 'pico-c'].includes(model ?? '') &&
    picoPakStepUses[location - 1]
  )
    return picoPakStepUses[location - 1]
  return `Valve position ${location}`
}
export function programTiming(program: MachineRecipe, archivedModel?: string) {
  if (program.z_program) {
    const steps = program.z_program.Steps
    const seconds = steps.reduce(
      (sum, step) => sum + (step.StepType === 1 ? step.Time + step.Drain : 0),
      0
    )
    return (
      zDuration(seconds) +
      (steps.some((s) => s.StepType === 1 && (s.Temp ?? 0) > 0) ? ' + heating' : '') +
      (steps.some((s) => s.StepType === 2) ? ' + pauses' : '')
    )
  }
  const active = program.steps.filter((step, i) =>
    program.z_pak
      ? program.z_pak.step_options[i]?.action !== 1
      : !archivePause(step.location, program.z_cloud ? 'z' : archivedModel)
  )
  const minutes = active.reduce((sum, step) => sum + step.minutes + step.drain_minutes, 0)
  return `${minutes} min${active.some((step) => step.target_f > 0) ? ' + heating' : ''}${active.length < program.steps.length ? ' + pauses' : ''}`
}
export function externalURL(value?: string) {
  if (!value) return undefined
  try {
    const url = new URL(value)
    return ['https:', 'http:'].includes(url.protocol) ? url.href : undefined
  } catch {
    return undefined
  }
}
export function catalogAssetURL(value?: string) {
  return value?.startsWith('/api/v1/catalog/assets/') && !value.includes('..') ? value : undefined
}
export function copyProgram(program: MachineRecipe): MachineRecipe {
  return structuredClone(program)
}

export function recipeSavePath(id: string, model?: string) {
  const path = id ? '/recipes/' + encodeURIComponent(id) : '/recipes'
  return model ? path + '?validate_for_model=' + encodeURIComponent(model) : path
}
