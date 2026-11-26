import { StockRecipeTransfer, useStockTargets } from './StockRecipeTransfer'
import { StockZymaticTransfer, offlineTarget, stockTarget } from './StockZymaticTransfer'
import { useState } from 'react'
import { ArrowRight, Check, RefreshCw, Send } from 'lucide-react'
import { useHub } from '../context'
import { api, errorMessage } from '../lib/api'
import { formatVolume } from '../lib/recipeDisplay'
import { supports } from '../lib/capabilities'
import { isNative, kindLabel } from '../lib/programs'
import type { BulkDeployment, Device, Recipe } from '../types'
import { Badge, Button, Empty, ErrorBanner, Field, Modal } from './ui'
import { ProgramTable } from './Program'
import { TransferLoadingSheet } from './RecipeLoadingSheet'

const receiptKey = (deviceID: string) => `hub-last-transfer-${deviceID}`
function savedReceipt(deviceID: string) {
  try {
    return localStorage.getItem(receiptKey(deviceID)) || ''
  } catch {
    return ''
  }
}
const statuses: Record<string, string> = {
  ready: 'Ready to send',
  skipped: 'Skipped',
  verified: 'Verified on machine',
  unconfirmed: 'Write not confirmed',
  not_sent: 'Not sent',
  sending: 'Write in progress',
}

export function TransferDialog({
  recipes,
  initialDeviceID,
  initialSelection,
  onClose,
  onComplete,
}: {
  recipes: Recipe[]
  initialDeviceID?: string
  initialSelection?: string[]
  onClose: () => void
  onComplete?: () => void
}) {
  const hub = useHub()
  const stockMachines = useStockTargets()
  const [selection, setSelection] = useState(initialSelection || [])
  const chosen = recipes.filter((recipe) => selection.includes(recipe.id))
  const requiresZ = chosen.some((r) => !!r.native_program?.z_program)
  const pairedMenu = (d: Device) =>
    d.adapter === 'pico-standalone' && d.model === 'zymatic' && !requiresZ
  const canReceive = (d: Device) =>
    pairedMenu(d) ||
    (supports(d.capabilities, 'recipes') &&
      (!requiresZ || (d.model === 'z-series' && supports(d.capabilities, 'z_program_maker'))))
  const [deviceID, setDeviceID] = useState(
    initialDeviceID ||
      hub.devices.find(
        (device) =>
          canReceive(device) &&
          device.online &&
          device.status?.ready &&
          !device.status?.busy &&
          !device.status?.stopped
      )?.id ||
      ''
  )
  const [batches, setBatches] = useState<Record<string, string>>({})
  const [overwrite, setOverwrite] = useState(false)
  const [plan, setPlan] = useState<BulkDeployment | null>(null)
  const [attempted, setAttempted] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [readingReceipt, setReadingReceipt] = useState(false)
  const target = hub.devices.find((device) => device.id === deviceID && canReceive(device))
  const ready =
    supports(target?.capabilities, 'recipes') &&
    target?.online &&
    target.status?.ready &&
    !target.status.busy &&
    !target.status.stopped
  const visible = recipes.filter((recipe) =>
    `${recipe.name} ${kindLabel(recipe.kind, recipe.source_model)}`
      .toLowerCase()
      .includes(query.toLowerCase())
  )
  const lastReceipt = savedReceipt(deviceID)
  async function preview() {
    if (!target) return
    setBusy('preview')
    setError('')
    setPlan(null)
    setAttempted(false)
    setReadingReceipt(false)
    try {
      const preview = await api<BulkDeployment>(`/devices/${deviceID}/deployments`, {
        method: 'POST',
        body: {
          profile_id: target.model,
          overwrite_existing: overwrite,
          items: chosen.map((recipe) => ({
            recipe_id: recipe.id,
            source_revision: recipe.revision,
            batch_liters: isNative(recipe) ? 0 : Number(batches[recipe.id] || 0),
          })),
        },
      })
      setPlan(preview)
      try {
        localStorage.setItem(receiptKey(deviceID), preview.id)
      } catch {}
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy('')
    }
  }
  async function refreshReceipt(id = plan?.id || lastReceipt) {
    if (!id) return
    setBusy('receipt')
    setError('')
    try {
      const receipt = await api<BulkDeployment>(
        `/devices/${deviceID}/deployments/${encodeURIComponent(id)}`
      )
      setPlan(receipt)
      setReadingReceipt(true)
      setAttempted(true)
      onComplete?.()
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy('')
    }
  }
  async function send() {
    if (!plan || attempted) return
    setBusy('send')
    setError('')
    setAttempted(true)
    try {
      const receipt = await api<BulkDeployment>(
        `/devices/${deviceID}/deployments/${encodeURIComponent(plan.id)}/execute`,
        { method: 'POST', body: {} }
      )
      setPlan(receipt)
      await hub.refresh()
      onComplete?.()
      hub.notify(
        `${receipt.verified_count} program${receipt.verified_count === 1 ? '' : 's'} verified on ${target?.name}.`,
        receipt.state === 'complete' ? 'success' : 'error'
      )
    } catch (error) {
      setError(
        `${errorMessage(error)} Read the saved receipt and refresh the machine inventory before preparing another transfer.`
      )
      try {
        setPlan(
          await api<BulkDeployment>(
            `/devices/${deviceID}/deployments/${encodeURIComponent(plan.id)}`
          )
        )
      } catch {}
      onComplete?.()
    } finally {
      setBusy('')
    }
  }
  const stockMachine = stockMachines.find((m) => m.id === deviceID)
  if (stockMachine)
    return (
      <StockRecipeTransfer
        key={stockMachine.id}
        target={stockMachine}
        recipes={recipes}
        initialSelection={selection}
        onClose={onClose}
        back={() => setDeviceID('')}
      />
    )
  const pairedZymatic = target && pairedMenu(target) ? target : undefined
  if (([stockTarget, offlineTarget].includes(deviceID) && !requiresZ) || pairedZymatic)
    return (
      <StockZymaticTransfer
        offline={deviceID === offlineTarget}
        key={deviceID + (pairedZymatic?.status?.stock_serial || '')}
        paired={
          pairedZymatic && { name: pairedZymatic.name, serial: pairedZymatic.status?.stock_serial }
        }
        recipes={recipes}
        initialSelection={selection}
        back={() => setDeviceID('')}
        close={onClose}
      />
    )
  return (
    <Modal
      title={
        plan ? (attempted ? 'Transfer receipt' : 'Review this transfer') : 'Send recipes & programs'
      }
      wide
      onClose={() => {
        if (!busy) onClose()
      }}
      actions={
        <>
          <Button variant="secondary" disabled={!!busy} onClick={onClose}>
            {attempted ? 'Done' : 'Cancel'}
          </Button>
          {plan ? (
            <>
              <Button
                variant="secondary"
                disabled={!!busy}
                onClick={() => {
                  setPlan(null)
                  setAttempted(false)
                  setReadingReceipt(false)
                  setError('')
                }}
              >
                New preview
              </Button>
              {attempted ? (
                <Button variant="secondary" disabled={!!busy} onClick={() => void refreshReceipt()}>
                  <RefreshCw size={15} />
                  {busy === 'receipt' ? 'Reading…' : 'Refresh receipt'}
                </Button>
              ) : (
                <Button
                  disabled={!!busy || !ready || !plan.planned_count}
                  onClick={() => void send()}
                >
                  <Send size={16} />
                  {busy === 'send'
                    ? 'Sending…'
                    : `Send ${plan.planned_count} program${plan.planned_count === 1 ? '' : 's'}`}
                </Button>
              )}
            </>
          ) : (
            <Button
              disabled={
                !ready ||
                !chosen.length ||
                !!busy ||
                chosen.some((r) => batches[r.id] && !(Number(batches[r.id]) > 0))
              }
              onClick={() => void preview()}
            >
              {busy === 'preview' ? 'Checking machine…' : 'Preview transfer'}
              <ArrowRight size={16} />
            </Button>
          )}
        </>
      }
    >
      <p>
        Send saved recipes to the machine’s local catalog. Programs remain available from its front
        panel and browser interface.
      </p>
      <ErrorBanner message={error} />
      {!plan ? (
        <>
          <Field label="Transfer to machine">
            <select
              disabled={!!busy}
              value={target?.id || ''}
              onChange={(event) => setDeviceID(event.target.value)}
            >
              <option value="">Choose a machine</option>
              {!requiresZ && (
                <>
                  <option value={stockTarget}>Stock Zymatic · sync menu</option>
                  <option value={offlineTarget}>Zymatic · offline program for USB setup</option>
                </>
              )}
              {stockMachines
                .filter((m) => !requiresZ || m.model === 'z-series')
                .map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name} · Original firmware
                  </option>
                ))}
              {hub.devices.filter(canReceive).map((device) => (
                <option key={device.id} value={device.id}>
                  {device.name}
                  {device.online ? '' : ' (offline)'}
                </option>
              ))}
            </select>
          </Field>
          {requiresZ && !hub.devices.some(canReceive) && (
            <p className="notice">
              This program requires a Z that reports Z Program Maker support. For an original Z with
              Program Maker, export the program JSON and import it on the brewer.
            </p>
          )}
          {target && !ready && (
            <p className="notice">
              {target.name} must be online and idle at its ready main menu before a transfer can be
              prepared.
            </p>
          )}
          <label className="checkbox-row">
            <input
              type="checkbox"
              checked={overwrite}
              disabled={!!busy}
              onChange={(event) => setOverwrite(event.target.checked)}
            />
            <span>
              Update programs with matching machine recipe IDs
              <small>
                Existing entries are preserved by default. The preview identifies every proposed
                update.
              </small>
            </span>
          </label>
          <Field label="Filter library selection">
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search recipes or cleaning programs"
            />
          </Field>
          <div className="transfer-selection-actions">
            <span>{chosen.length} selected</span>
            <Button
              variant="ghost"
              disabled={!!busy || !visible.length}
              onClick={() =>
                setSelection([...new Set([...selection, ...visible.map((recipe) => recipe.id)])])
              }
            >
              Select visible
            </Button>
            <Button
              variant="ghost"
              disabled={!!busy || !selection.length}
              onClick={() => setSelection([])}
            >
              Clear selection
            </Button>
          </div>
          {recipes.length ? (
            <div className="transfer-selection">
              {visible.map((recipe) => (
                <div className="transfer-choice" key={recipe.id}>
                  <label>
                    <input
                      type="checkbox"
                      disabled={!!busy}
                      checked={selection.includes(recipe.id)}
                      onChange={(event) =>
                        setSelection((values) =>
                          event.target.checked
                            ? [...values, recipe.id]
                            : values.filter((id) => id !== recipe.id)
                        )
                      }
                    />
                    <span>
                      <strong>{recipe.name}</strong>
                      <small>
                        {recipe.native_program?.z_program
                          ? 'Z Program Maker'
                          : kindLabel(recipe.kind, recipe.source_model)}{' '}
                        · revision {recipe.revision}
                        {isNative(recipe) ? ' · exact schedule' : ''}
                      </small>
                    </span>
                  </label>
                  {!isNative(recipe) && selection.includes(recipe.id) && (
                    <Field label={`${recipe.name} batch (L)`}>
                      <input
                        type="number"
                        min="0"
                        step="any"
                        disabled={!!busy}
                        placeholder="Automatic"
                        value={batches[recipe.id] ?? ''}
                        onChange={(event) =>
                          setBatches((values) => ({
                            ...values,
                            [recipe.id]: event.target.value,
                          }))
                        }
                      />
                    </Field>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <Empty
              title="Your library has no recipes yet."
              description="Copy a community recipe or program into your library, then return here to send it."
              action={
                <Button
                  variant="secondary"
                  onClick={() => {
                    onClose()
                    hub.navigate('/catalog')
                  }}
                >
                  Browse community catalog
                </Button>
              }
            />
          )}
          {lastReceipt && (
            <Button
              className="receipt-link"
              variant="ghost"
              disabled={!!busy}
              onClick={() => void refreshReceipt(lastReceipt)}
            >
              Read the last transfer receipt for this machine
            </Button>
          )}
        </>
      ) : (
        <>
          <div className="transfer-summary">
            <div>
              <span>Target</span>
              <strong>{target?.name || plan.device_id}</strong>
            </div>
            <div>
              <span>Slots at preview</span>
              <strong>
                {plan.used} / {plan.capacity}
              </strong>
            </div>
            <div>
              <span>New programs</span>
              <strong>{plan.new_count}</strong>
            </div>
            <div>
              <span>Updates</span>
              <strong>{plan.update_count}</strong>
            </div>
          </div>
          {attempted ? (
            <div className="notice">
              <strong>
                {plan.state === 'complete'
                  ? 'Transfer complete.'
                  : plan.state === 'planned'
                    ? readingReceipt
                      ? 'Saved preview. Prepare a fresh preview before sending.'
                      : 'The transfer has not been confirmed.'
                    : plan.state === 'running'
                      ? 'A transfer was started; its final result is not yet available.'
                      : 'Transfer stopped before every program was verified.'}
              </strong>{' '}
              {plan.verified_count} of {plan.planned_count} planned programs verified. No brew was
              started.
            </div>
          ) : (
            <p className="notice">
              {plan.available} free slots before this transfer. {plan.new_count} will be used.
              Review the schedules and skipped entries below, then send when ready.
            </p>
          )}
          <div className="transfer-results">
            {plan.items.map((item, index) => (
              <article className="transfer-result" key={`${item.recipe_id}-${index}`}>
                <header>
                  <div>
                    <strong>{item.name || item.recipe_id}</strong>
                    <small>
                      {item.native?.id || item.recipe_id}
                      {item.native && item.batch_liters > 0
                        ? ' · Estimated yield ' + formatVolume(item.batch_liters)
                        : ''}
                      {item.update ? ' · updates an existing slot' : ''}
                    </small>
                  </div>
                  <Badge
                    tone={
                      item.status === 'verified'
                        ? 'green'
                        : item.status === 'ready'
                          ? 'copper'
                          : item.status === 'skipped'
                            ? 'neutral'
                            : 'amber'
                    }
                  >
                    {statuses[item.status] || item.status}
                  </Badge>
                </header>
                {item.native && (
                  <TransferLoadingSheet
                    source={recipes.find(
                      (r) => r.id === item.recipe_id && r.revision === item.source_revision
                    )}
                    batchLiters={item.batch_liters}
                    slots={item.hop_slots_by_minute}
                  />
                )}
                {item.reason && <p>{item.reason}</p>}
                {!!item.warnings?.length && (
                  <ul className="warning-list">
                    {item.warnings.map((warning, index) => (
                      <li key={index}>{warning}</li>
                    ))}
                  </ul>
                )}
                {item.native && (
                  <details>
                    <summary>
                      {item.status === 'ready' ? 'Review machine schedule' : 'Machine schedule'}
                    </summary>
                    <ProgramTable program={item.native} model={plan.profile_id} />
                    {item.previous && (
                      <details>
                        <summary>Previous machine schedule</summary>
                        <ProgramTable program={item.previous} model={plan.profile_id} />
                      </details>
                    )}
                  </details>
                )}
              </article>
            ))}
          </div>
          {plan.state === 'complete' && (
            <p className="transfer-complete">
              <Check size={18} />
              The machine keeps these programs until you edit or remove them.
            </p>
          )}
        </>
      )}
    </Modal>
  )
}
