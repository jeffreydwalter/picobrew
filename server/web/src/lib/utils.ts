import type { Device, Recipe, Sample, Telemetry, Unit } from '../types'
export function temperature(value: number | null | undefined, unit: Unit = 'F') {
  return value == null
    ? '—'
    : `${(unit === 'C' ? ((value - 32) * 5) / 9 : value).toFixed(1)}°${unit}`
}
export function convertTemp(value: number | null, unit: Unit) {
  return value == null ? null : unit === 'C' ? Math.round(((value - 32) * 50) / 9) / 10 : value
}
export function duration(milliseconds: number) {
  const total = Math.max(0, Math.floor(milliseconds / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  return h ? `${h}h ${m.toString().padStart(2, '0')}m` : `${m}:${s.toString().padStart(2, '0')}`
}
export function clockTime(milliseconds: number) {
  const total = Math.max(0, Math.floor(milliseconds / 1000))
  return `${Math.floor(total / 60)}:${(total % 60).toString().padStart(2, '0')}`
}
export function dateLabel(value: string | null | undefined) {
  return value
    ? new Intl.DateTimeFormat(undefined, {
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
      }).format(new Date(value))
    : 'Not recorded'
}
export function deviceAttention(device: Device) {
  const status = device.online ? device.status : null
  if (!status || status.stopped) return null
  if (!status.busy && status.recovery && (status.recovery.available || !status.recovery.valid)) {
    return {
      title: 'Review checkpoint',
      instruction: status.recovery.valid
        ? 'Open the brewer to review the interrupted brew.'
        : 'Open the brewer to discard the unreadable checkpoint.',
    }
  }
  return null
}
export function sessionInProgress(device: Device): boolean {
  const status = device.status
  if (!device.online || !status?.busy || status.stopped) return false
  // The PicoFerm also reports busy for pressure calibration and for each sensor reading.
  if (device.model === 'picoferm') return status.command === 'start'
  return !(status.command === 'front-panel' && status.device_state === 2)
}
export function deviceState(device: Device) {
  const attention = deviceAttention(device)
  if (attention) return attention.title
  return !device.online
    ? 'Offline'
    : device.status?.stopped
      ? 'Stopped'
      : device.status?.state
        ? device.status.state.replace(/[_-]/g, ' ').replace(/^./, (c) => c.toUpperCase())
        : device.status?.busy &&
            device.status.command === 'front-panel' &&
            device.status.device_state === 2
          ? 'At front panel'
          : device.status?.busy
            ? ['picoferm', 'picostill'].includes(device.model)
              ? 'Session active'
              : 'Brewing'
            : device.status?.ready
              ? 'Ready'
              : 'Connected'
}
export function modelLabel(model?: string) {
  const names: Record<string, string> = {
    pico: 'Pico',
    'pico-s': 'Pico S',
    'pico-pro': 'Pico Pro',
    'pico-c': 'Pico C',
    z: 'Pico Z',
    'z-series': 'Pico Z',
    picoferm: 'PicoFerm',
    picostill: 'PicoStill',
    zymatic: 'Zymatic',
  }
  return model ? names[model] || model.replace(/[-_]/g, ' ') : 'Unknown model'
}
export function deviceModelLabel(device: Pick<Device, 'model' | 'model_error' | 'enabled'>) {
  return device.model
    ? modelLabel(device.model)
    : device.model_error || !device.enabled
      ? 'Model not identified'
      : 'Detecting model'
}
export function liveReadingsStatus(status?: Partial<Telemetry>) {
  switch (status?.state) {
    case 'configured':
      return status.enabled === false
        ? 'Saved. Live readings are off.'
        : 'Saved. Live readings are configured.'
    case 'pending':
      return 'Saved. Will apply when the machine is available.'
    case 'waiting_idle':
      return 'Saved. Will apply when the machine is idle.'
    case 'disabled':
      return 'Saved. Will apply when monitoring is enabled.'
    case 'unsupported':
      return 'Saved. This firmware cannot send live readings.'
    case 'needs_hub_url':
      return 'Enter the Hub address and port the machine can reach.'
    case 'outcome_unknown':
      return 'Saved. Waiting for the machine to confirm the connection.'
    case 'conflict':
      return 'Receiver settings changed on the machine. Save to apply these settings.'
    case 'error':
      return 'Reconnect the machine to apply these settings.'
    case 'simulator':
      return 'Live readings stay in this Hub.'
    default:
      return 'Settings will apply when the machine is available.'
  }
}
export function slug(value: string) {
  return (
    value
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '') || 'export'
  )
}
export function newRecipe(): Recipe {
  return {
    id: '',
    name: '',
    style: '',
    description: '',
    batch_liters: 2.5 * 3.785411784, // Standard PicoBrew recipe size in US liquid gallons.
    efficiency: 70,
    ingredients: [],
    steps: [
      { name: 'Mash', kind: 'mash', target_c: 67, minutes: 60, drain_minutes: 2, location: 0 },
    ],
    tags: [],
    notes: '',
    revision: 0,
  }
}
export function transitions(samples: Sample[]) {
  let previousFlags: number | undefined
  return samples.filter((sample, i) => {
    if (sample.stock_pico) {
      const current = sample.stock_pico
      const previous = samples[i - 1]?.stock_pico
      return (
        current.kind !== 'log' ||
        !!current.event ||
        current.step !== previous?.step ||
        current.controller_status !== previous?.controller_status
      )
    }
    const report = sample.stock_zymatic
    if (report) {
      if (report.kind !== 'temperatures') return true
      const changed = report.error_flags != null && report.error_flags !== previousFlags
      previousFlags = report.error_flags
      return changed
    }
    return (
      i === 0 ||
      sample.phase !== samples[i - 1].phase ||
      sample.current_step !== samples[i - 1].current_step ||
      (sample.zymatic_history && sample.compartment !== samples[i - 1].compartment) ||
      sample.flags !== samples[i - 1].flags ||
      sample.device_state !== samples[i - 1].device_state
    )
  })
}
export function annotations(notes: string) {
  return Array.from(notes.matchAll(/^\[(\d+):(\d{2})\]\s*(.+)$/gm), (m) => ({
    elapsed_ms: (Number(m[1]) * 60 + Number(m[2])) * 1000,
    label: m[3],
  }))
}

export function ingredientUnits(kind: string) {
  return kind === 'fermentable'
    ? ['kg']
    : kind === 'hop'
      ? ['g']
      : kind === 'water'
        ? ['L']
        : ['g', 'mL', 'count']
}
