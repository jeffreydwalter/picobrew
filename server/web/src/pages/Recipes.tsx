import { RecipeStyleField } from '../components/RecipeStyleField'
import {
  MeasureChoice,
  QuantityField,
  RecipeMeasures,
  useRecipeQuantity,
} from '../components/RecipeMeasures'
import { FermentationPlan, ConditioningFields } from '../components/FermentationPlan'
import { BeerCrafter, ReferenceLists } from '../components/BeerCrafter'
import { RecipeWizard } from '../components/RecipeWizard'
import { unmeasuredYeast } from '../lib/beerCrafter'
import { RecipeIngredients, RecipeSchedule } from '../components/RecipePlan'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  Plus,
  ChevronDown,
  Upload,
  Download,
  ArrowUpRight,
  Trash2,
  Send,
  Save,
  Wheat,
  ShoppingBasket,
  History,
  Copy,
  Sparkles,
  Check,
  Loader2,
} from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, download, errorMessage } from '../lib/api'
import { dateLabel, modelLabel, newRecipe, slug } from '../lib/utils'
import { isNative, kindLabel, programTiming, recipeSavePath } from '../lib/programs'
import { formatQuantity, formatVolume } from '../lib/recipeDisplay'
import { BrewerPlan } from '../components/MachineVersions'
import { OriginalRecipeLink } from '../components/OriginalRecipe'
import NativeRecipeEditor from './NativeRecipeEditor'
import ZProgramEditor from './ZProgramEditor'
import { newZRecipe } from '../lib/zProgram'
import { TransferDialog } from '../components/TransferDialog'
import { AdaptDialog } from '../components/AdaptDialog'
import type { Recipe, Ingredient } from '../types'
import {
  Button,
  Confirm,
  Empty,
  ErrorBanner,
  Field,
  Loading,
  Modal,
  PageTitle,
  SearchBox,
  SectionHeading,
} from '../components/ui'
export default function Recipes({ id }: { id?: string }) {
  const query = new URLSearchParams(location.hash.split('?')[1])
  if (id === 'new' && query.get('mode') === 'program')
    return (
      <NewMachineProgram
        key={query.toString()}
        model={query.get('model') || ''}
        name={query.get('name') || ''}
      />
    )
  return id ? (
    <RecipeMeasures>
      <RecipeEditor id={id} />
    </RecipeMeasures>
  ) : (
    <RecipeLibrary />
  )
}
function ZEditor({ initial }: { initial: Recipe }) {
  const [importing, setImporting] = useState(false)
  return (
    <>
      <ZProgramEditor initial={initial} onImport={() => setImporting(true)} />
      {importing && <ImportDialog initialFormat="json" onClose={() => setImporting(false)} />}
    </>
  )
}

function NewMachineProgram({ model, name }: { model: string; name: string }) {
  const hub = useHub()
  const models = ['pico-s', 'pico-pro', 'pico-c', 'z', 'zymatic']
  const [chosen, setChosen] = useState('')
  if (!models.includes(model))
    return (
      <>
        <PageTitle title="Create a machine program" back={() => hub.navigate('/recipes')} />
        <section className="panel">
          <Field label="Brewer">
            <select value={chosen} onChange={(e) => setChosen(e.target.value)}>
              <option value="">Choose a brewer</option>
              {models.map((id) => (
                <option key={id} value={id}>
                  {modelLabel(id)}
                </option>
              ))}
            </select>
          </Field>
          <Button
            disabled={!chosen}
            onClick={() => hub.navigate('/recipes/new?mode=program&model=' + chosen)}
          >
            Continue
          </Button>
        </section>
      </>
    )
  if (model === 'z') return <ZEditor initial={newZRecipe(name)} />
  const program = {
    ...newRecipe(),
    kind: 'native-program',
    source_model: model,
    name,
    batch_liters: 0,
    efficiency: 0,
    steps: [],
    native_program: {
      id: '',
      name,
      steps: [
        {
          name: 'Mash',
          location: model === 'zymatic' ? 1 : 2,
          target_f: 152,
          minutes: 60,
          drain_minutes: 2,
        },
      ],
    },
  }
  return <NativeRecipeEditor initial={program} isNew />
}

function AddRecipeMenu({ onImport }: { onImport: () => void }) {
  const details = useRef<HTMLDetailsElement>(null)
  const trigger = useRef<HTMLElement>(null)
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (!open) return
    function dismiss(event: PointerEvent) {
      if (details.current && !details.current.contains(event.target as Node)) {
        details.current.open = false
      }
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open])
  return (
    <details
      ref={details}
      className="add-recipe-menu"
      onToggle={(event) => setOpen(event.currentTarget.open)}
      onBlur={(event) => {
        if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget))
          event.currentTarget.open = false
      }}
      onKeyDown={(event) => {
        if (event.key === 'Escape' && event.currentTarget.open) {
          event.preventDefault()
          event.currentTarget.open = false
          trigger.current?.focus()
        }
      }}
    >
      <summary ref={trigger} className="button primary">
        <Plus size={16} /> Add recipe <ChevronDown size={16} />
      </summary>
      <div className="recipe-options">
        <a href="#/recipes/new?mode=batch">
          <Plus size={16} />
          Create by batch size
        </a>
        <a href="#/recipes/new">
          <Plus size={16} />
          Create for a brewer
        </a>
        <a href="#/recipes/new?mode=program">
          <Plus size={16} />
          Create a machine program
        </a>
        <a href="#/recipes/create-with-ai">
          <Sparkles size={16} />
          Ask the brew assistant
        </a>
        <button
          onClick={() => {
            if (details.current) details.current.open = false
            trigger.current?.focus()
            onImport()
          }}
        >
          <Upload size={16} />
          Import recipe
        </button>
      </div>
    </details>
  )
}
function RecipeLibrary() {
  const hub = useHub()
  const [query, setQuery] = useState('')
  const [importing, setImporting] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [shopping, setShopping] = useState(false)
  const [transfer, setTransfer] = useState(false)
  const emptyLibrary = hub.recipes.length === 0
  const shoppingIDs = selected.filter((id) =>
    hub.recipes.some((recipe) => recipe.id === id && !isNative(recipe))
  )
  const filtered = hub.recipes.filter((recipe) =>
    `${recipe.name} ${recipe.style} ${(recipe.tags || []).join(' ')}`
      .toLowerCase()
      .includes(query.toLowerCase())
  )
  return (
    <>
      <PageTitle
        title="Recipe library"
        actions={!emptyLibrary && <AddRecipeMenu onImport={() => setImporting(true)} />}
      />
      <div className="list-toolbar">
        <SearchBox value={query} onChange={setQuery} placeholder="Search recipes, styles or tags" />
        <div className="inline-actions">
          <span className="muted">{filtered.length} recipes & programs</span>
          {selected.length > 0 && (
            <>
              <Button variant="secondary" onClick={() => setTransfer(true)}>
                <Send size={16} /> Send selected ({selected.length})
              </Button>
              {shoppingIDs.length > 0 && (
                <Button variant="secondary" onClick={() => setShopping(true)}>
                  <ShoppingBasket size={16} /> Shopping list ({shoppingIDs.length})
                </Button>
              )}
            </>
          )}
        </div>
      </div>
      {filtered.length ? (
        <ul className="library-list" aria-label="Saved recipes and programs">
          {filtered.map((recipe) => {
            const native = isNative(recipe)
            const timing = native
              ? recipe.native_program
                ? programTiming(recipe.native_program, recipe.source_model)
                : 'Schedule unavailable'
              : `${(recipe.steps || []).reduce((sum, step) => sum + step.minutes + step.drain_minutes, 0)} min`
            return (
              <li key={recipe.id}>
                <input
                  type="checkbox"
                  aria-label={`Select ${recipe.name}`}
                  checked={selected.includes(recipe.id)}
                  onChange={(event) =>
                    setSelected((values) =>
                      event.target.checked
                        ? [...values, recipe.id]
                        : values.filter((id) => id !== recipe.id)
                    )
                  }
                />
                <a href={`#/recipes/${recipe.id}`}>
                  <span className="library-name">
                    <strong>{recipe.name}</strong>
                    <small>
                      {native
                        ? [
                            recipe.native_program?.z_program
                              ? 'Z Program Maker'
                              : kindLabel(recipe.kind, recipe.source_model),
                            recipe.source_model && modelLabel(recipe.source_model),
                          ]
                            .filter(Boolean)
                            .join(' · ')
                        : recipe.style || kindLabel(recipe.kind, recipe.source_model)}
                    </small>
                  </span>
                  <span className="library-fact">
                    <small>Batch</small>
                    {native ? 'Program only' : formatVolume(recipe.batch_liters)}
                  </span>
                  <span className="library-fact">
                    <small>Schedule</small>
                    {timing}
                  </span>
                  <ArrowUpRight size={16} aria-hidden="true" />
                </a>
              </li>
            )
          })}
        </ul>
      ) : (
        <div className="panel">
          <Empty
            icon={<Wheat size={28} />}
            title={
              emptyLibrary ? 'Great brews start with an idea.' : 'No recipes match that search.'
            }
            description={
              emptyLibrary
                ? 'Create your first recipe, import BeerXML, or ask the brew assistant for a starting point.'
                : 'Try a different name or style.'
            }
            action={emptyLibrary && <AddRecipeMenu onImport={() => setImporting(true)} />}
          />
        </div>
      )}
      {importing && <ImportDialog onClose={() => setImporting(false)} />}{' '}
      {shopping && <ShoppingList ids={shoppingIDs} onClose={() => setShopping(false)} />}
      {transfer && (
        <TransferDialog
          recipes={hub.recipes}
          initialSelection={selected}
          onClose={() => setTransfer(false)}
        />
      )}
    </>
  )
}
function metadata(recipe: Recipe) {
  const {
    id,
    name,
    style,
    description,
    batch_liters,
    estimated_yield,
    source_yeast,
    efficiency,
    ingredients,
    steps,
    tags,
    notes,
    revision,
    created_at,
    updated_at,
    type,
    boil_liters,
    boil_minutes,
    water,
    fermentation,
    carbonation,
    machine_versions,
    ...extras
  } = recipe as Recipe & Record<string, unknown>
  return extras
}
function RecipeEditor({ id }: { id: string }) {
  const quantity = useRecipeQuantity()
  const hub = useHub()
  const isNew = id === 'new'
  const loaded = useResource<Recipe>(isNew ? null : `/recipes/${id}`)
  const generic = isNew && new URLSearchParams(location.hash.split('?')[1]).get('mode') === 'batch'
  const [recipe, setRecipe] = useState<Recipe>(() => {
    if (isNew && hub.draft) return { ...hub.draft, id: '', revision: 0 }
    const base = newRecipe()
    if (generic) {
      base.boil_minutes = 60
      base.steps.push({
        name: 'Boil',
        kind: 'boil',
        target_c: 100,
        minutes: 60,
        location: 0,
        drain_minutes: 0,
      })
    }
    return base
  })
  const [guided, setGuided] = useState(isNew && !hub.draft && !generic)
  const [viewModel, setViewModel] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [dirty, setDirty] = useState(isNew)
  const [tagsInput, setTagsInput] = useState(isNew ? (hub.draft?.tags || []).join(', ') : '')
  const [deploy, setDeploy] = useState(false)
  const [remove, setRemove] = useState(false)
  const [revisions, setRevisions] = useState(false)
  const [advanced, setAdvanced] = useState<string | null>(null)
  const originalRevision = useRef(0)
  useEffect(() => {
    if (loaded.data) {
      setRecipe({
        ...loaded.data,
        ingredients: loaded.data.ingredients || [],
        steps: loaded.data.steps || [],
        tags: loaded.data.tags || [],
      })
      setAdvanced(null)
      originalRevision.current = loaded.data.revision
      setTagsInput((loaded.data.tags || []).join(', '))
      setDirty(false)
    }
  }, [loaded.data])
  useEffect(() => {
    if (isNew) hub.setDraft(null)
  }, [])
  useEffect(() => {
    const listener = (event: BeforeUnloadEvent) => {
      if (dirty) {
        event.preventDefault()
        event.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', listener)
    return () => window.removeEventListener('beforeunload', listener)
  }, [dirty])
  function update(patch: Partial<Recipe>) {
    setRecipe((value) => ({ ...value, ...patch }))
    setDirty(true)
  }
  async function save(event?: FormEvent, draft: Recipe = recipe) {
    event?.preventDefault()
    setBusy(true)
    setError('')
    try {
      let payload = {
        ...draft,
        tags: tagsInput
          .split(',')
          .map((tag) => tag.trim())
          .filter(Boolean),
      }
      if (advanced?.trim()) {
        const extras = JSON.parse(advanced)
        if (!extras || Array.isArray(extras) || typeof extras !== 'object')
          throw new Error('Advanced metadata must be a JSON object.')
        payload = { ...payload, ...extras, id: recipe.id, revision: recipe.revision }
      }
      const targetModel =
        !draft.provenance &&
        ['pico-s', 'pico-pro', 'pico-c', 'z', 'zymatic'].includes(draft.source_model || '')
          ? draft.source_model
          : undefined
      const saved = await api<Recipe>(recipeSavePath(isNew ? '' : id, targetModel), {
        method: isNew ? 'POST' : 'PUT',
        body: { ...payload, machine_versions: undefined },
      })
      setRecipe(saved)
      originalRevision.current = saved.revision
      setDirty(false)
      await hub.refresh()
      hub.notify(
        saved.machine_versions?.length === 5 &&
          saved.machine_versions.every((v) => v.machine_recipe && !v.deployment_blockers?.length)
          ? 'Recipe saved with versions for all five brewers.'
          : 'Recipe saved to your library.'
      )
      if (isNew) hub.navigate(guided ? '/recipes' : `/recipes/${saved.id}`)
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  async function exportRecipe(format: string) {
    try {
      await download(
        `/recipes/${id}/export?format=${format}`,
        `${slug(recipe.name)}.${format === 'beerxml' ? 'xml' : 'json'}`
      )
    } catch (error) {
      hub.notify(errorMessage(error), 'error')
    }
  }
  if (!isNew && loaded.loading) return <Loading label="Opening your recipe…" />
  if (!isNew && loaded.error) return <ErrorBanner message={loaded.error} retry={loaded.reload} />
  const nativeRecipe = isNew ? recipe : loaded.data
  if (nativeRecipe?.native_program?.z_program) return <ZEditor initial={nativeRecipe} />
  if (nativeRecipe && isNative(nativeRecipe))
    return <NativeRecipeEditor initial={nativeRecipe} isNew={isNew} />
  if (guided && isNew)
    return (
      <RecipeWizard
        busy={busy}
        error={error}
        save={(draft) => void save(undefined, draft)}
        advanced={(draft) => {
          setRecipe(draft)
          setError('')
          setGuided(false)
        }}
        back={() => hub.navigate('/recipes')}
      />
    )
  const versions = recipe.machine_versions || []
  const plan = dirty ? undefined : versions.find((v) => v.profile.id === viewModel)
  const shown = plan ? (plan.recipe.ingredients?.length ? plan.recipe : undefined) : recipe
  const sourceID =
    recipe.provenance?.source_id === 'public-recipes' ? recipe.provenance.catalog_id : undefined
  return (
    <>
      <PageTitle
        back={() => {
          if (!dirty || window.confirm('Leave this recipe without saving your changes?'))
            hub.navigate('/recipes')
        }}
        eyebrow={
          isNew
            ? 'A FRESH START'
            : `RECIPE · REVISION ${recipe.revision}${dirty ? ' · UNSAVED CHANGES' : ''}`
        }
        title={isNew ? 'What will you brew next?' : recipe.name || 'Untitled recipe'}
        actions={
          <>
            {!isNew && (
              <Button
                variant="secondary"
                disabled={dirty}
                onClick={() => setDeploy(true)}
                title={dirty ? 'Save your changes before sending this recipe' : ''}
              >
                <Send size={16} />
                Send
              </Button>
            )}
            <Button disabled={busy || !!plan} onClick={() => void save()}>
              {busy ? <Loader2 size={16} className="spin" /> : <Save size={16} />}Save recipe
            </Button>
          </>
        }
      />
      <ErrorBanner message={error} />
      <ReferenceLists />
      <div className="editor-layout">
        <div className="editor-main">
          <section className="panel">
            <SectionHeading
              title="Recipe details"
              actions={sourceID && <OriginalRecipeLink id={sourceID} />}
            />
            {!isNew && versions.length > 0 && (
              <Field
                label="Recipe view"
                hint={dirty ? 'Save your changes to update the brewer quantities.' : undefined}
              >
                <select
                  value={plan ? plan.profile.id : ''}
                  onChange={(e) => setViewModel(e.target.value)}
                >
                  <option value="">Edit base recipe</option>
                  {versions.map((v) => (
                    <option key={v.profile.id} value={v.profile.id} disabled={dirty}>
                      {modelLabel(v.profile.id)}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            {plan ? (
              <>
                <BrewerPlan plan={plan} />
                {plan.profile.id === 'z' && plan.machine_recipe && (
                  <Button variant="secondary" onClick={() => void exportRecipe('z-program')}>
                    <Download size={16} />
                    Export for Z Program Maker
                  </Button>
                )}
              </>
            ) : (
              <>
                <MeasureChoice />
                <div className="form-grid">
                  <Field label="Recipe name">
                    <input
                      value={recipe.name}
                      onChange={(event) => update({ name: event.target.value })}
                      placeholder="Sunday afternoon pale ale"
                      maxLength={100}
                      required
                    />
                  </Field>
                  <RecipeStyleField value={recipe.style} change={(style) => update({ style })} />
                </div>
                <Field label="Description">
                  <textarea
                    rows={2}
                    value={recipe.description}
                    onChange={(event) => update({ description: event.target.value })}
                    placeholder="The flavors and character you're aiming for…"
                  />
                </Field>
                <div className="form-grid thirds">
                  <QuantityField
                    label={recipe.estimated_yield ? 'Estimated yield' : 'Batch size'}
                    unit="L"
                    value={recipe.batch_liters}
                    change={(v) => update({ batch_liters: Number(v), estimated_yield: false })}
                  />
                  <Field label="Brewhouse efficiency (%)">
                    <input
                      type="number"
                      min="1"
                      max="100"
                      value={recipe.efficiency}
                      onChange={(event) => update({ efficiency: Number(event.target.value) })}
                    />
                  </Field>
                  <Field label="Tags">
                    <input
                      value={tagsInput}
                      onChange={(event) => {
                        setTagsInput(event.target.value)
                        setDirty(true)
                      }}
                      placeholder="citrus, weekend"
                    />
                  </Field>
                </div>
              </>
            )}
          </section>
          {shown && <BeerCrafter recipe={shown} />}
          {plan ? (
            <>
              {shown?.notes && (
                <section className="panel">
                  <SectionHeading title="Notes for brew day" />
                  <p className="preserve-lines">{shown.notes}</p>
                </section>
              )}
            </>
          ) : (
            <>
              <RecipeIngredients
                items={recipe.ingredients}
                sourceYeast={unmeasuredYeast(recipe)}
                change={(ingredients) => update({ ingredients })}
              />
              <RecipeSchedule
                model={recipe.source_model}
                steps={recipe.steps}
                unit={hub.unit}
                change={(steps) => update({ steps })}
              />
              <section className="panel">
                <SectionHeading title="Boil planning" />
                <div className="form-grid thirds">
                  <Field label="Recipe type">
                    <select
                      value={recipe.type || ''}
                      onChange={(event) => update({ type: event.target.value })}
                    >
                      <option value="">Not specified</option>
                      <option>All Grain</option>
                      <option>Partial Mash</option>
                      <option>Extract</option>
                    </select>
                  </Field>
                  <QuantityField
                    label="Boil volume"
                    unit="L"
                    value={recipe.boil_liters || ''}
                    change={(v) => update({ boil_liters: Number(v) })}
                  />
                  <Field label="Boil duration (min)">
                    <input
                      type="number"
                      min="0"
                      value={recipe.boil_minutes || ''}
                      onChange={(event) => update({ boil_minutes: Number(event.target.value) })}
                    />
                  </Field>
                </div>
              </section>
              <FermentationPlan recipe={recipe} change={update} />
              <ConditioningFields recipe={recipe} change={update} allowPitchStep />
              <section className="panel">
                <SectionHeading title="Notes for brew day" />
                <Field label="Recipe notes">
                  <textarea
                    rows={4}
                    value={recipe.notes}
                    onChange={(event) => update({ notes: event.target.value })}
                    placeholder="Water treatment, preparation, tasting goals…"
                  />
                </Field>
                <Field
                  label="Additional recipe metadata"
                  hint="Additional source and exchange fields, stored as JSON."
                >
                  <textarea
                    className="code-input"
                    rows={5}
                    value={advanced ?? JSON.stringify(metadata(recipe), null, 2)}
                    onChange={(event) => {
                      setAdvanced(event.target.value)
                      setDirty(true)
                    }}
                  />
                </Field>
              </section>
            </>
          )}
        </div>
        <aside className="editor-aside">
          {shown && (
            <div className="recipe-summary">
              <p className="eyebrow">ON THE BREW SHEET</p>
              <Wheat size={38} strokeWidth={1.2} />
              <h3>{shown.name || 'A recipe in the making'}</h3>
              <dl>
                <div>
                  <dt>{plan ? 'Estimated yield' : 'Batch'}</dt>
                  <dd>{quantity(shown.batch_liters, 'L')}</dd>
                </div>
                <div>
                  <dt>Ingredients</dt>
                  <dd>{shown.ingredients.length}</dd>
                </div>
                <div>
                  <dt>Schedule</dt>
                  <dd>
                    {shown.steps.reduce(
                      (total, step) => total + step.minutes + step.drain_minutes,
                      0
                    )}{' '}
                    min
                  </dd>
                </div>
                <div>
                  <dt>Steps</dt>
                  <dd>{shown.steps.length}</dd>
                </div>
              </dl>
            </div>
          )}
          <div className="panel small-panel">
            <h3>Make it your own.</h3>
            <p>Need a starting point or another perspective?</p>
            <Button
              variant="secondary"
              className="full-width"
              onClick={() =>
                hub.navigate(`/recipes/create-with-ai?recipe=${encodeURIComponent(recipe.id)}`)
              }
            >
              <Sparkles size={15} />
              Ask the brew assistant
            </Button>
          </div>
          {!isNew && (
            <div className="editor-tools">
              <Button variant="ghost" onClick={() => void exportRecipe('json')}>
                <Download size={15} />
                Export JSON
              </Button>
              <Button variant="ghost" onClick={() => void exportRecipe('beerxml')}>
                <Download size={15} />
                Export BeerXML
              </Button>
              <Button variant="ghost" onClick={() => setRevisions(true)}>
                <History size={15} />
                Revision history
              </Button>
              <Button
                variant="ghost"
                onClick={() => {
                  hub.setDraft({ ...recipe, id: '', revision: 0, name: `${recipe.name} — copy` })
                  hub.navigate('/recipes/new')
                }}
              >
                <Copy size={15} />
                Duplicate recipe
              </Button>
              <Button variant="ghost" className="danger-text" onClick={() => setRemove(true)}>
                <Trash2 size={15} />
                Delete recipe
              </Button>
            </div>
          )}
        </aside>
      </div>
      {deploy && (
        <AdaptDialog
          recipe={recipe}
          initialModel={plan ? plan.profile.id : undefined}
          onClose={() => setDeploy(false)}
        />
      )}
      {remove && (
        <Confirm
          title="Delete this recipe?"
          description="This removes it from the shared library. Existing brew history and recipes already on machines are retained."
          label="Delete recipe"
          danger
          busy={busy}
          onClose={() => setRemove(false)}
          onConfirm={async () => {
            setBusy(true)
            try {
              await api(`/recipes/${id}`, { method: 'DELETE' })
              await hub.refresh()
              hub.notify('Recipe removed.')
              hub.navigate('/recipes')
            } catch (error) {
              setError(errorMessage(error))
              setRemove(false)
            } finally {
              setBusy(false)
            }
          }}
        />
      )}{' '}
      {revisions && (
        <RevisionDialog
          id={id}
          onClose={() => setRevisions(false)}
          onLoad={(old) => {
            setRecipe({ ...old, id, revision: originalRevision.current })
            setTagsInput((old.tags || []).join(', '))
            setAdvanced('')
            setDirty(true)
            setRevisions(false)
            hub.notify(`Revision ${old.revision} loaded. Save to create a new revision.`)
          }}
        />
      )}
    </>
  )
}
function ImportDialog({
  onClose,
  initialFormat = 'beerxml',
}: {
  onClose: () => void
  initialFormat?: string
}) {
  const hub = useHub()
  const [format, setFormat] = useState(initialFormat)
  const [data, setData] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function submit() {
    setBusy(true)
    setError('')
    try {
      const items = await api<Recipe[]>('/recipes/import', {
        method: 'POST',
        body: { format, data },
      })
      await hub.refresh()
      hub.notify(`Imported ${items.length} ${items.length === 1 ? 'recipe' : 'recipes'}.`)
      onClose()
      if (items.length === 1) hub.navigate(`/recipes/${items[0].id}`)
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title="Bring your recipes along."
      onClose={onClose}
      wide
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!data.trim() || busy} onClick={() => void submit()}>
            <Upload size={16} />
            {busy ? 'Importing…' : 'Import recipes'}
          </Button>
        </>
      }
    >
      <p>
        Import BeerXML, Hub recipes, original PicoPak files, or Z Program Maker JSON. Recipes are added as new library
        entries.
      </p>
      <div className="form-grid">
        <Field label="Format">
          <select value={format} onChange={(event) => setFormat(event.target.value)}>
            <option value="beerxml">BeerXML (.xml)</option>
            <option value="json">PicoBrew recipe JSON (.json)</option>
          </select>
        </Field>
        <Field label="Choose a file">
          <input
            type="file"
            accept=".xml,.json,application/json,application/xml,text/xml"
            onChange={async (event) => {
              const file = event.target.files?.[0]
              if (file) {
                setFormat(file.name.toLowerCase().endsWith('.json') ? 'json' : 'beerxml')
                setData(await file.text())
              }
            }}
          />
        </Field>
      </div>
      <Field label="Or paste the recipe">
        <textarea
          className="code-input"
          rows={10}
          value={data}
          onChange={(event) => setData(event.target.value)}
          placeholder={format === 'beerxml' ? '<RECIPES>…</RECIPES>' : '{"name": "Your recipe", …}'}
        />
      </Field>
      <ErrorBanner message={error} />
    </Modal>
  )
}
function ShoppingList({ ids, onClose }: { ids: string[]; onClose: () => void }) {
  const resource = useResource<(Ingredient & { amount_unspecified?: boolean })[]>(
    `/shopping-list?recipes=${encodeURIComponent(ids.join(','))}`
  )
  return (
    <Modal title="Your brew-day shopping list" onClose={onClose}>
      <p>
        Combined ingredients for {ids.length} selected recipes. Different units remain separate.
      </p>
      {resource.loading ? (
        <Loading />
      ) : resource.error ? (
        <ErrorBanner message={resource.error} />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Ingredient</th>
              <th>Kind</th>
              <th>Amount</th>
            </tr>
          </thead>
          <tbody>
            {resource.data?.map((item, index) => (
              <tr key={index}>
                <td>{item.name}</td>
                <td>{item.kind}</td>
                <td>
                  {item.amount_unspecified
                    ? 'Pitch amount unspecified'
                    : formatQuantity(item.amount, item.unit)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Modal>
  )
}
function RevisionDialog({
  id,
  onClose,
  onLoad,
}: {
  id: string
  onClose: () => void
  onLoad: (recipe: Recipe) => void
}) {
  const resource = useResource<Recipe[]>(`/recipes/${id}/revisions`)
  return (
    <Modal title="Recipe revision history" onClose={onClose}>
      <p>Load a prior version into the editor, then save it as a new revision.</p>
      {resource.loading ? (
        <Loading />
      ) : resource.error ? (
        <ErrorBanner message={resource.error} />
      ) : (
        <div className="revision-list">
          {resource.data?.map((recipe) => (
            <div key={recipe.revision}>
              <div>
                <strong>Revision {recipe.revision}</strong>
                <small>
                  {dateLabel(recipe.updated_at)} · {recipe.name}
                </small>
              </div>
              <Button variant="secondary" onClick={() => onLoad(recipe)}>
                Load revision
              </Button>
            </div>
          ))}
        </div>
      )}
    </Modal>
  )
}
