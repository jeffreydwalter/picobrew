import type { BrewDetail, FieldMetadata, Sample } from '../types'

const stable: Record<string, FieldMetadata> = {
  wort_f: { label: 'Wort', unit: 'F', kind: 'temperature' },
  thermoblock_f: { label: 'Thermoblock', unit: 'F', kind: 'temperature' },
  target_f: { label: 'Target', unit: 'F', kind: 'temperature' },
  temperature_f: { label: 'Temperature', unit: 'F', kind: 'temperature' },
  pressure_kpa: { label: 'Pressure', unit: 'kPa', kind: 'pressure' },
  battery_v: { label: 'Battery', unit: 'V', kind: 'voltage' },
}

export function sampleReading(sample: Sample, field: string): number | null {
  const value =
    field.startsWith('sensor_') || field.startsWith('actuator_')
      ? sample.readings?.[field]
      : sample[field as keyof Sample]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function historyChannels(
  brew: Pick<BrewDetail, 'samples' | 'sample_fields' | 'field_metadata' | 'model'>
): { field: string; metadata: FieldMetadata }[] {
  const fields =
    brew.sample_fields ||
    Array.from(
      new Set([
        ...Object.keys(stable),
        ...brew.samples.flatMap((sample) => Object.keys(sample.readings || {})),
      ])
    )
  const accessory = ['picostill', 'picoferm'].includes(brew.model)
  return fields
    .filter((field) => {
      if (!stable[field] && !field.startsWith('sensor_') && !field.startsWith('actuator_'))
        return false
      if (accessory && ['wort_f', 'thermoblock_f', 'target_f'].includes(field)) return false
      return brew.samples.some((sample) => sampleReading(sample, field) !== null)
    })
    .map((field) => ({
      field,
      metadata: brew.field_metadata?.[field] ||
        stable[field] || { label: field.replaceAll('_', ' '), unit: 'raw', kind: 'other' },
    }))
}

// Small Pico brew programs use zero as the unheated target sentinel.
export function unheatedTarget(model: string, target: number | null) {
  return target === 0 && ['pico-s', 'pico-pro', 'pico-c'].includes(model)
}

const zymaticResets: Record<number, string> = {
  300: 'Debug interface reset',
  301: 'External reset',
  302: 'Power-off reset',
  303: 'Brownout reset',
  304: 'System reset',
  305: 'Software reset',
}
export function zymaticError(code: number) {
  return zymaticResets[code] ?? `Brewer error ${code}`
}
export function zymaticReportLabel(report: NonNullable<Sample['stock_zymatic']>) {
  if (report.kind === 'step') return report.step_name || 'Step reported'
  if (report.kind === 'error')
    return report.error_code == null ? 'Brewer error' : zymaticError(report.error_code)
  return 'Temperature report'
}
export function zymaticReportState(report: NonNullable<Sample['stock_zymatic']>) {
  if (report.state != null) return `Reported state ${report.state}`
  if (report.error_flags == null) return ''
  if (report.error_flags === 0) return 'No fault flags'
  if (report.error_flags === 64) return 'Sensor disconnected'
  return `Reported fault flags ${report.error_flags}`
}

export function picoReportLabel(report: NonNullable<Sample['stock_pico']>) {
  if (report.kind === 'error') return `Brewer error ${report.error_code}`
  if (report.kind === 'state') return `Brewer state ${report.state}`
  return [report.step, report.event !== report.step && report.event].filter(Boolean).join(' · ')
}
