import { sessionState } from '../lib/sessionState'
import type { BrewDetail, Sample, Unit } from '../types'
import { clockTime, duration, temperature } from '../lib/utils'
import {
  unheatedTarget,
  sampleReading,
  zymaticReportLabel,
  zymaticReportState,
} from '../lib/history'
import { sensorValue } from '../lib/capabilities'

export function HistorySamples({
  brew,
  page,
  unit,
}: {
  brew: BrewDetail
  page: number
  unit: Unit
}) {
  const accessory = ['picoferm', 'picostill'].includes(brew.model)
  const columns = (
    [
      ['wort_f', 'Wort'],
      ['thermoblock_f', 'Thermoblock'],
      ['target_f', 'Target'],
      ['step_seconds', 'Step time'],
      ['phase', 'Phase'],
      ['controller_status', 'Controller'],
      ['compartment', 'Compartment'],
      ['current_step', 'Step'],
    ] as const
  ).filter(([key]) => !accessory && (!brew.sample_fields || brew.sample_fields.includes(key)))
  const stateColumns = (
    [
      ['flags', 'Flags'],
      ['device_state', 'State'],
    ] as const
  ).filter(([key]) => !brew.sample_fields || brew.sample_fields.includes(key))
  const stable: Record<string, { label: string; unit: string }> = {
    temperature_f: { label: 'Temperature', unit: 'F' },
    pressure_kpa: { label: 'Pressure', unit: 'kPa' },
    battery_v: { label: 'Battery', unit: 'V' },
  }
  const extra = (
    brew.sample_fields ||
    Object.keys(stable).concat(...brew.samples.map((sample) => Object.keys(sample.readings || {})))
  ).filter(
    (field, index, fields) =>
      fields.indexOf(field) === index &&
      (stable[field] || field.startsWith('sensor_') || field.startsWith('actuator_'))
  )
  const metadata = (field: string) =>
    brew.field_metadata?.[field] ||
    stable[field] || { label: field.replaceAll('_', ' '), unit: 'raw' }
  return (
    <div className="table-scroll">
      <table className="data-table sample-table">
        <thead>
          <tr>
            <th>Time</th>
            {brew.source === 'zymatic-report' && <th>Brewer report</th>}
            {columns.map(([key, label]) => (
              <th key={key}>{brew.field_metadata?.[key]?.label || label}</th>
            ))}
            {extra.map((field) => (
              <th key={field}>
                {metadata(field).label} (
                {metadata(field).unit === 'F' ? unit : metadata(field).unit})
              </th>
            ))}
            {stateColumns.map(([key, label]) => (
              <th key={key}>{label}</th>
            ))}
            <th>Source</th>
          </tr>
        </thead>
        <tbody>
          {brew.samples.slice(page * 100, (page + 1) * 100).map((sample: Sample, index) => (
            <tr key={index}>
              <td>{clockTime(sample.elapsed_ms)}</td>
              {sample.stock_zymatic && (
                <td>
                  {[
                    zymaticReportLabel(sample.stock_zymatic),
                    zymaticReportState(sample.stock_zymatic),
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </td>
              )}
              {columns.map(([key]) => {
                const raw = sample[key]
                const value =
                  key === 'wort_f' || key === 'thermoblock_f' || key === 'target_f'
                    ? key === 'target_f' && unheatedTarget(brew.model, raw ?? null)
                      ? 'Unheated'
                      : temperature(raw, unit)
                    : key === 'step_seconds'
                      ? raw == null
                        ? 'Not recorded'
                        : duration(raw * 1000)
                      : sessionState(brew, key, raw, sample.source)
                return <td key={key}>{value}</td>
              })}
              {extra.map((field) => (
                <td key={field}>
                  {sensorValue(
                    { value: sampleReading(sample, field), unit: metadata(field).unit },
                    unit
                  )}
                </td>
              ))}
              {stateColumns.map(([key]) => (
                <td key={key}>{sessionState(brew, key, sample[key], sample.source)}</td>
              ))}
              <td>{sample.stock_zymatic ? 'Brewer report' : sample.source}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
