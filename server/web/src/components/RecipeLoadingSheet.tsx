import { unmeasuredYeast } from '../lib/beerCrafter'
import { formatNumber } from '../lib/recipeDisplay'
import type { Adapted, Ingredient, Recipe } from '../types'
import { SourceEvidence } from './SourceEvidence'
import { useRecipeQuantity } from './RecipeMeasures'

export function EditionInstructions({ notes }: { notes?: string }) {
  const paragraph = notes?.split(/\n\s*\n/).find((p) => p.startsWith('Hub adaptation ('))
  const match = paragraph?.match(/^Hub adaptation \((.+?)\)\. ([\s\S]+)/)
  if (!match) return null
  // Historical directions may conflict with the procedure for this edition.
  const instructions = match[2].split(' Original record: ')[0].trim()
  return (
    <section>
      <h4>Hub adaptation · {match[1]}</h4>
      <SourceEvidence text={instructions} scaledBill />
    </section>
  )
}

function placement(item: Ingredient, slots?: Adapted['hop_slots_by_minute']) {
  const use = item.use.trim().toLowerCase()
  const time = item.minutes
  if (
    item.kind === 'water' &&
    use === 'mash' &&
    ['source starting water', 'scaled source water (reference)', 'starting keg water'].includes(
      item.name.trim().toLowerCase()
    )
  )
    return 'Starting keg water'
  const slot = (item.slot_addition || item.kind === 'hop') && slots?.[time]
  if (use === 'boil' || use === 'first-wort' || use === 'first wort') {
    const when =
      use !== 'boil'
        ? 'First wort'
        : time
          ? formatNumber(time) + ' min before boil ends'
          : 'Flameout'
    return slot ? 'Hop slot ' + slot + ' · ' + when : when
  }
  const labels: Record<string, string> = {
    mash: 'Mash',
    'dry-hop': 'Dry hop',
    'before pitching': 'Before pitching yeast',
    primary: 'Primary fermentation',
    fermentation: 'Fermentation',
    secondary: 'Secondary fermentation',
    conditioning: 'Conditioning',
    packaging: 'Packaging',
    bottling: 'Bottling',
    starter: 'Yeast starter',
    'starting keg': 'Starting keg water',
  }
  const label = labels[use] || item.use || 'See recipe notes'
  if (
    time > 0 &&
    ['dry-hop', 'primary', 'secondary', 'fermentation', 'conditioning'].includes(use)
  ) {
    return (
      label +
      ' · ' +
      (time >= 1440
        ? formatNumber(time / 1440) + (time === 1440 ? ' day' : ' days')
        : formatNumber(time) + ' min')
    )
  }
  return label
}

export function RecipeLoadingSheet({
  recipe,
  slots,
  original,
  scale = 1,
}: {
  recipe: Pick<Recipe, 'ingredients' | 'source_yeast'>
  slots?: Adapted['hop_slots_by_minute']
  original?: Ingredient[]
  scale?: number
}) {
  const quantity = useRecipeQuantity()
  const yeast = unmeasuredYeast(recipe)
  return (
    <div className="table-scroll">
      <table className="data-table" aria-label="Scaled ingredient bill">
        <thead>
          <tr>
            <th>Ingredient</th>
            {original && <th>Original</th>}
            <th>{original ? 'Adapted' : 'Amount'}</th>
            <th>When to add</th>
          </tr>
        </thead>
        <tbody>
          {yeast && (
            <tr>
              <td>{yeast}</td>
              {original && <td>Pitch amount unspecified</td>}
              <td>Pitch amount unspecified</td>
              <td>Fermentation</td>
            </tr>
          )}
          {recipe.ingredients.map((item, index) => (
            <tr key={index}>
              <td>{item.name}</td>
              {original && (
                <td>
                  {original[index] ? quantity(original[index].amount, original[index].unit) : '—'}
                </td>
              )}
              <td>{quantity(item.amount * scale, item.unit)}</td>
              <td>{placement(item, slots)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function TransferLoadingSheet({
  source,
  batchLiters,
  slots,
  ingredients,
}: {
  source?: Recipe
  batchLiters?: number
  ingredients?: Ingredient[]
  slots?: Adapted['hop_slots_by_minute']
}) {
  if (!batchLiters) return null
  const bill = ingredients?.length ? { ingredients, source_yeast: source?.source_yeast } : source
  const scale = ingredients?.length
    ? 1
    : source && source.batch_liters > 0
      ? batchLiters / source.batch_liters
      : 0
  if (!bill?.ingredients?.length || !scale) return null
  return (
    <>
      <EditionInstructions notes={source?.notes} />
      <RecipeLoadingSheet recipe={bill} scale={scale} slots={slots} />
    </>
  )
}
