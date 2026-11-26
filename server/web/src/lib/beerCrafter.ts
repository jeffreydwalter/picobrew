import reference from '../data/beer-crafter.json'
import type { Ingredient, Recipe } from '../types'

export const brewingReference = reference
const poundsPerKg = 2.20462262
const litersPerGallon = 3.785411784
const sucrosePPG = 46
const key = (s: string) => s.trim().toLowerCase()
export function grainInfo(name: string) {
  const grain = reference.fermentables.find((i) => key(i.name) === key(name))
  // The archived reference misclassifies this whole-kernel wheat malt as dry extract.
  // https://www.weyermann.de/en-us/product/weyermann-oak-smoked-wheat-malt-6/
  return grain?.name === 'Oak Smoked Wheat' ? { ...grain, type: 'Grain' } : grain
}
export const hopInfo = (name: string) => reference.hops.find((i) => key(i.name) === key(name))
export const yeastInfo = (name: string) => reference.yeasts.find((i) => key(i.name) === key(name))
export function styleInfo(name: string) {
  return (
    reference.styles.find((s) => s.name === name) ??
    reference.styles.find((s) => key(s.label) === key(name))
  )
}
export function ingredientDefaults(kind: string, name: string): Partial<Ingredient> {
  if (kind === 'fermentable') {
    const g = grainInfo(name)
    if (g)
      return {
        yield: Number(((g.ppg / sucrosePPG) * 100).toFixed(2)),
        color: g.color,
        type: g.type,
      }
  }
  if (kind === 'hop') {
    const h = hopInfo(name)
    if (h) return { alpha: h.alpha }
  }
  if (kind === 'yeast') {
    const y = yeastInfo(name)
    if (y) return { attenuation: y.attenuation }
  }
  if (kind === 'misc') {
    const m = reference.misc.find((i) => key(i.name) === key(name))
    if (m) return { type: m.type }
  }
  return {}
}
export function renameIngredient(item: Ingredient, name: string) {
  const old = ingredientDefaults(item.kind, item.name)
  const clear = Object.fromEntries(
    Object.entries(old)
      .filter(([k, v]) => item[k as keyof Ingredient] === v)
      .map(([k]) => [k, undefined])
  )
  return { ...clear, name, ...ingredientDefaults(item.kind, name) }
}

export const goesInKeg = (type?: string) => /^(sugar|extract|dry extract)$/i.test(type ?? '')

export function unmeasuredYeast(r: Pick<Recipe, 'ingredients' | 'source_yeast'>) {
  return r.ingredients?.some((i) => i.kind === 'yeast') ? undefined : r.source_yeast
}

export function ingredientBill(ingredients: Ingredient[], kind: string) {
  const weights = new Map<string, number>()
  for (const i of ingredients) {
    if (i.kind !== kind || !(i.amount > 0) || !Number.isFinite(i.amount)) continue
    const name = i.name.trim() || 'Unnamed ingredient'
    weights.set(name, (weights.get(name) ?? 0) + i.amount)
  }
  return [...weights]
    .map(([name, amount]) => ({ name, amount }))
    .sort((a, b) => b.amount - a.amount || a.name.localeCompare(b.name))
}
const kettleHop = (i: Ingredient) =>
  i.kind === 'hop' && ['boil', 'first-wort', 'first wort'].includes(key(i.use))
export function beerStats(r: Recipe) {
  // Lactose is also stored as a weighed miscellaneous addition in older records.
  const milkSugar = (i: Ingredient) => /\b(lactose|milk sugar)\b/i.test(i.name)
  const bill = r.ingredients.filter(
    (i) => i.amount > 0 && (i.kind === 'fermentable' || (i.kind === 'misc' && milkSugar(i)))
  )
  const hotUses = ['mash', 'steep', 'boil']
  const laterUses = ['secondary', 'conditioning', 'priming', 'packaging', 'bottling']
  const kettle = bill.filter((i) => hotUses.includes(key(i.use)))
  const beforePitch = bill.filter((i) => key(i.use) === 'before pitching')
  const knownPlacement = bill.every((i) =>
    [...hotUses, ...laterUses, 'before pitching', 'starter', 'primary', 'fermentation'].includes(
      key(i.use)
    )
  )
  const gallons = r.batch_liters / litersPerGallon
  function pointsPerGallon(items: Ingredient[], volume: number) {
    if (
      volume <= 0 ||
      !items.length ||
      !knownPlacement ||
      !items.every(
        (i) => i.unit === 'kg' && (i.yield ?? 0) > 0 && (goesInKeg(i.type) || r.efficiency > 0)
      )
    )
      return null
    const points = items.reduce(
      (sum, i) =>
        sum +
        i.amount *
          poundsPerKg *
          sucrosePPG *
          ((i.yield ?? 0) / 100) *
          (goesInKeg(i.type) ? 1 : r.efficiency / 100),
      0
    )
    return points / volume
  }
  // A generic primary addition could be before or after the pitching-gravity reading.
  const pitchingPoints = bill.some((i) => ['primary', 'fermentation'].includes(key(i.use)))
    ? null
    : pointsPerGallon([...kettle, ...beforePitch], gallons)
  const og = pitchingPoints === null ? null : 1 + pitchingPoints / 1000
  const colorUnits = kettle.reduce((sum, i) => sum + i.amount * poundsPerKg * (i.color ?? 0), 0)
  const srm =
    gallons > 0 &&
    knownPlacement &&
    kettle.length &&
    kettle.every((i) => i.unit === 'kg' && i.color !== undefined)
      ? 1.4922 * (colorUnits / gallons) ** 0.6859
      : null
  const yeasts = r.ingredients.filter((i) => i.kind === 'yeast')
  const attenuations = new Set(yeasts.map((i) => i.attenuation))
  const attenuation = attenuations.size === 1 ? yeasts[0]?.attenuation : undefined
  // Yeast attenuation alone cannot predict residual lactose, fermentation of
  // supplemental sugars, or packaged gravity after additions and dilution.
  const simpleFermentation = bill.every(
    (i) => key(i.use) === 'starter' || (hotUses.includes(key(i.use)) && !milkSugar(i))
  )
  const fg =
    og !== null && simpleFermentation && attenuation && attenuation <= 100
      ? 1 + (og - 1) * (1 - attenuation / 100)
      : null
  const abv = og !== null && fg !== null ? (og - fg) * 131.25 : null
  const kettlePoints = pointsPerGallon(kettle, (r.boil_liters || r.batch_liters) / litersPerGallon)
  const varyingBoilGravity = kettle.some(
    (i) => key(i.use) === 'boil' && i.minutes > 0 && i.minutes !== r.boil_minutes
  )
  const boilGravity = kettlePoints === null || varyingBoilGravity ? null : 1 + kettlePoints / 1000
  const hops = r.ingredients.filter((i) => kettleHop(i) && i.amount > 0 && i.minutes > 0)
  let ibu: number | null = null
  if (r.batch_liters > 0 && !hops.length) ibu = 0
  else if (boilGravity !== null && hops.every((i) => (i.alpha ?? 0) > 0)) {
    const factor = 1.65 * 0.000125 ** (boilGravity - 1)
    ibu = hops.reduce(
      (sum, i) =>
        sum +
        (i.amount * 10 * (i.alpha ?? 0) * factor * -Math.expm1(-0.04 * i.minutes)) /
          4.15 /
          r.batch_liters,
      0
    )
  }
  return { og, fg, abv, ibu, srm }
}
export function wortCurve(r: Recipe) {
  const program = r.native_program
  const steps = program
    ? program.steps.map((s) => ({
        name: s.name,
        c: s.target_f === 0 ? null : (s.target_f - 32) / 1.8,
        hold: s.minutes,
        drain: s.drain_minutes,
        ramp: 0,
        pause: (r.source_model === 'z' || r.source_model === 'zymatic') && s.location === 6,
      }))
    : r.steps.map((s) => ({
        name: s.name,
        c: s.target_c === 0 ? null : s.target_c,
        hold: s.minutes,
        drain: s.drain_minutes,
        ramp: s.ramp_minutes || 0,
        pause: false,
      }))
  const points: { minute: number; c: number | null; name: string }[] = []
  let minutes = 0,
    temp: number | null = (55 - 32) / 1.8,
    chill = 0
  for (const [index, s] of steps.entries()) {
    if (s.pause) {
      points.push({ minute: minutes, c: null, name: s.name })
      continue
    }
    points.push({ minute: minutes, c: temp, name: s.name })
    const ramp =
      s.ramp ||
      (s.c !== null && temp !== null ? Math.abs(s.c - temp) / (s.c > temp ? 4.96 : 0.585) : 0)
    if (index === steps.length - 1 && temp !== null && s.c !== null && s.c < temp) chill = ramp
    minutes += ramp
    temp = s.c
    points.push({ minute: minutes, c: temp, name: s.name })
    minutes += s.hold
    points.push({ minute: minutes, c: temp, name: s.name })
    if (s.drain) {
      minutes += s.drain
      points.push({ minute: minutes, c: temp, name: 'Drain · ' + s.name })
    }
  }
  return { points, minutes, chill }
}
