import { useState } from 'react'
import { api, errorMessage, saveBlob } from '../lib/api'
import { useResource } from '../context'
import type { Recipe, MachineRecipe } from '../types'
import { Button, ErrorBanner, Field, Modal } from './ui'
import { StockSetupGuide } from './StockSetupGuide'
import { ProgramTable } from './Program'
import { TransferLoadingSheet } from './RecipeLoadingSheet'

type StockItem = {
  recipe_id: string
  revision: number
  program: MachineRecipe
  batch_liters?: number
  hop_slots_by_minute?: Record<number, number>
  maintenance: boolean
}
type Menu = { machine: string; items: StockItem[]; last_requested: string }
export const stockTarget = 'stock-zymatic-menu'
export const offlineTarget = 'offline-zymatic-program'

export function StockZymaticTransfer({
  recipes,
  initialSelection,
  back,
  close,
  paired,
  offline = false,
}: {
  recipes: Recipe[]
  initialSelection: string[]
  back: () => void
  close: () => void
  paired?: { name: string; serial?: string }
  offline?: boolean
}) {
  const reported =
    paired?.serial && /^[a-f0-9]{12}$/i.test(paired.serial) ? paired.serial.toUpperCase() : ''
  const [installProgram, setInstallProgram] = useState<MachineRecipe>()
  const [machine, setMachine] = useState(() =>
    offline ? '' : paired ? reported : localStorage.getItem('stock-zymatic-serial') || ''
  )
  const [chosen, setChosen] = useState(initialSelection)
  const [routing, setRouting] = useState<Record<string, number[]>>({})
  function paths(r: Recipe) {
    return (
      routing[r.id] || (r.steps || []).map((s) => (r.source_model === 'zymatic' ? s.location : 0))
    )
  }
  const selected = recipes.filter((r) => chosen.includes(r.id))
  const [manualPaths, setManualPaths] = useState(false)
  const candidates = selected.filter(
    (r) => !r.native_program && (r.source_model !== 'zymatic' || r.steps.some((s) => !s.location))
  )
  const needsPaths = candidates.filter((r) => manualPaths || !r.ingredients?.length)
  const ready = needsPaths.every((r) => paths(r).every(Boolean))
  const [preview, setPreview] = useState<StockItem[] | null>(null),
    [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const id = machine.trim().toUpperCase(),
    valid = /^[A-Z0-9]{1,32}$/.test(id)
  const path = '/stock-zymatics/' + encodeURIComponent(id) + '/recipes'
  const menu = useResource<Menu>(!offline && valid ? path : null)
  async function submit(publish: boolean) {
    if (busy || (publish && (offline || !valid))) return
    setBusy(true)
    setError('')
    try {
      const references = selected.map((r) => ({
        recipe_id: r.id,
        revision: r.revision,
        ...(needsPaths.includes(r) ? { locations: paths(r) } : {}),
      }))
      const response = await api<{ items: StockItem[] }>(
        offline ? '/stock-zymatics/preview' : path,
        {
          method: 'POST',
          body: offline ? { recipes: references } : { recipes: references, publish },
        }
      )
      setPreview(response.items)
      if (publish) {
        setSent(true)
        if (!paired) localStorage.setItem('stock-zymatic-serial', id)
        menu.reload()
      }
    } catch (e) {
      setError(errorMessage(e))
      setPreview(null)
    } finally {
      setBusy(false)
    }
  }
  async function remove(item: StockItem) {
    setBusy(true)
    setError('')
    try {
      await api(path + '/' + encodeURIComponent(item.recipe_id), { method: 'DELETE' })
      menu.reload()
      setPreview(null)
      setSent(false)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  if (installProgram)
    return <StockSetupGuide program={installProgram} onClose={() => setInstallProgram(undefined)} />
  return (
    <Modal
      title={
        offline
          ? 'Offline Zymatic program'
          : paired
            ? 'Send to ' + paired.name
            : 'Send to stock Zymatic'
      }
      wide
      onClose={close}
      actions={
        <>
          <Button variant="secondary" disabled={busy} onClick={back}>
            Change target
          </Button>
          {sent || (offline && preview) ? (
            <Button onClick={close}>Done</Button>
          ) : (
            <Button
              disabled={(!valid && !offline) || !chosen.length || !ready || busy}
              onClick={() => void submit(!!preview)}
            >
              {busy
                ? 'Working…'
                : preview
                  ? 'Publish to brewer menu'
                  : offline
                    ? 'Preview offline programs'
                    : 'Preview stock menu'}
            </Button>
          )}
        </>
      }
    >
      <p>
        {offline
          ? 'Review a recipe to install it for offline use. The Zymatic holds one offline recipe.'
          : paired
            ? 'Open the recipe menu on the brewer to fetch the published recipes from its configured Hub.'
            : 'The stock brewer fetches its menu over HTTP. Point its PicoBrew hostname to this Hub on LAN port 80, then open the recipe menu on the machine.'}
      </p>
      {!offline && (
        <Field label="Zymatic serial number" hint="Shown under About Zymatic">
          <input
            maxLength={32}
            value={machine}
            disabled={busy || !!reported}
            onChange={(e) => {
              setMachine(e.target.value)
              setPreview(null)
              setSent(false)
              setError('')
            }}
          />
        </Field>
      )}
      <ErrorBanner message={error || (valid ? menu.error : '')} />
      {!sent && (
        <fieldset disabled={busy}>
          <legend>Saved recipes</legend>
          {recipes.map((r) => (
            <label className="checkbox-row" key={r.id}>
              <input
                type="checkbox"
                checked={chosen.includes(r.id)}
                onChange={() => {
                  setChosen((v) =>
                    v.includes(r.id) ? v.filter((id) => id !== r.id) : [...v, r.id]
                  )
                  setPreview(null)
                }}
              />
              {r.name}
            </label>
          ))}
        </fieldset>
      )}
      {!sent && candidates.some((r) => r.ingredients?.length) && (
        <label className="checkbox-row">
          <input
            type="checkbox"
            checked={manualPaths}
            disabled={busy}
            onChange={(e) => {
              setManualPaths(e.target.checked)
              setPreview(null)
            }}
          />
          Assign flow paths manually
        </label>
      )}
      {!sent &&
        needsPaths.map((r) => (
          <fieldset key={r.id} disabled={busy}>
            <legend>{r.name} · Zymatic flow paths</legend>
            {r.steps.map((s, i) => (
              <Field key={i} label={String(i + 1) + '. ' + s.name}>
                <select
                  value={paths(r)[i]}
                  onChange={(e) => {
                    setRouting({
                      ...routing,
                      [r.id]: paths(r).map((n, j) => (j === i ? Number(e.target.value) : n)),
                    })
                    setPreview(null)
                  }}
                >
                  {[
                    'Choose flow path',
                    'Keg circulation',
                    'Grain compartment',
                    'Hop addition 1',
                    'Hop addition 2',
                    'Hop addition 3',
                    'Hop addition 4',
                  ].map((name, n) => (
                    <option key={n} value={n}>
                      {name}
                    </option>
                  ))}
                </select>
              </Field>
            ))}
          </fieldset>
        ))}
      {sent && (
        <p role="status">Published for {id}. The brewer can now fetch the selected programs.</p>
      )}
      {offline && preview && (
        <p>Choose a program to install during USB setup, or download a copy.</p>
      )}
      {preview?.map((item) => (
        <section key={item.recipe_id}>
          <h4>{item.program.name}</h4>
          <TransferLoadingSheet
            source={selected.find((r) => r.id === item.recipe_id && r.revision === item.revision)}
            batchLiters={item.batch_liters}
            slots={item.hop_slots_by_minute}
          />
          <ProgramTable program={item.program} model="zymatic" archived />
          {offline && (
            <Button onClick={() => setInstallProgram(item.program)}>Use in USB setup</Button>
          )}
          <Button
            variant="secondary"
            onClick={() => {
              const json = JSON.stringify({ model: 'zymatic', ...item.program }, null, 2)
              saveBlob(
                new Blob([json + '\n'], { type: 'application/json' }),
                'zymatic-' + item.program.id + '.json'
              )
            }}
          >
            Download offline program
          </Button>
        </section>
      ))}
      {!offline && valid && menu.data?.machine === id && menu.data.items.length > 0 && (
        <section>
          <h4>Published on this Hub</h4>
          {menu.data.items.map((item) => (
            <p key={item.recipe_id}>
              {item.program.name} · Revision {item.revision}{' '}
              <Button variant="ghost" disabled={busy} onClick={() => void remove(item)}>
                Remove from menu
              </Button>
            </p>
          ))}
        </section>
      )}
    </Modal>
  )
}
