import { StockRecipeTransfer, useStockTargets } from './StockRecipeTransfer'
import { EditionInstructions, RecipeLoadingSheet } from './RecipeLoadingSheet'
import { StockZymaticTransfer, offlineTarget, stockTarget } from './StockZymaticTransfer'
import { useEffect, useRef, useState } from 'react'
import { ArrowUpRight, Send } from 'lucide-react'
import { useHub } from '../context'
import { api, APIError, errorMessage } from '../lib/api'
import { isNative, routeLabel } from '../lib/programs'
import { formatNumber, formatTemperature, formatVolume } from '../lib/recipeDisplay'
import { beerStats } from '../lib/beerCrafter'
import type { Adapted, MachineRecipe, Recipe } from '../types'
import { Badge, Button, ErrorBanner, Field, Modal } from './ui'

export function AdaptDialog({
  recipe,
  onClose,
  initialModel,
}: {
  recipe: Recipe
  onClose: () => void
  initialModel?: string
}) {
  const hub = useHub()
  const stockMachines = useStockTargets()
  const native = isNative(recipe)
  const modelProfile = (model: string) => (model === 'z-series' ? 'z' : model)
  const supported = hub.devices.filter((d) =>
    hub.profiles.some((p) => p.id === modelProfile(d.model))
  )
  const candidates = initialModel
    ? supported.filter((d) => modelProfile(d.model) === initialModel)
    : supported
  const first =
    candidates.find((d) => d.online && modelProfile(d.model) === recipe.source_model) ||
    candidates.find((d) => d.online) ||
    candidates[0]
  const [deviceID, setDeviceID] = useState(
    first?.id || (initialModel === 'zymatic' ? stockTarget : '')
  )
  const target = hub.devices.find((d) => d.id === deviceID)
  const profile = hub.profiles.find((p) => target && p.id === modelProfile(target.model))
  const profileID = profile?.id
  const [previewResult, setPreviewResult] = useState<{
    key: string
    value: Adapted
    existing?: MachineRecipe
  } | null>(null)
  const [sent, setSent] = useState<{ machine: string; path: string; program: string } | null>(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const requestID = useRef(0)
  const previewController = useRef<AbortController | null>(null)
  const needsReady = target?.status?.busy || target?.status?.stopped || !target?.status?.ready
  const inputKey = JSON.stringify({
    recipe: [recipe.id, recipe.revision, recipe.kind],
    deviceID,
    profile,
    machineReady: !!target?.online && !needsReady,
  })
  const latestKey = useRef(inputKey)
  latestKey.current = inputKey
  const adapted = previewResult?.key === inputKey ? previewResult.value : null
  const stats = adapted && !native ? beerStats(adapted.recipe) : null
  const canSend = !!(
    adapted?.can_deploy &&
    adapted.machine_recipe &&
    !adapted.deployment_blockers?.length &&
    target?.online &&
    !needsReady &&
    modelProfile(target.model) === profileID &&
    !busy
  )

  function invalidatePreview() {
    requestID.current++
    previewController.current?.abort()
    previewController.current = null
    setPreviewResult(null)
    setError('')
    setBusy((current) => (current === 'preview' ? '' : current))
  }

  useEffect(() => {
    invalidatePreview()
    return () => {
      requestID.current++
      previewController.current?.abort()
    }
  }, [inputKey])

  async function preview() {
    if (!profile || busy) return
    const key = inputKey
    const id = ++requestID.current
    const controller = new AbortController()
    previewController.current?.abort()
    previewController.current = controller
    setBusy('preview')
    setError('')
    setPreviewResult(null)
    try {
      const result = await api<Adapted>(`/recipes/${recipe.id}/adapt`, {
        method: 'POST',
        body: { profile_id: profileID, batch_liters: 0 },
        signal: controller.signal,
      })
      if (id !== requestID.current || controller.signal.aborted) return
      let existing: MachineRecipe | undefined
      if (result.can_deploy && result.machine_recipe && target?.online && !needsReady) {
        const inventory = await api<{ recipes: MachineRecipe[] | null }>(
          `/devices/${deviceID}/recipes`,
          { signal: controller.signal }
        )
        existing = (inventory.recipes ?? []).find((item) => item.id === result.machine_recipe!.id)
      }
      if (id === requestID.current && key === latestKey.current && !controller.signal.aborted)
        setPreviewResult({ key, value: result, existing })
    } catch (error) {
      if (id === requestID.current && key === latestKey.current && !controller.signal.aborted)
        setError(errorMessage(error))
    } finally {
      if (id === requestID.current) {
        previewController.current = null
        setBusy('')
      }
    }
  }

  async function deploy() {
    if (!adapted || !canSend || previewResult?.key !== latestKey.current) return
    setBusy('deploy')
    setError('')
    try {
      const receipt = await api<{ device_id: string; recipe: { id: string; name: string } }>(
        `/devices/${deviceID}/deploy`,
        {
          method: 'POST',
          body: {
            recipe_id: recipe.id,
            profile_id: adapted.profile.id,
            batch_liters: 0,
            source_revision: adapted.source_revision,
            profile_fingerprint: adapted.profile_fingerprint,
            ...(previewResult.existing && { replace_expected: previewResult.existing }),
          },
        }
      )
      setSent({
        machine: target!.name,
        program: receipt.recipe.name,
        path: `/devices/${receipt.device_id}?recipe=${encodeURIComponent(receipt.recipe.id)}`,
      })
    } catch (error) {
      setError(errorMessage(error))
      if (
        error instanceof APIError &&
        [
          'profile_changed',
          'revision_conflict',
          'recipe_changed',
          'recipe_exists',
          'device_recipe_exists',
        ].includes(error.code)
      ) {
        setPreviewResult(null)
        void hub.refresh()
      }
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
        recipes={[recipe]}
        initialSelection={[recipe.id]}
        onClose={onClose}
        back={() => setDeviceID('')}
      />
    )
  const pairedZymatic =
    target?.adapter === 'pico-standalone' && target.model === 'zymatic' ? target : undefined
  if ([stockTarget, offlineTarget].includes(deviceID) || pairedZymatic)
    return (
      <StockZymaticTransfer
        offline={deviceID === offlineTarget}
        key={deviceID + (pairedZymatic?.status?.stock_serial || '')}
        paired={
          pairedZymatic && { name: pairedZymatic.name, serial: pairedZymatic.status?.stock_serial }
        }
        recipes={[recipe]}
        initialSelection={[recipe.id]}
        back={() => setDeviceID('')}
        close={onClose}
      />
    )
  if (sent)
    return (
      <Modal
        title="Recipe sent"
        onClose={onClose}
        actions={
          <>
            <Button variant="secondary" onClick={onClose}>
              Done
            </Button>
            <Button
              onClick={() => {
                onClose()
                hub.navigate(sent.path)
              }}
            >
              Open {sent.machine}
              <ArrowUpRight size={16} />
            </Button>
          </>
        }
      >
        <p>
          <strong>{sent.program}</strong> is saved on {sent.machine}. Open the brewer to review the
          selection and start when ready.
        </p>
      </Modal>
    )
  return (
    <Modal
      title="Send recipe"
      onClose={onClose}
      wide
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy === 'deploy'}>
            Cancel
          </Button>
          {adapted ? (
            <Button disabled={!canSend} onClick={() => void deploy()}>
              <Send size={16} />
              {busy === 'deploy'
                ? 'Sending…'
                : previewResult?.existing
                  ? 'Replace on machine'
                  : 'Send to machine'}
            </Button>
          ) : (
            <Button disabled={!profile || !!busy} onClick={() => void preview()}>
              {busy === 'preview' ? 'Adapting…' : 'Preview recipe'}
              <ArrowUpRight size={16} />
            </Button>
          )}
        </>
      }
    >
      <p>
        {native
          ? 'The original PicoPak schedule is sent as recorded.'
          : 'Ingredients scale together to fit the selected brewer’s grain, hop and starting-water limits. The preview shows quantities and estimated yield.'}
      </p>
      {adapted && previewResult?.existing && (
        <p className="notice">
          This will replace “{previewResult.existing.name}” on {target?.name}.
        </p>
      )}
      <Field label="Target machine">
        <select
          value={deviceID}
          disabled={busy === 'deploy'}
          onChange={(event) => {
            invalidatePreview()
            setDeviceID(event.target.value)
          }}
        >
          <option value="" disabled>
            Choose a machine
          </option>
          <option value={stockTarget}>Stock Zymatic · sync menu</option>
          <option value={offlineTarget}>Zymatic · offline program for USB setup</option>
          {stockMachines.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name} · Original firmware
            </option>
          ))}
          {hub.devices.map((device) => (
            <option
              key={device.id}
              value={device.id}
              disabled={!supported.some((d) => d.id === device.id)}
            >
              {device.name}
              {!device.model
                ? ' (model not identified)'
                : !supported.some((d) => d.id === device.id)
                  ? ' (unsupported model)'
                  : device.online
                    ? ''
                    : ' (offline)'}
            </option>
          ))}
        </select>
      </Field>
      <ErrorBanner message={error} />
      {adapted && (
        <>
          <div className="adapt-summary">
            <Badge tone={adapted.can_deploy ? 'green' : 'amber'}>
              {adapted.can_deploy ? 'Machine recipe ready' : 'Planning preview'}
            </Badge>
            <strong>
              {native
                ? 'Exact native schedule'
                : `Estimated yield ${formatVolume(adapted.recipe.batch_liters)} · ${formatNumber(adapted.recipe.efficiency)}% efficiency`}
            </strong>
          </div>
          {stats && (stats.og !== null || stats.ibu !== null) && (
            <p>
              {[
                stats.og !== null && 'Estimated original gravity ' + stats.og.toFixed(3),
                stats.ibu !== null &&
                  'Estimated boil bitterness ' + formatNumber(stats.ibu, 1) + ' IBU',
              ]
                .filter(Boolean)
                .join(' · ')}
            </p>
          )}
          {!!adapted.deployment_blockers?.length && (
            <section aria-label="Deployment blockers">
              <h3>Deployment blocked</h3>
              <ul>
                {adapted.deployment_blockers.map((reason, index) => (
                  <li key={index}>{reason}</li>
                ))}
              </ul>
            </section>
          )}
          {(adapted.warnings || []).map((warning, index) => (
            <p className="notice" key={index}>
              {warning}
            </p>
          ))}
          {target && !target.online && (
            <p className="notice">{target.name} is offline. Reconnect it to send this recipe.</p>
          )}
          {needsReady && target?.online && (
            <p className="notice">
              {target.name} must be idle and ready at its main menu before deployment.
            </p>
          )}
          {!native && (
            <>
              {!!adapted.recipe.ingredients?.length && (
                <EditionInstructions notes={adapted.recipe.notes} />
              )}
              <h3>Scaled ingredients</h3>
              <RecipeLoadingSheet
                recipe={adapted.recipe}
                slots={adapted.hop_slots_by_minute}
                original={recipe.ingredients}
              />
            </>
          )}
          {adapted.machine_recipe || (native && adapted.recipe.native_program) ? (
            <>
              <h3>{adapted.machine_recipe ? 'Machine schedule' : 'Source program schedule'}</h3>
              <div className="table-scroll">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Step</th>
                      <th>Target</th>
                      <th>Time</th>
                      <th>Drain</th>
                      <th>Flow path</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(adapted.machine_recipe || adapted.recipe.native_program)!.steps.map(
                      (step, index) => (
                        <tr key={index}>
                          <td>{step.name}</td>
                          <td>{step.target_f ? `${step.target_f}°F` : 'Unheated'}</td>
                          <td>{step.minutes} min</td>
                          <td>{step.drain_minutes} min</td>
                          <td>
                            {adapted.machine_recipe
                              ? routeLabel(
                                  step.location,
                                  adapted.profile.id,
                                  adapted.profile.id === 'zymatic'
                                )
                              : routeLabel(step.location, adapted.recipe.source_model, true)}
                          </td>
                        </tr>
                      )
                    )}
                  </tbody>
                </table>
              </div>
            </>
          ) : (
            <>
              <h3>Recipe schedule</h3>
              <p>
                This schedule remains a recipe plan. Resolve the deployment blockers before sending
                it to a machine.
              </p>
              <div className="table-scroll">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Step</th>
                      <th>Target</th>
                      <th>Time</th>
                      <th>Drain</th>
                      <th>Flow path</th>
                      <th>Instructions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {adapted.recipe.steps.map((step, index) => (
                      <tr key={index}>
                        <td>{step.name}</td>
                        <td>{step.target_c ? formatTemperature(step.target_c) : 'Unheated'}</td>
                        <td>{step.minutes} min</td>
                        <td>{step.drain_minutes} min</td>
                        <td>
                          {routeLabel(
                            step.location,
                            adapted.profile.id,
                            adapted.profile.id === 'zymatic'
                          )}
                        </td>
                        <td>
                          {[
                            step.type || step.kind,
                            step.infuse_liters
                              ? `${formatVolume(step.infuse_liters)} infusion`
                              : '',
                            step.ramp_minutes ? `${formatNumber(step.ramp_minutes)} min ramp` : '',
                          ]
                            .filter(Boolean)
                            .join(' · ')}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <button
            className="text-button"
            disabled={!!busy}
            onClick={() => {
              hub.setDraft({
                ...adapted.recipe,
                id: '',
                revision: 0,
                name: native ? recipe.name : `${recipe.name} · ${adapted.profile.name}`,
              })
              onClose()
              hub.navigate('/recipes/new')
            }}
          >
            Save this adaptation as a separate recipe <ArrowUpRight size={14} />
          </button>
        </>
      )}
    </Modal>
  )
}
