import { useState } from 'react'
import { useHub, useResource } from '../context'
import { api, errorMessage } from '../lib/api'
import type { Adapted, Ingredient, MachineRecipe, Recipe } from '../types'
import type { BrewGroup } from './BrewGroups'
import { Button, ErrorBanner, Field, Modal } from './ui'
import { TransferLoadingSheet } from './RecipeLoadingSheet'
import { ProgramTable } from './Program'
import { StockZTemperatureLimits, type StockZTemperatureLimit } from './StockZTemperatureLimits'

export type StockTarget = { id: string; name: string; model: string; base_url?: string }
type PicoMachine = { uid: string; name: string; model: string }
type ZMachine = { id: string; name: string; base_url: string }

export function useStockTargets() {
  const live = !useHub().session.demo
  const picos = useResource<{ machines: PicoMachine[] }>(live ? '/stock-picos' : null)
  const modules = useResource<{ machines: ZMachine[] }>(live ? '/stock-z-programs' : null)
  return [
    ...(picos.data?.machines || []).map((m) => ({ ...m, id: 'stock-pico:' + m.uid })),
    ...(modules.data?.machines || []).map((m) => ({
      ...m,
      id: 'stock-z:' + m.id,
      model: 'z-series',
    })),
  ] as StockTarget[]
}

type Item = {
  recipe_id: string
  ingredients?: Ingredient[]
  program?: MachineRecipe
  program_name?: string
  step_count?: number
  temperature_limits?: StockZTemperatureLimit[]
  batch_liters?: number
  hop_slots_by_minute?: Adapted['hop_slots_by_minute']
}
type Preview = { signature: string; hash: string; programHash?: string; items: Item[] }
type ZPreview = Item & { program_hash: string; review_hash: string; transfer_id?: string }

export function StockRecipeTransfer({
  target,
  recipes,
  initialSelection = [],
  onClose,
  back,
}: {
  target: StockTarget
  recipes: Recipe[]
  initialSelection?: string[]
  onClose: () => void
  back?: () => void
}) {
  const z = target.id.startsWith('stock-z:')
  const machine = encodeURIComponent(target.id.slice(target.id.indexOf(':') + 1))
  const base = (z ? '/stock-z-programs/' : '/stock-picos/') + machine
  const [ids, choose] = useState(z && initialSelection.length > 1 ? [] : initialSelection)
  const [tags, setTags] = useState<Record<string, string>>({})
  const [query, search] = useState('')
  const [review, setReview] = useState<Preview | null>(null)
  const [confirmed, confirm] = useState(false)
  const [sent, setSent] = useState(false)
  const [transferID, setTransferID] = useState('')
  const [offerMessage, setOfferMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const groups = useResource<{ groups: BrewGroup[] }>(z ? '/brew-groups' : null)
  const group = groups.data?.groups?.find((g) => g.device_ids.includes(target.id))
  const selected = recipes.filter((r) => ids.includes(r.id))
  const refs = selected.map((r) => ({
    recipe_id: r.id,
    revision: r.revision,
    ...(tags[r.id] !== undefined && { rfid: tags[r.id].trim() }),
  }))
  const signature = JSON.stringify([target.id, refs])
  const preview = review?.signature === signature ? review : null
  const visible = recipes.filter((r) => r.name.toLowerCase().includes(query.trim().toLowerCase()))
  function changeSelection(id: string) {
    choose((prev) => (z ? [id] : prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
    setReview(null)
    confirm(false)
    setError('')
  }
  async function submit() {
    if (busy || !refs.length || (z && refs.length !== 1)) return
    const commit = Boolean(preview)
    if (commit && z && !confirmed) return
    setBusy(true)
    setError('')
    try {
      if (z) {
        const response = await api<ZPreview>(base + '/recipes', {
          method: 'POST',
          body: {
            ...refs[0],
            confirm_replace: commit,
            ...(preview && { program_hash: preview.programHash, review_hash: preview.hash }),
          },
        })
        if (commit) setTransferID(response.transfer_id || '')
        else
          setReview({
            signature,
            hash: response.review_hash,
            programHash: response.program_hash,
            items: [{ ...response, recipe_id: refs[0].recipe_id }],
          })
      } else {
        const response = await api<{ items: Item[]; review_hash: string }>(base + '/recipes', {
          method: 'POST',
          body: { recipes: refs, publish: commit, ...(preview && { review_hash: preview.hash }) },
        })
        if (!commit) setReview({ signature, hash: response.review_hash, items: response.items })
      }
      if (commit) {
        setSent(true)
        window.dispatchEvent(new Event('stock-brewers-changed'))
      }
    } catch (e) {
      setError(errorMessage(e))
      setReview(null)
      confirm(false)
    } finally {
      setBusy(false)
    }
  }
  async function offer() {
    if (!group || busy || !sent || !transferID) return
    setBusy(true)
    setError('')
    try {
      const { offered } = await api<{ offered: number }>(base + '/group-offers', {
        method: 'POST',
        body: { transfer_id: transferID, group_id: group.id, group_revision: group.revision },
      })
      setOfferMessage(
        offered === 0
          ? 'No new invitations were available. Check the group on the Brewery page.'
          : `Offered to ${offered} other ${offered === 1 ? 'brewer' : 'brewers'} in ${group.name}. Each brewer accepts separately.`
      )
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title={'Send to ' + target.name}
      wide
      onClose={() => {
        if (!busy) onClose()
      }}
      actions={
        <>
          {back && !sent && (
            <Button variant="secondary" disabled={busy} onClick={back}>
              Change target
            </Button>
          )}
          {sent ? (
            <Button onClick={onClose} disabled={busy}>
              Done
            </Button>
          ) : (
            <Button
              onClick={() => void submit()}
              disabled={
                busy || !refs.length || (z && refs.length !== 1) || (z && !!preview && !confirmed)
              }
            >
              {busy
                ? 'Working…'
                : preview
                  ? z
                    ? 'Replace working recipe'
                    : 'Publish to brewer menu'
                  : refs.length > 1
                    ? 'Preview recipes'
                    : 'Preview recipe'}
            </Button>
          )}
        </>
      }
    >
      <ErrorBanner message={error} />
      {sent ? (
        <>
          <p role="status">
            {z
              ? 'Program Maker acknowledged the recipe. Review and start it at the Z’s dial.'
              : 'Published. Open the recipe menu on the brewer to fetch your selection.'}
          </p>
          {group &&
            transferID &&
            (offerMessage ? (
              <p role="status">{offerMessage}</p>
            ) : (
              <Button variant="secondary" disabled={busy} onClick={() => void offer()}>
                Offer recipe to {group.name}
              </Button>
            ))}
        </>
      ) : (
        <>
          <p>
            {z
              ? 'The Z stores one working recipe. You’ll review the replacement before sending it.'
              : 'Publish recipes for this brewer to fetch from the Hub. Select and start one at its dial.'}
          </p>
          {recipes.length > 1 && (
            <>
              <Field label="Find a saved recipe">
                <input
                  type="search"
                  value={query}
                  onChange={(e) => search(e.target.value)}
                  disabled={busy}
                />
              </Field>
              <div className="transfer-selection">
                {visible.map((r) => (
                  <label className="checkbox-row" key={r.id}>
                    <input
                      type={z ? 'radio' : 'checkbox'}
                      name={z ? 'stock-program' : undefined}
                      checked={ids.includes(r.id)}
                      disabled={busy}
                      onChange={() => changeSelection(r.id)}
                    />
                    {r.name}
                  </label>
                ))}
              </div>
              {!visible.length && <p>No matching recipes.</p>}
            </>
          )}
          {!recipes.length && <p>Save a recipe in your library first.</p>}
          {recipes.length === 1 && !refs.length && (
            <Button variant="secondary" onClick={() => changeSelection(recipes[0].id)}>
              {recipes[0].name}
            </Button>
          )}
          {!z &&
            selected.map((r) => (
              <Field
                key={r.id}
                label={(selected.length > 1 ? r.name + ' · ' : '') + 'RFID tag (optional)'}
              >
                <input
                  value={tags[r.id] ?? ''}
                  maxLength={20}
                  disabled={busy}
                  placeholder="Keep current assignment"
                  onChange={(e) => {
                    setTags({ ...tags, [r.id]: e.target.value })
                    setReview(null)
                  }}
                />
              </Field>
            ))}
          {preview?.items.map((item) => (
            <section key={item.recipe_id}>
              <h3>{item.program?.name || item.program_name}</h3>
              {item.program ? (
                <ProgramTable program={item.program} model={target.model} />
              ) : (
                <p>{item.step_count} program steps</p>
              )}
              <StockZTemperatureLimits limits={item.temperature_limits} />
              <TransferLoadingSheet
                source={recipes.find((r) => r.id === item.recipe_id)}
                ingredients={item.ingredients}
                batchLiters={item.batch_liters}
                slots={item.hop_slots_by_minute}
              />
            </section>
          ))}
          {z && preview && (
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={confirmed}
                disabled={busy}
                onChange={(e) => confirm(e.target.checked)}
              />
              <span>
                I verified that {target.name} at {target.base_url} is idle and want to replace its
                working recipe.
              </span>
            </label>
          )}
        </>
      )}
    </Modal>
  )
}
