import { useHub } from '../context'
import { formatNumber } from '../lib/recipeDisplay'

export type StockZTemperatureLimit = { step: string; requested_c: number; maximum_c: number }

export function StockZTemperatureLimits({ limits }: { limits?: StockZTemperatureLimit[] }) {
  const { unit } = useHub()
  if (!limits?.length) return null
  function degrees(celsius: number) {
    return unit === 'C'
      ? formatNumber(celsius) + ' °C'
      : formatNumber(celsius * 1.8 + 32, 1) + ' °F'
  }
  return (
    <section>
      <h4>Kettle temperature</h4>
      <ul>
        {limits.map((entry, index) => (
          <li key={index}>
            {entry.requested_c > entry.maximum_c
              ? `${entry.step} requests ${degrees(entry.requested_c)}. The Z limits it to ${degrees(entry.maximum_c)}.`
              : `${entry.step} targets ${degrees(entry.requested_c)}, the Z’s kettle limit.`}
          </li>
        ))}
      </ul>
      <p>
        The brewer can lower the target further for the local boiling point and extend holds longer
        than 10 minutes.
      </p>
    </section>
  )
}
